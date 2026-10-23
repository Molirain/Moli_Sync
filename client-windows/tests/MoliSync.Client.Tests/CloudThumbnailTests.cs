using System.Net;
using System.Text;
using MoliSync.Client.Logging;
using MoliSync.Client.Services;
using MoliSync.Client.Sync;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class CloudThumbnailTests
{
    [Fact]
    public async Task DownloadsServerRenderedPreview()
    {
        using var directory = new TempDirectory();
        var handler = new ThumbnailHttpHandler
        {
            Preview = Encoding.UTF8.GetBytes("jpeg-preview"),
        };
        using var http = new HttpClient(handler);
        var repository = new CloudRepository(
            http,
            "https://sync.example.com",
            new DataDirectoryManager(directory.Path),
            NullClientLog.Instance);

        var path = await repository.EnsureThumbnailAsync(ImageRecord(), CancellationToken.None);

        Assert.Equal(Path.Combine(directory.Path, "cache", "record-1", "preview.jpg"), path);
        Assert.Equal("jpeg-preview", await File.ReadAllTextAsync(path));
        Assert.Equal("/api/v1/blobs/record-1/thumb", handler.RequestPaths.Single());
        Assert.Contains("w=480", handler.RequestQueries.Single());
    }

    [Fact]
    public async Task ReusesCachedPreviewWithoutHttp()
    {
        using var directory = new TempDirectory();
        var handler = new ThumbnailHttpHandler { Preview = Encoding.UTF8.GetBytes("first") };
        using var http = new HttpClient(handler);
        var repository = new CloudRepository(
            http,
            "https://sync.example.com",
            new DataDirectoryManager(directory.Path),
            NullClientLog.Instance);

        var first = await repository.EnsureThumbnailAsync(ImageRecord(), CancellationToken.None);
        var second = await repository.EnsureThumbnailAsync(ImageRecord(), CancellationToken.None);

        Assert.Equal(first, second);
        Assert.Single(handler.RequestPaths);
    }

    private static CloudRecord ImageRecord() => new()
    {
        RecordId = "record-1",
        BlobId = "blob-1",
        FileName = "photo.jpg",
        FileSize = 4096,
        Type = "image",
        MimeType = "image/jpeg",
    };

    private sealed class ThumbnailHttpHandler : HttpMessageHandler
    {
        public byte[] Preview { get; set; } = Array.Empty<byte>();

        public List<string> RequestPaths { get; } = new();

        public List<string> RequestQueries { get; } = new();

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            RequestPaths.Add(request.RequestUri!.AbsolutePath);
            RequestQueries.Add(request.RequestUri!.Query);
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(Preview),
            };
            response.Content.Headers.ContentType =
                new System.Net.Http.Headers.MediaTypeHeaderValue("image/jpeg");
            return Task.FromResult(response);
        }
    }

    private sealed class TempDirectory : IDisposable
    {
        public TempDirectory()
        {
            Path = System.IO.Path.Combine(
                System.IO.Path.GetTempPath(),
                $"molisync-thumb-{Guid.NewGuid():N}");
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
                // Best effort cleanup.
            }
        }
    }
}

public sealed class DevicePinFingerprintTests
{
    [Fact]
    public void UsesServerFingerprintWhenPresent()
    {
        var device = new DeviceView
        {
            DeviceId = "cn:study-pc",
            Fingerprint = "AB".PadRight(64, '0'),
        };

        Assert.Equal("ab" + new string('0', 62), device.PinFingerprint);
    }

    [Fact]
    public void FallsBackToFingerprintShapedDeviceId()
    {
        var fingerprint = new string('c', 64);
        var device = new DeviceView { DeviceId = fingerprint };

        Assert.Equal(fingerprint, device.PinFingerprint);
    }

    [Fact]
    public void RejectsLogicalDeviceIds()
    {
        var device = new DeviceView { DeviceId = "cn:study-pc" };

        Assert.Equal(string.Empty, device.PinFingerprint);
        Assert.False(DeviceView.IsFingerprint("cn:study-pc"));
        Assert.True(DeviceView.IsFingerprint(new string('d', 64)));
    }
}
