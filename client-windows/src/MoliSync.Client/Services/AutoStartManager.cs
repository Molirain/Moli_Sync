using Microsoft.Win32;
using Windows.ApplicationModel;

namespace MoliSync.Client.Services;

public sealed record AutoStartResult(bool Enabled, string? Error)
{
    public static AutoStartResult Ok(bool enabled) => new(enabled, null);

    public static AutoStartResult Fail(string error) => new(false, error);
}

/// <summary>
/// Manages the per-user startup entry. Unpackaged builds use HKCU Run; MSIX
/// builds use the package startup task declared in AppxManifest.xml.
/// </summary>
public sealed class AutoStartManager
{
    private const string RunKeyPath = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string ValueName = "MoliSync";
    private const string StartupTaskId = "MoliSyncStartup";

    public bool IsEnabled
    {
        get
        {
            if (AppPaths.IsPackaged)
            {
                return IsPackagedTaskEnabled();
            }

            try
            {
                using var key = Registry.CurrentUser.OpenSubKey(RunKeyPath);
                return key?.GetValue(ValueName) is string value && value.Length > 0;
            }
            catch (Exception exception) when (exception is UnauthorizedAccessException or IOException)
            {
                return false;
            }
        }
    }

    public async Task<AutoStartResult> SetEnabledAsync(bool enabled, bool startMinimized)
    {
        if (AppPaths.IsPackaged)
        {
            return await SetPackagedTaskAsync(enabled).ConfigureAwait(true);
        }

        try
        {
            using var key = Registry.CurrentUser.CreateSubKey(RunKeyPath, writable: true);
            if (key is null)
            {
                return AutoStartResult.Fail("无法打开当前用户的开机启动注册表项");
            }

            if (!enabled)
            {
                key.DeleteValue(ValueName, throwOnMissingValue: false);
                return AutoStartResult.Ok(false);
            }

            var executable = Environment.ProcessPath;
            if (string.IsNullOrWhiteSpace(executable))
            {
                return AutoStartResult.Fail("无法确定 MoliSync 可执行文件路径");
            }

            var command = $"\"{executable}\"";
            if (startMinimized)
            {
                command += " --background";
            }
            key.SetValue(ValueName, command, RegistryValueKind.String);
            return AutoStartResult.Ok(true);
        }
        catch (Exception exception) when (exception is UnauthorizedAccessException or IOException)
        {
            return AutoStartResult.Fail($"更新开机启动失败：{exception.Message}");
        }
    }

    private static bool IsPackagedTaskEnabled()
    {
        try
        {
            var task = StartupTask.GetAsync(StartupTaskId).AsTask().GetAwaiter().GetResult();
            return task.State is StartupTaskState.Enabled or StartupTaskState.EnabledByPolicy;
        }
        catch (Exception)
        {
            return false;
        }
    }

    private static async Task<AutoStartResult> SetPackagedTaskAsync(bool enabled)
    {
        try
        {
            var task = await StartupTask.GetAsync(StartupTaskId);
            if (!enabled)
            {
                task.Disable();
                return AutoStartResult.Ok(false);
            }

            var state = await task.RequestEnableAsync();
            return state switch
            {
                StartupTaskState.Enabled or StartupTaskState.EnabledByPolicy => AutoStartResult.Ok(true),
                StartupTaskState.DisabledByUser => AutoStartResult.Fail(
                    "开机启动已被用户在 Windows 设置中禁用，请在“设置 → 应用 → 启动”中重新启用"),
                StartupTaskState.DisabledByPolicy => AutoStartResult.Fail(
                    "开机启动已被组策略禁用"),
                _ => AutoStartResult.Ok(false),
            };
        }
        catch (Exception exception)
        {
            return AutoStartResult.Fail($"更新开机启动失败：{exception.Message}");
        }
    }
}
