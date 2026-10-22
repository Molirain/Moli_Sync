using Avalonia.Controls;

namespace MoliSync.Client.Views;

public static class UploadDialog
{
    public static async Task<UploadMode?> ChooseModeAsync(
        Window? owner,
        int fileCount,
        long totalBytes,
        bool cloudAllowed)
    {
        var window = new UploadChoiceWindow(fileCount, totalBytes, cloudAllowed);
        return owner is null ? null : await window.ShowDialog<UploadMode?>(owner);
    }

    public static async Task RunAsync(
        Window? owner,
        string title,
        Func<IProgress<UploadProgress>, CancellationToken, Task> action)
    {
        if (owner is null)
        {
            return;
        }

        var window = new UploadProgressWindow(title, action);
        await window.ShowDialog(owner);
        window.ThrowIfFailed();
    }
}
