using MoliSync.Client.Logging;

namespace MoliSync.Client.Sync;

public enum SyncItemState
{
    Ready = 0,
    Placeholder = 1,
    Downloading = 2,
    Downloaded = 3,
    Failed = 4,
}

/// <summary>Immutable snapshot of a sync history entry.</summary>
public sealed record SyncItem(
    string RecordId,
    string SenderDeviceId,
    long CreatedAt,
    string PayloadType,
    string? Text,
    CloudFileMeta? File,
    SyncItemState State,
    string? LocalPath,
    string? Error);

/// <summary>
/// Thread-safe in-memory history. A real UI binds to ItemAdded/ItemUpdated and
/// renders file/image entries as placeholders until the user downloads them.
/// </summary>
public sealed class SyncItemStore
{
    private readonly object _gate = new();
    private readonly Dictionary<string, SyncItem> _items = new(StringComparer.Ordinal);
    private readonly IClientLog? _log;

    public SyncItemStore(IClientLog? log = null)
    {
        _log = log;
    }

    public event Action<SyncItem>? ItemAdded;

    public event Action<SyncItem>? ItemUpdated;

    public bool TryAdd(SyncItem item)
    {
        ArgumentNullException.ThrowIfNull(item);

        lock (_gate)
        {
            if (!_items.TryAdd(item.RecordId, item))
            {
                return false;
            }
        }

        try
        {
            ItemAdded?.Invoke(item);
        }
        catch (Exception exception)
        {
            _log?.Warn("sync item subscriber failed", exception);
        }
        return true;
    }

    public SyncItem? Get(string recordId)
    {
        lock (_gate)
        {
            return _items.TryGetValue(recordId, out var item) ? item : null;
        }
    }

    public IReadOnlyList<SyncItem> Snapshot()
    {
        lock (_gate)
        {
            return _items.Values
                .OrderByDescending(item => item.CreatedAt)
                .ThenBy(item => item.RecordId, StringComparer.Ordinal)
                .ToArray();
        }
    }

    public SyncItem? UpdateState(
        string recordId,
        SyncItemState state,
        string? localPath = null,
        string? error = null)
    {
        SyncItem updated;
        lock (_gate)
        {
            if (!_items.TryGetValue(recordId, out var item))
            {
                return null;
            }

            updated = item with
            {
                State = state,
                LocalPath = localPath ?? item.LocalPath,
                Error = error,
            };
            _items[recordId] = updated;
        }

        try
        {
            ItemUpdated?.Invoke(updated);
        }
        catch (Exception exception)
        {
            _log?.Warn("sync item subscriber failed", exception);
        }
        return updated;
    }
}
