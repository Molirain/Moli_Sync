using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using MoliSync.Client.Http;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Services;

/// <summary>
/// HTTP data-plane for durable records and blobs. The control-plane WebSocket is
/// only a change signal; all record contents come from these endpoints.
/// </summary>
public sealed class CloudRepository
{
    private readonly HttpClient _http;
    private readonly Uri _baseUri;
    private readonly DataDirectoryManager _dataDirectory;
    private readonly IClientLog _log;

    public CloudRepository(
        HttpClient http,
        string serverUrl,
        DataDirectoryManager dataDirectory,
        IClientLog log)
    {
        _http = http ?? throw new ArgumentNullException(nameof(http));
        _dataDirectory = dataDirectory ?? throw new ArgumentNullException(nameof(dataDirectory));
        _log = log ?? throw new ArgumentNullException(nameof(log));
        _baseUri = new Uri(serverUrl.TrimEnd('/') + "/", UriKind.Absolute);
    }

    public async Task<IReadOnlyList<CloudRecord>> ListAsync(
        string? kind,
        int limit,
        CancellationToken cancellationToken)
    {
        var query = new List<string> { $"limit={Math.Clamp(limit, 1, 200)}" };
        if (!string.IsNullOrWhiteSpace(kind))
        {
            query.Add("kind=" + Uri.EscapeDataString(kind));
        }

        using var request = new HttpRequestMessage(
            HttpMethod.Get,
            new Uri(_baseUri, "api/v1/records?" + string.Join("&", query)));
        using var response = await _http
            .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
            .ConfigureAwait(false);
        var body = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw CreateError(response.StatusCode, body);
        }

        var parsed = JsonSerializer.Deserialize(body, CloudJsonContext.Default.RecordsResponse);
        return parsed?.Records ?? new List<CloudRecord>();
    }

    public async Task<CloudRecord> PostTextAsync(string text, CancellationToken cancellationToken)
    {
        var payload = JsonSerializer.SerializeToUtf8Bytes(
            new TextRecordRequest { Text = text },
            CloudJsonContext.Default.TextRecordRequest);
        using var content = new ByteArrayContent(payload);
        content.Headers.ContentType = new MediaTypeHeaderValue("application/json")
        {
            CharSet = "utf-8",
        };
        using var request = new HttpRequestMessage(
            HttpMethod.Post,
            new Uri(_baseUri, "api/v1/records/text"))
        {
            Content = content,
        };
        return await SendForRecordAsync(request, cancellationToken).ConfigureAwait(false);
    }

    public async Task<CloudRecord> UploadAsync(
        string kind,
        string fileName,
        string mimeType,
        Stream source,
        long length,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        if (kind is not (CloudKinds.Image or CloudKinds.File))
        {
            throw new ArgumentException("kind must be image or file", nameof(kind));
        }

        var query =
            $"kind={Uri.EscapeDataString(kind)}" +
            $"&name={Uri.EscapeDataString(fileName)}" +
            $"&mime={Uri.EscapeDataString(mimeType)}";
        using var request = new HttpRequestMessage(
            HttpMethod.Post,
            new Uri(_baseUri, "api/v1/records/blob?" + query));
        using var streamContent = new StreamContent(new ProgressReadStream(source, progress));
        streamContent.Headers.ContentLength = length;
        streamContent.Headers.ContentType = new MediaTypeHeaderValue(
            string.IsNullOrWhiteSpace(mimeType) ? "application/octet-stream" : mimeType);
        request.Content = streamContent;

        _log.Info($"uploading {fileName} ({length} bytes) as {kind}");
        return await SendForRecordAsync(request, cancellationToken).ConfigureAwait(false);
    }

    public Task<DownloadedBlob> DownloadAsync(
        CloudRecord record,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        if (!record.HasBlob)
        {
            throw new InvalidOperationException("record has no blob");
        }

        var descriptor = new BlobDescriptor(record.BlobId!, record.FileName ?? record.BlobId!, record.FileSize);
        return _dataDirectory.WithDownloadGateAsync(
            directory => new BlobDownloader(_http, _baseUri, directory, _log)
                .DownloadAsync(descriptor, progress, cancellationToken),
            cancellationToken);
    }

    /// <summary>
    /// Downloads a blob into a per-record cache directory so thumbnails and
    /// previews are fetched once and reused. Safe to call repeatedly.
    /// </summary>
    public async Task<string> EnsureCachedAsync(
        CloudRecord record,
        CancellationToken cancellationToken)
    {
        if (!record.HasBlob)
        {
            throw new InvalidOperationException("record has no blob");
        }

        var directory = Path.Combine(_dataDirectory.Current, "cache", record.RecordId);
        var descriptor = new BlobDescriptor(record.BlobId!, record.FileName ?? record.BlobId!, record.FileSize);
        var downloader = new BlobDownloader(_http, _baseUri, directory, _log);
        var result = await _dataDirectory.WithDownloadGateAsync(
            _ => downloader.DownloadAsync(descriptor, null, cancellationToken),
            cancellationToken).ConfigureAwait(false);
        return result.FilePath;
    }

    public async Task DeleteAsync(string recordId, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(
            HttpMethod.Delete,
            new Uri(_baseUri, "api/v1/records/" + Uri.EscapeDataString(recordId)));
        using var response = await _http
            .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
            .ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            var body = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
            throw CreateError(response.StatusCode, body);
        }
    }

    private async Task<CloudRecord> SendForRecordAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        using var response = await _http
            .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
            .ConfigureAwait(false);
        var body = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw CreateError(response.StatusCode, body);
        }

        var record = JsonSerializer.Deserialize(body, CloudJsonContext.Default.CloudRecord);
        return record ?? throw new CloudException("server returned an empty record");
    }

    private static CloudException CreateError(HttpStatusCode status, byte[] body)
    {
        try
        {
            var envelope = JsonSerializer.Deserialize(body, CloudJsonContext.Default.ApiErrorEnvelope);
            if (envelope?.Error is { } error && !string.IsNullOrWhiteSpace(error.Message))
            {
                return new CloudException(error.Message, (int)status, error.Code);
            }
        }
        catch (JsonException)
        {
            // Fall through to the generic message.
        }

        var text = body.Length == 0
            ? string.Empty
            : Encoding.UTF8.GetString(body);
        if (text.Length > 200)
        {
            text = text[..200];
        }
        return new CloudException(
            $"服务器返回 HTTP {(int)status}" + (text.Length == 0 ? string.Empty : $"：{text}"),
            (int)status);
    }

    private sealed class ProgressReadStream : Stream
    {
        private readonly Stream _inner;
        private readonly IProgress<long>? _progress;
        private long _read;

        public ProgressReadStream(Stream inner, IProgress<long>? progress)
        {
            _inner = inner ?? throw new ArgumentNullException(nameof(inner));
            _progress = progress;
        }

        public override bool CanRead => true;

        public override bool CanSeek => false;

        public override bool CanWrite => false;

        public override long Length => _inner.Length;

        public override long Position
        {
            get => _read;
            set => throw new NotSupportedException();
        }

        public override int Read(byte[] buffer, int offset, int count)
        {
            var read = _inner.Read(buffer, offset, count);
            Report(read);
            return read;
        }

        public override async ValueTask<int> ReadAsync(
            Memory<byte> buffer,
            CancellationToken cancellationToken = default)
        {
            var read = await _inner.ReadAsync(buffer, cancellationToken).ConfigureAwait(false);
            Report(read);
            return read;
        }

        public override Task<int> ReadAsync(
            byte[] buffer,
            int offset,
            int count,
            CancellationToken cancellationToken) =>
            ReadAsync(buffer.AsMemory(offset, count), cancellationToken).AsTask();

        private void Report(int read)
        {
            if (read <= 0)
            {
                return;
            }
            _read += read;
            _progress?.Report(_read);
        }

        public override void Flush()
        {
        }

        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();

        public override void SetLength(long value) => throw new NotSupportedException();

        public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();

        protected override void Dispose(bool disposing)
        {
            // The caller owns the source stream; StreamContent may dispose us.
            base.Dispose(disposing);
        }
    }
}
