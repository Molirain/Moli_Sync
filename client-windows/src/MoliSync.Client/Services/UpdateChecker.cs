using System.Net;
using System.Text.Json;
using System.Text.Json.Serialization;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Services;

/// <summary>One release found in a component's tag namespace.</summary>
public sealed record UpdateRelease(
    string Component,
    string Version,
    string Tag,
    string Url,
    string? Notes,
    DateTimeOffset PublishedAt);

/// <summary>
/// Looks for a newer release in this component's own tag namespace
/// (<c>desktop-vX.Y.Z</c>). Android and server releases are ignored so a fix in
/// one client never looks like a desktop update.
/// </summary>
public sealed class UpdateChecker
{
    public const string Component = "desktop";
    public const string Repository = "Molirain/Moli_Sync";
    private static readonly TimeSpan CacheLifetime = TimeSpan.FromHours(12);
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(10);

    private readonly HttpClient _http;
    private readonly IClientLog _log;
    private readonly string _cachePath;
    private readonly string _apiBase;

    public UpdateChecker(HttpClient http, IClientLog log, string cachePath, string currentVersion, string apiBase = "https://api.github.com")
    {
        _http = http ?? throw new ArgumentNullException(nameof(http));
        _log = log ?? throw new ArgumentNullException(nameof(log));
        _cachePath = cachePath ?? throw new ArgumentNullException(nameof(cachePath));
        _apiBase = apiBase.TrimEnd('/');
        CurrentVersion = Normalize(currentVersion);
    }

    public string CurrentVersion { get; }

    public UpdateRelease? Latest { get; private set; }

    public string? LastError { get; private set; }

    public DateTimeOffset? CheckedAt { get; private set; }

    public bool IsUpdateAvailable => Latest is not null && Compare(Latest.Version, CurrentVersion) > 0;

    /// <summary>
    /// Returns the newest desktop release. The result is cached on disk for 12
    /// hours so repeated launches stay far below the GitHub anonymous limit.
    /// Network and API failures never throw.
    /// </summary>
    public async Task<UpdateRelease?> CheckAsync(bool force, CancellationToken cancellationToken)
    {
        if (!force && TryLoadCache(out var cached, out var cachedAt))
        {
            Latest = cached;
            CheckedAt = cachedAt;
            return cached;
        }

        try
        {
            using var request = new HttpRequestMessage(
                HttpMethod.Get,
                $"{_apiBase}/repos/{Repository}/releases?per_page=50");
            request.Headers.Accept.ParseAdd("application/vnd.github+json");
            request.Headers.Add("X-GitHub-Api-Version", "2022-11-28");
            request.Headers.UserAgent.ParseAdd($"MoliSync-Desktop/{CurrentVersion}");

            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(RequestTimeout);

            using var response = await _http
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, timeout.Token)
                .ConfigureAwait(false);

            if (response.StatusCode == HttpStatusCode.NotFound)
            {
                LastError = "仓库暂不可公开读取";
                return null;
            }
            if (!response.IsSuccessStatusCode)
            {
                LastError = $"GitHub 返回 HTTP {(int)response.StatusCode}";
                return null;
            }

            await using var stream = await response.Content.ReadAsStreamAsync(timeout.Token).ConfigureAwait(false);
            var releases = await JsonSerializer
                .DeserializeAsync(stream, UpdateJsonContext.Default.ListGitHubRelease, timeout.Token)
                .ConfigureAwait(false);

            var latest = PickLatest(releases ?? new List<GitHubRelease>());
            Latest = latest;
            CheckedAt = DateTimeOffset.UtcNow;
            LastError = null;
            SaveCache(latest);
            return latest;
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            LastError = "检查更新超时";
            return null;
        }
        catch (Exception exception) when (exception is HttpRequestException or JsonException or IOException)
        {
            LastError = exception.Message;
            _log.Debug($"update check failed: {exception.Message}");
            return null;
        }
    }

    private UpdateRelease? PickLatest(IReadOnlyList<GitHubRelease> releases)
    {
        var prefix = Component + "-v";
        UpdateRelease? best = null;
        foreach (var release in releases)
        {
            if (release.Draft || release.Prerelease || string.IsNullOrWhiteSpace(release.TagName))
            {
                continue;
            }
            if (!release.TagName.StartsWith(prefix, StringComparison.Ordinal))
            {
                continue;
            }
            var version = Normalize(release.TagName[prefix.Length..]);
            if (!IsSemVer(version))
            {
                continue;
            }
            if (best is null || Compare(version, best.Version) > 0)
            {
                best = new UpdateRelease(
                    Component,
                    version,
                    release.TagName,
                    release.HtmlUrl ?? string.Empty,
                    release.Body,
                    release.PublishedAt ?? default);
            }
        }
        return best;
    }

    private bool TryLoadCache(out UpdateRelease? release, out DateTimeOffset checkedAt)
    {
        release = null;
        checkedAt = default;
        try
        {
            if (!File.Exists(_cachePath))
            {
                return false;
            }
            var written = File.GetLastWriteTimeUtc(_cachePath);
            if (DateTimeOffset.UtcNow - written > CacheLifetime)
            {
                return false;
            }

            var json = File.ReadAllText(_cachePath);
            var cached = JsonSerializer.Deserialize(json, UpdateJsonContext.Default.UpdateCache);
            if (cached is null)
            {
                return false;
            }

            checkedAt = new DateTimeOffset(written, TimeSpan.Zero);
            if (string.IsNullOrEmpty(cached.Version))
            {
                // A cached "no release found" answer must not trigger another
                // request; otherwise every launch would spend an API call.
                return true;
            }
            if (!IsSemVer(cached.Version))
            {
                return false;
            }
            release = new UpdateRelease(
                Component,
                cached.Version,
                cached.Tag,
                cached.Url,
                cached.Notes,
                cached.PublishedAt);
            return true;
        }
        catch (Exception exception) when (exception is IOException or JsonException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    private void SaveCache(UpdateRelease? release)
    {
        try
        {
            var directory = Path.GetDirectoryName(_cachePath);
            if (!string.IsNullOrEmpty(directory))
            {
                Directory.CreateDirectory(directory);
            }
            var payload = release is null
                ? new UpdateCache(string.Empty, string.Empty, string.Empty, null, default)
                : new UpdateCache(release.Version, release.Tag, release.Url, release.Notes, release.PublishedAt);
            File.WriteAllText(_cachePath, JsonSerializer.Serialize(payload, UpdateJsonContext.Default.UpdateCache));
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            _log.Debug($"cannot cache the update check: {exception.Message}");
        }
    }

    /// <summary>Normalizes "v0.5.1", " 0.5.1 " and "0.5.1-rc1" to "0.5.1".</summary>
    internal static string Normalize(string value)
    {
        var trimmed = (value ?? string.Empty).Trim();
        if (trimmed.StartsWith('v') || trimmed.StartsWith('V'))
        {
            trimmed = trimmed[1..];
        }
        var cut = trimmed.IndexOfAny(new[] { '-', '+' });
        return cut >= 0 ? trimmed[..cut] : trimmed;
    }

    internal static bool IsSemVer(string value) =>
        System.Version.TryParse(value, out var parsed) &&
        parsed.Major >= 0 &&
        !value.Contains('-') &&
        value.Split('.').Length == 3;

    /// <summary>Compares two MAJOR.MINOR.PATCH strings.</summary>
    internal static int Compare(string left, string right)
    {
        return Parse(left).CompareTo(Parse(right));
    }

    private static Version Parse(string value) =>
        Version.TryParse(Normalize(value), out var parsed) ? parsed : new Version(0, 0, 0);

    public sealed record UpdateCache(
        string Version,
        string Tag,
        string Url,
        string? Notes,
        DateTimeOffset PublishedAt);

    public sealed record GitHubRelease(
        string? TagName,
        string? HtmlUrl,
        string? Body,
        bool Draft,
        bool Prerelease,
        DateTimeOffset? PublishedAt);
}

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower)]
[JsonSerializable(typeof(List<UpdateChecker.GitHubRelease>))]
[JsonSerializable(typeof(UpdateChecker.UpdateCache))]
internal sealed partial class UpdateJsonContext : JsonSerializerContext
{
}
