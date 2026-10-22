using MoliSync.Client.Services;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class AppSettingsTests
{
    [Fact]
    public void ServerUrlAndDataDirectoryAreEnoughToBeConfigured()
    {
        var settings = new AppSettings
        {
            ServerUrl = "https://sync.example.com/",
            DataDirectory = Path.Combine(Path.GetTempPath(), "molisync-data"),
        };

        settings.Normalize();
        settings.Validate();

        Assert.True(settings.IsConfigured);
        Assert.Empty(settings.CaCertificatePath);
        Assert.Equal("https://sync.example.com", settings.ServerUrl);
    }

    [Fact]
    public void MissingOptionalCaFileIsRejectedOnlyWhenConfigured()
    {
        var settings = new AppSettings
        {
            ServerUrl = "https://sync.example.com",
            DataDirectory = Path.Combine(Path.GetTempPath(), "molisync-data"),
            CaCertificatePath = Path.Combine(Path.GetTempPath(), $"missing-{Guid.NewGuid():N}.crt"),
        };

        Assert.Throws<ConfigurationException>(settings.Validate);
    }
}
