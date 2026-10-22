using MoliSync.Client.Http;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Sync;

/// <summary>
/// User initiated download flow: a placeholder is turned into a local file via
/// the resumable Range downloader, and its store state is kept in sync.
/// </summary>
public sealed class SyncCoordinator
{
    private readonly SyncItemStore _store;
    private readonly BlobDownloader _downloader;
    private readonly IClientLog _log;

    public SyncCoordinator(SyncItemStore store, BlobDownloader downloader, IClientLog log)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _downloader = downloader ?? throw new ArgumentNullException(nameof(downloader));
        _log = log ?? throw new ArgumentNullException(nameof(log));
    }

    public async Task<SyncItem> DownloadAsync(
        string recordId,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        var item = _store.Get(recordId)
            ?? throw new KeyNotFoundException($"unknown sync record {recordId}");

        if (item.File is null)
        {
            throw new InvalidOperationException($"record {recordId} has no file metadata");
        }
        if (item.State == SyncItemState.Downloaded && item.LocalPath is not null)
        {
            return item;
        }

        _store.UpdateState(recordId, SyncItemState.Downloading, error: null);
        try
        {
            var descriptor = new BlobDescriptor(item.File.BlobId, item.File.FileName, item.File.FileSize);
            var downloaded = await _downloader.DownloadAsync(descriptor, progress, cancellationToken)
                .ConfigureAwait(false);
            return _store.UpdateState(recordId, SyncItemState.Downloaded, downloaded.FilePath, error: null)
                ?? throw new InvalidOperationException($"record {recordId} disappeared during download");
        }
        catch (OperationCanceledException)
        {
            _store.UpdateState(recordId, SyncItemState.Placeholder, error: "download canceled");
            throw;
        }
        catch (Exception exception)
        {
            _log.Warn($"download of record {recordId} failed", exception);
            _store.UpdateState(recordId, SyncItemState.Failed, error: exception.Message);
            throw;
        }
    }
}
