namespace MoliSync.Client.Services;

public sealed record MigrationProgress(
    long BytesCopied,
    long TotalBytes,
    int FilesCopied,
    int TotalFiles,
    string CurrentFile)
{
    public double Percent => TotalBytes == 0
        ? (TotalFiles == 0 ? 100 : FilesCopied * 100.0 / TotalFiles)
        : BytesCopied * 100.0 / TotalBytes;
}

public sealed record MigrationResult(
    string DataDirectory,
    long BytesCopied,
    int FilesCopied,
    bool OldDirectoryDeleted,
    string? CleanupWarning);

/// <summary>
/// Owns the local data directory used for downloaded blobs and resumable
/// partial files. Downloads take the read gate; migration takes the write gate,
/// so no file is moved while a download is writing to it.
/// </summary>
public sealed class DataDirectoryManager
{
    private const int CopyBufferSize = 256 * 1024;
    private readonly SemaphoreSlim _downloadGate = new(1, 1);
    private readonly object _currentGate = new();
    private string _current;

    public DataDirectoryManager(string current)
    {
        _current = Path.GetFullPath(current);
        Directory.CreateDirectory(_current);
    }

    public string Current
    {
        get
        {
            lock (_currentGate)
            {
                return _current;
            }
        }
    }

    public async Task<T> WithDownloadGateAsync<T>(
        Func<string, Task<T>> action,
        CancellationToken cancellationToken)
    {
        await _downloadGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            return await action(Current).ConfigureAwait(false);
        }
        finally
        {
            _downloadGate.Release();
        }
    }

    public static void ValidateTarget(string currentDirectory, string targetDirectory)
    {
        var current = Path.GetFullPath(currentDirectory);
        var target = Path.GetFullPath(targetDirectory);

        if (string.Equals(
                current.TrimEnd(Path.DirectorySeparatorChar),
                target.TrimEnd(Path.DirectorySeparatorChar),
                StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException("新目录不能与当前数据目录相同");
        }

        if (IsInside(target, current))
        {
            throw new InvalidOperationException("新目录不能位于当前数据目录内部");
        }
        if (IsInside(current, target))
        {
            throw new InvalidOperationException("当前数据目录不能位于新目录内部");
        }

        var root = Path.GetPathRoot(target);
        if (root is null || string.Equals(
                target.TrimEnd(Path.DirectorySeparatorChar),
                root.TrimEnd(Path.DirectorySeparatorChar),
                StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException("不能把磁盘根目录作为数据目录");
        }

        if (!Directory.Exists(target))
        {
            return;
        }

        if (Directory.EnumerateFileSystemEntries(target).Any())
        {
            throw new InvalidOperationException("目标文件夹必须为空");
        }
    }

    public async Task<MigrationResult> MigrateAsync(
        string targetDirectory,
        IProgress<MigrationProgress>? progress,
        CancellationToken cancellationToken)
    {
        var target = Path.GetFullPath(targetDirectory);
        ValidateTarget(_current, target);

        await _downloadGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            var source = _current;
            var files = Directory.Exists(source)
                ? Directory.EnumerateFiles(source, "*", SearchOption.AllDirectories)
                    .Select(path => new FileInfo(path))
                    .ToArray()
                : Array.Empty<FileInfo>();
            var totalBytes = files.Sum(file => file.Length);
            var copiedBytes = 0L;
            var copiedFiles = 0;

            try
            {
                if (!Directory.Exists(target))
                {
                    Directory.CreateDirectory(target);
                }

                var sourceRoot = Path.EndsInDirectorySeparator(source)
                    ? source
                    : source + Path.DirectorySeparatorChar;

                foreach (var file in files)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    var relative = Path.GetRelativePath(sourceRoot, file.FullName);
                    var destinationPath = Path.Combine(target, relative);
                    Directory.CreateDirectory(Path.GetDirectoryName(destinationPath)!);

                    progress?.Report(new MigrationProgress(
                        copiedBytes, totalBytes, copiedFiles, files.Length, relative));

                    await CopyFileAsync(file.FullName, destinationPath, cancellationToken)
                        .ConfigureAwait(false);
                    if (new FileInfo(destinationPath).Length != file.Length)
                    {
                        throw new IOException($"复制后的文件大小不一致：{relative}");
                    }

                    copiedBytes += file.Length;
                    copiedFiles++;
                    progress?.Report(new MigrationProgress(
                        copiedBytes, totalBytes, copiedFiles, files.Length, relative));
                }

                var oldDirectoryDeleted = true;
                string? cleanupWarning = null;
                if (Directory.Exists(source))
                {
                    try
                    {
                        Directory.Delete(source, recursive: true);
                    }
                    catch (Exception exception) when (
                        exception is IOException or UnauthorizedAccessException)
                    {
                        oldDirectoryDeleted = false;
                        cleanupWarning = $"迁移已完成，但旧目录未能删除：{exception.Message}";
                    }
                }

                _current = target;
                return new MigrationResult(target, copiedBytes, copiedFiles, oldDirectoryDeleted, cleanupWarning);
            }
            catch
            {
                TryDeleteDirectory(target);
                throw;
            }
        }
        finally
        {
            _downloadGate.Release();
        }
    }

    private static async Task CopyFileAsync(string source, string destination, CancellationToken cancellationToken)
    {
        await using var input = new FileStream(
            source,
            FileMode.Open,
            FileAccess.Read,
            FileShare.Read,
            CopyBufferSize,
            FileOptions.Asynchronous | FileOptions.SequentialScan);
        await using var output = new FileStream(
            destination,
            FileMode.CreateNew,
            FileAccess.Write,
            FileShare.None,
            CopyBufferSize,
            FileOptions.Asynchronous | FileOptions.SequentialScan);

        var buffer = new byte[CopyBufferSize];
        while (true)
        {
            var read = await input.ReadAsync(buffer, cancellationToken).ConfigureAwait(false);
            if (read == 0)
            {
                break;
            }
            await output.WriteAsync(buffer.AsMemory(0, read), cancellationToken).ConfigureAwait(false);
        }
        await output.FlushAsync(cancellationToken).ConfigureAwait(false);
    }

    private static void TryDeleteDirectory(string path)
    {
        try
        {
            if (Directory.Exists(path))
            {
                Directory.Delete(path, recursive: true);
            }
        }
        catch (IOException)
        {
            // Best effort rollback.
        }
        catch (UnauthorizedAccessException)
        {
            // Best effort rollback.
        }
    }

    private static bool IsInside(string candidate, string parent)
    {
        var normalizedParent = Path.EndsInDirectorySeparator(parent)
            ? parent
            : parent + Path.DirectorySeparatorChar;
        return candidate.StartsWith(normalizedParent, StringComparison.OrdinalIgnoreCase);
    }
}
