using MoliSync.Client.Logging;
using MoliSync.Client.Platform;
using MoliSync.Client.Sync;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class SyncDispatcherTests
{
    [Fact]
    public void TextRecordIsStoredAndCopiedToClipboard()
    {
        var (dispatcher, store, clipboard) = Create();

        dispatcher.Handle(Envelope(
            new SyncRecord
            {
                RecordId = "record-1",
                SenderDeviceId = "device-a",
                CreatedAt = 1_760_000_000_000,
                Type = PayloadTypes.Text,
                TextContent = "hello from the phone",
            }));

        Assert.Equal(new[] { "hello from the phone" }, clipboard.Texts);
        var item = Assert.Single(store.Snapshot());
        Assert.Equal(SyncItemState.Ready, item.State);
        Assert.Equal("hello from the phone", item.Text);
        Assert.Null(item.File);
    }

    [Fact]
    public void DuplicateTextRecordIsIgnored()
    {
        var (dispatcher, store, clipboard) = Create();
        var record = new SyncRecord
        {
            RecordId = "record-1",
            SenderDeviceId = "device-a",
            CreatedAt = 1,
            Type = PayloadTypes.Text,
            TextContent = "hello",
        };

        dispatcher.Handle(Envelope(record));
        dispatcher.Handle(Envelope(record));

        Assert.Single(clipboard.Texts);
        Assert.Single(store.Snapshot());
    }

    [Fact]
    public void FileRecordCreatesPlaceholderWithoutDownloadOrClipboard()
    {
        var (dispatcher, store, clipboard) = Create();
        var meta = new CloudFileMeta
        {
            BlobId = "blob-1",
            FileName = "report.pdf",
            FileSize = 4096,
            MimeType = "application/pdf",
        };

        dispatcher.Handle(Envelope(new SyncRecord
        {
            RecordId = "record-file",
            SenderDeviceId = "device-a",
            CreatedAt = 2,
            Type = PayloadTypes.File,
            FileMeta = meta,
        }));

        Assert.Empty(clipboard.Texts);
        var item = Assert.Single(store.Snapshot());
        Assert.Equal(SyncItemState.Placeholder, item.State);
        Assert.Same(meta, item.File);
        Assert.Null(item.LocalPath);
    }

    [Fact]
    public void ImageRecordCreatesPlaceholder()
    {
        var (dispatcher, store, clipboard) = Create();
        dispatcher.Handle(Envelope(new SyncRecord
        {
            RecordId = "record-image",
            SenderDeviceId = "device-b",
            CreatedAt = 3,
            Type = PayloadTypes.Image,
            FileMeta = new CloudFileMeta
            {
                BlobId = "blob-image",
                FileName = "photo.webp",
                FileSize = 128,
                MimeType = "image/webp",
                ThumbnailWebp = new byte[] { 1, 2, 3 },
            },
        }));

        Assert.Empty(clipboard.Texts);
        var item = Assert.Single(store.Snapshot());
        Assert.Equal(SyncItemState.Placeholder, item.State);
        Assert.Equal("photo.webp", item.File?.FileName);
    }

    [Fact]
    public void TextRecordWithoutContentIsRejected()
    {
        var (dispatcher, store, clipboard) = Create();

        dispatcher.Handle(Envelope(new SyncRecord
        {
            RecordId = "bad-text",
            SenderDeviceId = "device-a",
            CreatedAt = 4,
            Type = PayloadTypes.Text,
            TextContent = null,
        }));

        Assert.Empty(store.Snapshot());
        Assert.Empty(clipboard.Texts);
    }

    [Fact]
    public void FileRecordWithoutMetadataIsRejected()
    {
        var (dispatcher, store, _) = Create();

        dispatcher.Handle(Envelope(new SyncRecord
        {
            RecordId = "bad-file",
            SenderDeviceId = "device-a",
            CreatedAt = 5,
            Type = PayloadTypes.File,
        }));

        Assert.Empty(store.Snapshot());
    }

    [Fact]
    public void AutoCopyCanBeDisabled()
    {
        var (dispatcher, store, clipboard) = Create(autoCopyText: false);

        dispatcher.Handle(Envelope(new SyncRecord
        {
            RecordId = "record-1",
            SenderDeviceId = "device-a",
            CreatedAt = 6,
            Type = PayloadTypes.Text,
            TextContent = "no clipboard",
        }));

        Assert.Single(store.Snapshot());
        Assert.Empty(clipboard.Texts);
    }

    [Fact]
    public void WelcomeClampsHeartbeatValues()
    {
        var (dispatcher, _, _) = Create();

        dispatcher.Handle(new ServerEnvelope
        {
            Type = ProtocolTypes.Welcome,
            ProtocolVersion = 1,
            DeviceId = "device-a",
            HeartbeatIntervalMs = 1,
            PongTimeoutMs = long.MaxValue,
            Devices = new List<DeviceView>(),
        });

        var welcome = Assert.IsType<WelcomeState>(dispatcher.Welcome);
        Assert.Equal(TimeSpan.FromSeconds(5), welcome.HeartbeatInterval);
        Assert.Equal(TimeSpan.FromMinutes(10), welcome.PongTimeout);
    }

    [Fact]
    public void PresenceRaisesEvent()
    {
        var (dispatcher, _, _) = Create();
        PresenceUpdate? observed = null;
        dispatcher.PresenceChanged += update => observed = update;

        var device = new DeviceView { DeviceId = "device-c", Name = "PC C" };
        dispatcher.Handle(new ServerEnvelope
        {
            Type = ProtocolTypes.Presence,
            State = "online",
            Device = device,
        });

        Assert.NotNull(observed);
        Assert.Equal("online", observed.State);
        Assert.Same(device, observed.Device);
    }

    [Fact]
    public void ClipboardFailureDoesNotLoseTheRecord()
    {
        var clipboard = new FakeClipboard { Accept = false };
        var store = new SyncItemStore();
        var dispatcher = new SyncDispatcher(store, clipboard, NullClientLog.Instance);

        dispatcher.Handle(Envelope(new SyncRecord
        {
            RecordId = "record-1",
            SenderDeviceId = "device-a",
            CreatedAt = 7,
            Type = PayloadTypes.Text,
            TextContent = "clipboard busy",
        }));

        Assert.Single(store.Snapshot());
        Assert.Empty(clipboard.Texts);
    }

    private static ServerEnvelope Envelope(SyncRecord record) => new()
    {
        Type = ProtocolTypes.SyncRecord,
        Record = record,
    };

    private static (SyncDispatcher Dispatcher, SyncItemStore Store, FakeClipboard Clipboard) Create(
        bool autoCopyText = true)
    {
        var store = new SyncItemStore();
        var clipboard = new FakeClipboard();
        var dispatcher = new SyncDispatcher(store, clipboard, NullClientLog.Instance, autoCopyText);
        return (dispatcher, store, clipboard);
    }

    private sealed class FakeClipboard : IClipboardSink
    {
        public List<string> Texts { get; } = new();

        public bool Accept { get; init; } = true;

        public bool TrySetText(string text)
        {
            if (!Accept)
            {
                return false;
            }
            Texts.Add(text);
            return true;
        }
    }
}
