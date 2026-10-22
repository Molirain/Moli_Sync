using System.Runtime.InteropServices;
using System.Text;

namespace MoliSync.Client.Services;

internal static class AppPaths
{
    private const int ErrorInsufficientBuffer = 122;

    static AppPaths()
    {
        SettingsDirectory = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "MoliSync");
        SettingsFile = Path.Combine(SettingsDirectory, "settings.json");
        LogFile = Path.Combine(SettingsDirectory, "logs", "client.log");
        DefaultDataDirectory = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            "MoliSyncData");
    }

    public static string SettingsDirectory { get; }

    public static string SettingsFile { get; }

    public static string LogFile { get; }

    public static string DefaultDataDirectory { get; }

    public static bool IsPackaged => DetectPackageIdentity();

    public static string LegacyConfigFile => Path.Combine(AppContext.BaseDirectory, "client.json");

    private static bool DetectPackageIdentity()
    {
        try
        {
            var length = 0;
            var result = GetCurrentPackageFullName(ref length, null);
            return result == ErrorInsufficientBuffer;
        }
        catch (DllNotFoundException)
        {
            return false;
        }
        catch (EntryPointNotFoundException)
        {
            return false;
        }
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode)]
    private static extern int GetCurrentPackageFullName(ref int packageFullNameLength, StringBuilder? packageFullName);
}
