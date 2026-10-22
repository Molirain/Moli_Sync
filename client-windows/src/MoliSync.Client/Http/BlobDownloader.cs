using System.Buffers;
using System.Collections.Concurrent;
using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.RegularExpressions;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Http;

public sealed record BlobDescriptor(string BlobId, string FileName, long FileSize);

public sealed record DownloadedBlob(string FilePath, long Size);

/// <summary>
/// Resumable blob downloader. The partial file is kept under a stable name and
/// resumed with an HTTP Range request; a completed file is moved atomically
/// into place. Only the size is validated here because the content hash belongs
/// to the protocol metadata and the server is the authoritative blob source.
/// </summary>
public sealed partial class BlobDownloader
{
    private const int BufferSize = 128 * 1024;
    private const int MaxAttempts = 3;
    private const int MaxRangeRequests = 10_000;
    private const int MaxFileNameLength = 120;

    [GeneratedRegex("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$", RegexOptions.CultureInvariant)]
    private static partial Regex BlobIdPattern();

    private static readonly HashSet<char> InvalidFileNameChars = Path.GetInvalidFileNameChars().ToHashSet();
    private static readonly HashSet<string> ReservedDeviceNames = new(StringComparer.OrdinalIgnoreCase)
    {
        "CON", "PRN", "AUX", "NUL",
        "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
    };

    private readonly HttpClient _http;
    private readonly string _directory;
    private readonly string _directoryPrefix;
    private readonly IClientLog _log;
    private readonly ConcurrentDictionary<string, SemaphoreSlim> _blobLocks = new(StringComparer.Ordinal);

    public BlobDownloader(HttpClient httpClient, string downloadDirectory, IClientLog log)
    {
        _http = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _log = log ?? throw new ArgumentNullException(nameof(log));

        _directory = Path.GetFullPath(downloadDirectory);
        Directory.CreateDirectory(_directory);
        _directoryPrefix = Path.EndsInDirectorySeparator(_directory)
            ? _directory
            : _directory + Path.DirectorySeparatorChar;
    }

    public async Task<DownloadedBlob> DownloadAsync(
        BlobDescriptor blob,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        ValidateBlob(blob);

        var gate = _blobLocks.GetOrAdd(blob.BlobId, static _ => new SemaphoreSlim(1, 1));
        await gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            return await DownloadCoreAsync(blob, progress, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            gate.Release();
        }
    }

    private async Task<DownloadedBlob> DownloadCoreAsync(
        BlobDescriptor blob,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        var targetPath = ResolveTargetPath(blob);
        if (File.Exists(targetPath) && new FileInfo(targetPath).Length == blob.FileSize)
        {
            _log.Debug($"blob {blob.BlobId} is already complete at {targetPath}");
            progress?.Report(blob.FileSize);
            return new DownloadedBlob(targetPath, blob.FileSize);
        }

        var partialPath = Path.Combine(_directory, blob.BlobId + ".part");

        for (var attempt = 1; ; attempt++)
        {
            try
            {
                return await DownloadAttemptAsync(blob, partialPath, targetPath, progress, cancellationToken)
                    .ConfigureAwait(false);
            }
            catch (Exception exception) when (
                attempt < MaxAttempts &&
                IsTransient(exception) &&
                !cancellationToken.IsCancellationRequested)
            {
                var delay = TimeSpan.FromMilliseconds(250 * attempt);
                _log.Warn(
                    $"blob {blob.BlobId} attempt {attempt} failed; retrying in {delay.TotalMilliseconds:F0} ms",
                    exception);
                await Task.Delay(delay, cancellationToken).ConfigureAwait(false);
            }
        }
    }

    private async Task<DownloadedBlob> DownloadAttemptAsync(
        BlobDescriptor blob,
        string partialPath,
        string targetPath,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        for (var rangeRequest = 0; rangeRequest < MaxRangeRequests; rangeRequest++)
        {
            var existing = File.Exists(partialPath) ? new FileInfo(partialPath).Length : 0;
            if (existing > blob.FileSize)
            {
                _log.Warn($"discarding oversized partial file for blob {blob.BlobId}");
                File.Delete(partialPath);
                existing = 0;
            }
            if (existing == blob.FileSize && File.Exists(partialPath))
            {
                return FinalizeDownload(partialPath, targetPath, blob);
            }

            using var request = new HttpRequestMessage(
                HttpMethod.Get,
                $"api/v1/blobs/{Uri.EscapeDataString(blob.BlobId)}");
            if (existing > 0)
            {
                request.Headers.Range = new RangeHeaderValue(existing, null);
            }

            using var response = await _http
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);

            if (response.StatusCode == HttpStatusCode.RequestedRangeNotSatisfiable)
            {
                if (existing == blob.FileSize && File.Exists(partialPath))
                {
                    return FinalizeDownload(partialPath, targetPath, blob);
                }
                throw new HttpRequestException(
                    $"server rejected the resume range for blob {blob.BlobId}",
                    inner: null,
                    response.StatusCode);
            }

            var append = false;
            if (response.StatusCode == HttpStatusCode.PartialContent)
            {
                ValidateContentRange(response, existing, blob.FileSize);
                append = existing > 0;
            }
            else if (response.StatusCode == HttpStatusCode.OK)
            {
                // The server ignored the Range header (or this is a fresh download).
                // Restart from zero so bytes are never duplicated.
                if (existing > 0)
                {
                    _log.Debug($"server ignored Range for blob {blob.BlobId}; restarting the partial download");
                }
                existing = 0;
                append = false;

                if (response.Content.Headers.ContentLength is { } contentLength && contentLength != blob.FileSize)
                {
                    throw new InvalidDataException(
                        $"blob {blob.BlobId} declared {blob.FileSize} bytes but the server sent {contentLength}");
                }
            }
            else
            {
                throw new HttpRequestException(
                    $"blob download failed with HTTP {(int)response.StatusCode}",
                    inner: null,
                    response.StatusCode);
            }

            var total = existing;
            progress?.Report(total);

            await using (var partial = new FileStream(
                partialPath,
                append ? FileMode.Append : FileMode.Create,
                FileAccess.Write,
                FileShare.None,
                BufferSize,
                FileOptions.Asynchronous | FileOptions.SequentialScan))
            {
                await using var source = await response.Content
                    .ReadAsStreamAsync(cancellationToken)
                    .ConfigureAwait(false);
                var buffer = ArrayPool<byte>.Shared.Rent(BufferSize);
                try
                {
                    while (true)
                    {
                        var read = await source.ReadAsync(buffer.AsMemory(0, BufferSize), cancellationToken)
                            .ConfigureAwait(false);
                        if (read == 0)
                        {
                            break;
                        }
                        if (total + read > blob.FileSize)
                        {
                            throw new InvalidDataException(
                                $"server sent more than {blob.FileSize} bytes for blob {blob.BlobId}");
                        }

                        await partial.WriteAsync(buffer.AsMemory(0, read), cancellationToken).ConfigureAwait(false);
                        total += read;
                        progress?.Report(total);
                    }
                }
                finally
                {
                    ArrayPool<byte>.Shared.Return(buffer);
                }

                await partial.FlushAsync(cancellationToken).ConfigureAwait(false);
            }

            if (total == blob.FileSize)
            {
                return FinalizeDownload(partialPath, targetPath, blob);
            }

            if (total <= existing)
            {
                throw new InvalidDataException(
                    $"server returned no new data for blob {blob.BlobId} at offset {total}");
            }

            // The server may cap each response to a smaller range. Continue
            // requesting the remainder instead of failing the transfer.
            _log.Debug($"blob {blob.BlobId} continuing at offset {total} of {blob.FileSize}");
        }

        throw new InvalidDataException(
            $"blob {blob.BlobId} required more than {MaxRangeRequests} range requests");
    }

    private DownloadedBlob FinalizeDownload(string partialPath, string targetPath, BlobDescriptor blob)
    {
        var partialInfo = new FileInfo(partialPath);
        if (!partialInfo.Exists || partialInfo.Length != blob.FileSize)
        {
            throw new InvalidDataException($"partial file for blob {blob.BlobId} has an unexpected size");
        }

        for (var index = 0; index < 1000; index++)
        {
            var candidate = index == 0 ? targetPath : AppendSuffix(targetPath, index);
            EnsureInsideDirectory(candidate);
            try
            {
                File.Move(partialPath, candidate, overwrite: false);
                _log.Info($"downloaded blob {blob.BlobId} ({blob.FileSize} bytes) to {candidate}");
                return new DownloadedBlob(candidate, blob.FileSize);
            }
            catch (IOException) when (File.Exists(candidate))
            {
                // Another process created the name first; try the next suffix.
            }
        }

        throw new IOException($"cannot allocate a destination name for blob {blob.BlobId}");
    }

    private string ResolveTargetPath(BlobDescriptor blob)
    {
        var baseName = SanitizeFileName(blob.FileName, blob.BlobId);
        for (var index = 0; index < 1000; index++)
        {
            var candidate = index == 0
                ? Path.Combine(_directory, baseName)
                : Path.Combine(_directory, AppendSuffix(baseName, index));
            EnsureInsideDirectory(candidate);

            if (!File.Exists(candidate))
            {
                return candidate;
            }
            if (new FileInfo(candidate).Length == blob.FileSize)
            {
                return candidate;
            }
        }

        throw new IOException($"cannot allocate a destination name for blob {blob.BlobId}");
    }

    private void EnsureInsideDirectory(string path)
    {
        var fullPath = Path.GetFullPath(path);
        if (!fullPath.StartsWith(_directoryPrefix, StringComparison.OrdinalIgnoreCase))
        {
            throw new IOException("resolved download path escapes the download directory");
        }
    }

    internal static string SanitizeFileName(string? fileName, string fallback)
    {
        var value = string.IsNullOrWhiteSpace(fileName) ? fallback : fileName;
        value = value.Replace('\\', '/');
        var lastSlash = value.LastIndexOf('/');
        if (lastSlash >= 0)
        {
            value = value[(lastSlash + 1)..];
        }

        var builder = new StringBuilder(value.Length);
        foreach (var character in value)
        {
            builder.Append(InvalidFileNameChars.Contains(character) ? '_' : character);
        }

        value = builder.ToString().Trim().TrimEnd('.');
        if (value.Length == 0)
        {
            value = fallback;
        }

        if (ReservedDeviceNames.Contains(Path.GetFileNameWithoutExtension(value)))
        {
            value = "_" + value;
        }

        if (value.Length > MaxFileNameLength)
        {
            var extension = Path.GetExtension(value);
            if (extension.Length > 20)
            {
                extension = string.Empty;
            }
            value = value[..(MaxFileNameLength - extension.Length)] + extension;
        }

        return value;
    }

    private static string AppendSuffix(string path, int index)
    {
        var directory = Path.GetDirectoryName(path) ?? string.Empty;
        var name = Path.GetFileNameWithoutExtension(path);
        var extension = Path.GetExtension(path);
        return Path.Combine(directory, $"{name} ({index}){extension}");
    }

    private static void ValidateContentRange(
        HttpResponseMessage response,
        long expectedStart,
        long expectedTotal)
    {
        var range = response.Content.Headers.ContentRange;
        if (range is null || !range.HasRange || range.From != expectedStart)
        {
            throw new InvalidDataException("server returned an unexpected Content-Range");
        }
        if (range.Length is { } length && length != expectedTotal)
        {
            throw new InvalidDataException(
                $"server reported a total of {length} bytes, expected {expectedTotal}");
        }
        if (range.To is not { } end || end < expectedStart || end >= expectedTotal)
        {
            throw new InvalidDataException("server returned an unexpected range end");
        }
    }

    private static void ValidateBlob(BlobDescriptor blob)
    {
        ArgumentNullException.ThrowIfNull(blob);
        if (string.IsNullOrWhiteSpace(blob.BlobId) || !BlobIdPattern().IsMatch(blob.BlobId))
        {
            throw new ArgumentException("blob id contains unsupported characters", nameof(blob));
        }
        if (blob.FileSize < 0)
        {
            throw new ArgumentOutOfRangeException(nameof(blob), "blob size must not be negative");
        }
    }

    private static bool IsTransient(Exception exception)
    {
        if (exception is HttpRequestException { StatusCode: { } statusCode })
        {
            return (int)statusCode >= 500;
        }
        return exception is HttpRequestException or IOException;
    }
}
