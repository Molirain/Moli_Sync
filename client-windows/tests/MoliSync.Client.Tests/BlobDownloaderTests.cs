using System.Net;
using System.Net.Http.Headers;
using MoliSync.Client.Http;
using MoliSync.Client.Logging;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class BlobDownloaderTests
{
    [Fact]
    public async Task ResumesFromExistingPartialFile()
    {
        var content = CreateContent(256);
        using var directory = new TempDirectory();
        await File.WriteAllBytesAsync(Path.Combine(directory.Path, "blob1.part"), content[..100]);

        var handler = new RangeHttpHandler(content);
        using var http = CreateClient(handler);
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        var result = await downloader.DownloadAsync(
            new BlobDescriptor("blob1", "report.bin", content.Length),
            progress: null,
            CancellationToken.None);

        Assert.Equal(content, await File.ReadAllBytesAsync(result.FilePath));
        Assert.False(File.Exists(Path.Combine(directory.Path, "blob1.part")));
        Assert.Equal(100, handler.Ranges[0]!.Ranges.First().From);
    }

    [Fact]
    public async Task RestartsWhenServerIgnoresRange()
    {
        var content = CreateContent(128);
        using var directory = new TempDirectory();
        var partial = Path.Combine(directory.Path, "blob2.part");
        await File.WriteAllBytesAsync(partial, content[..64]);

        var handler = new RangeHttpHandler(content) { IgnoreRange = true };
        using var http = CreateClient(handler);
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        var result = await downloader.DownloadAsync(
            new BlobDescriptor("blob2", "payload.bin", content.Length),
            progress: null,
            CancellationToken.None);

        Assert.Equal(content, await File.ReadAllBytesAsync(result.FilePath));
        Assert.Equal(64, handler.Ranges[0]!.Ranges.First().From);
    }

    [Fact]
    public async Task FinalizesCompletePartialFileWithoutHttp()
    {
        var content = CreateContent(64);
        using var directory = new TempDirectory();
        await File.WriteAllBytesAsync(Path.Combine(directory.Path, "blob3.part"), content);

        var handler = new RangeHttpHandler(content);
        using var http = CreateClient(handler);
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        var result = await downloader.DownloadAsync(
            new BlobDescriptor("blob3", "done.bin", content.Length),
            progress: null,
            CancellationToken.None);

        Assert.Empty(handler.Ranges);
        Assert.Equal(content, await File.ReadAllBytesAsync(result.FilePath));
    }

    [Fact]
    public async Task HandlesZeroByteBlob()
    {
        using var directory = new TempDirectory();
        var handler = new RangeHttpHandler(Array.Empty<byte>());
        using var http = CreateClient(handler);
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        var result = await downloader.DownloadAsync(
            new BlobDescriptor("blob4", "empty.txt", 0),
            progress: null,
            CancellationToken.None);

        Assert.True(File.Exists(result.FilePath));
        Assert.Equal(0, new FileInfo(result.FilePath).Length);
    }

    [Fact]
    public async Task SanitizesPathTraversalFileName()
    {
        var content = CreateContent(16);
        using var directory = new TempDirectory();
        var handler = new RangeHttpHandler(content);
        using var http = CreateClient(handler);
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        var result = await downloader.DownloadAsync(
            new BlobDescriptor("blob5", @"..\..\evil\payload.txt", content.Length),
            progress: null,
            CancellationToken.None);

        Assert.Equal("payload.txt", Path.GetFileName(result.FilePath));
        Assert.StartsWith(
            directory.Path + Path.DirectorySeparatorChar,
            Path.GetFullPath(result.FilePath),
            StringComparison.OrdinalIgnoreCase);
        Assert.Equal(content, await File.ReadAllBytesAsync(result.FilePath));
    }

    [Fact]
    public async Task RejectsUnsafeBlobIdentifier()
    {
        using var directory = new TempDirectory();
        using var http = CreateClient(new RangeHttpHandler(Array.Empty<byte>()));
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        await Assert.ThrowsAsync<ArgumentException>(
            () => downloader.DownloadAsync(
                new BlobDescriptor("../escape", "file.bin", 1),
                progress: null,
                CancellationToken.None));
    }

    [Fact]
    public async Task DeclaredSizeMismatchFails()
    {
        var content = CreateContent(32);
        using var directory = new TempDirectory();
        var handler = new RangeHttpHandler(content);
        using var http = CreateClient(handler);
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);

        await Assert.ThrowsAsync<InvalidDataException>(
            () => downloader.DownloadAsync(
                new BlobDescriptor("blob6", "short.bin", content.Length + 10),
                progress: null,
                CancellationToken.None));

        Assert.False(File.Exists(Path.Combine(directory.Path, "short.bin")));
    }

    [Fact]
    public async Task ProgressReportsMonotonicBytes()
    {
        var content = CreateContent(100_000);
        using var directory = new TempDirectory();
        using var http = CreateClient(new RangeHttpHandler(content));
        var downloader = new BlobDownloader(http, directory.Path, NullClientLog.Instance);
        var reports = new List<long>();

        await downloader.DownloadAsync(
            new BlobDescriptor("blob7", "large.bin", content.Length),
            new SynchronousProgress(value =>
            {
                lock (reports)
                {
                    reports.Add(value);
                }
            }),
            CancellationToken.None);

        lock (reports)
        {
            Assert.NotEmpty(reports);
            Assert.Equal(content.Length, reports[^1]);
            Assert.True(reports.SequenceEqual(reports.OrderBy(value => value)));
        }
    }

    private static HttpClient CreateClient(HttpMessageHandler handler) =>
        new(handler)
        {
            BaseAddress = new Uri("https://blob.test/"),
        };

    private static byte[] CreateContent(int size)
    {
        var content = new byte[size];
        for (var index = 0; index < size; index++)
        {
            content[index] = (byte)(index % 251);
        }
        return content;
    }

    private sealed class SynchronousProgress(Action<long> handler) : IProgress<long>
    {
        public void Report(long value) => handler(value);
    }

    private sealed class TempDirectory : IDisposable
    {
        public TempDirectory()
        {
            Path = System.IO.Path.Combine(System.IO.Path.GetTempPath(), $"molisync-test-{Guid.NewGuid():N}");
            Directory.CreateDirectory(Path);
        }

        public string Path { get; }

        public void Dispose()
        {
            try
            {
                Directory.Delete(Path, recursive: true);
            }
            catch (IOException)
            {
                // Best-effort cleanup of the test directory.
            }
        }
    }

    private sealed class RangeHttpHandler : HttpMessageHandler
    {
        private readonly byte[] _content;

        public RangeHttpHandler(byte[] content)
        {
            _content = content;
        }

        public List<RangeHeaderValue?> Ranges { get; } = new();

        public bool IgnoreRange { get; set; }

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            _ = cancellationToken;
            Ranges.Add(request.Headers.Range);

            var requestedFrom = request.Headers.Range?.Ranges.FirstOrDefault()?.From;
            if (!IgnoreRange && requestedFrom is not null && requestedFrom >= _content.Length && _content.Length > 0)
            {
                return Task.FromResult(new HttpResponseMessage(HttpStatusCode.RequestedRangeNotSatisfiable)
                {
                    RequestMessage = request,
                });
            }

            var from = IgnoreRange ? 0 : (int)(requestedFrom ?? 0);
            from = Math.Min(from, _content.Length);
            var slice = _content.AsSpan(from).ToArray();
            var partial = !IgnoreRange && from > 0;

            var response = new HttpResponseMessage(
                partial ? HttpStatusCode.PartialContent : HttpStatusCode.OK)
            {
                RequestMessage = request,
                Content = new ByteArrayContent(slice),
            };
            if (partial)
            {
                response.Content.Headers.ContentRange = new ContentRangeHeaderValue(
                    from,
                    _content.Length - 1,
                    _content.Length);
            }
            return Task.FromResult(response);
        }
    }
}
