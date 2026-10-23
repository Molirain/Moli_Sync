using System.Net;
using System.Net.Http;
using System.Text;
using MoliSync.Client.Logging;
using MoliSync.Client.Services;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class UpdateCheckerTests : IDisposable
{
    private readonly string _cachePath = Path.Combine(Path.GetTempPath(), $"molisync-update-{Guid.NewGuid():N}.json");

    public void Dispose()
    {
        if (File.Exists(_cachePath))
        {
            File.Delete(_cachePath);
        }
    }

    private sealed class StubHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, HttpResponseMessage> _respond;

        public StubHandler(Func<HttpRequestMessage, HttpResponseMessage> respond) => _respond = respond;

        public int Calls { get; private set; }

        public HttpRequestMessage? LastRequest { get; private set; }

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            Calls++;
            LastRequest = request;
            return Task.FromResult(_respond(request));
        }
    }

    private static HttpResponseMessage Json(string body, HttpStatusCode status = HttpStatusCode.OK) =>
        new(status) { Content = new StringContent(body, Encoding.UTF8, "application/json") };

    private UpdateChecker CreateChecker(StubHandler handler, string current = "0.5.0") =>
        new(new HttpClient(handler), NullClientLog.Instance, _cachePath, current);

    [Fact]
    public async Task PicksOnlyTheDesktopNamespaceAndSkipsDrafts()
    {
        const string payload = """
        [
          {"tag_name":"android-v0.9.0","html_url":"https://example/android"},
          {"tag_name":"server-v0.9.0","html_url":"https://example/server"},
          {"tag_name":"desktop-v0.5.1","html_url":"https://example/d-051"},
          {"tag_name":"desktop-v0.5.2","html_url":"https://example/d-052"},
          {"tag_name":"desktop-v0.9.9","html_url":"https://example/draft","draft":true},
          {"tag_name":"desktop-v0.8.0","html_url":"https://example/pre","prerelease":true},
          {"tag_name":"v9.9.9","html_url":"https://example/umbrella"}
        ]
        """;
        var handler = new StubHandler(_ => Json(payload));
        var checker = CreateChecker(handler);

        var release = await checker.CheckAsync(force: true, CancellationToken.None);

        Assert.NotNull(release);
        Assert.Equal("0.5.2", release!.Version);
        Assert.Equal("https://example/d-052", release.Url);
        Assert.True(checker.IsUpdateAvailable);
        Assert.Equal("MoliSync-Desktop/0.5.0", handler.LastRequest!.Headers.UserAgent.ToString());
    }

    [Fact]
    public async Task ReportsUpToDateWhenTheNewestTagMatches()
    {
        const string payload = """[{"tag_name":"desktop-v0.5.0","html_url":"https://example/d"}]""";
        var checker = CreateChecker(new StubHandler(_ => Json(payload)));

        var release = await checker.CheckAsync(force: true, CancellationToken.None);

        Assert.NotNull(release);
        Assert.False(checker.IsUpdateAvailable);
    }

    [Fact]
    public async Task CachesTheResultBetweenChecks()
    {
        const string payload = """[{"tag_name":"desktop-v0.5.1","html_url":"https://example/d"}]""";
        var handler = new StubHandler(_ => Json(payload));
        var checker = CreateChecker(handler);

        await checker.CheckAsync(force: true, CancellationToken.None);
        await checker.CheckAsync(force: false, CancellationToken.None);

        Assert.Equal(1, handler.Calls);

        // A fresh checker reuses the on-disk cache.
        var second = CreateChecker(handler);
        var cached = await second.CheckAsync(force: false, CancellationToken.None);
        Assert.Equal(1, handler.Calls);
        Assert.Equal("0.5.1", cached!.Version);
    }

    [Fact]
    public async Task ForceIgnoresTheCache()
    {
        const string payload = """[{"tag_name":"desktop-v0.5.1","html_url":"https://example/d"}]""";
        var handler = new StubHandler(_ => Json(payload));
        var checker = CreateChecker(handler);

        await checker.CheckAsync(force: false, CancellationToken.None);
        await checker.CheckAsync(force: true, CancellationToken.None);

        Assert.Equal(2, handler.Calls);
    }

    [Fact]
    public async Task NotFoundReportsThePrivateRepositoryHint()
    {
        var checker = CreateChecker(new StubHandler(_ => Json("{}", HttpStatusCode.NotFound)));

        var release = await checker.CheckAsync(force: true, CancellationToken.None);

        Assert.Null(release);
        Assert.Contains("公开", checker.LastError);
        Assert.False(checker.IsUpdateAvailable);
    }

    [Fact]
    public async Task ServerErrorsDoNotThrow()
    {
        var checker = CreateChecker(new StubHandler(_ => Json("{}", HttpStatusCode.InternalServerError)));

        var release = await checker.CheckAsync(force: true, CancellationToken.None);

        Assert.Null(release);
        Assert.Contains("500", checker.LastError);
    }

    [Fact]
    public async Task MalformedJsonDoesNotThrow()
    {
        var checker = CreateChecker(new StubHandler(_ => Json("not json")));

        var release = await checker.CheckAsync(force: true, CancellationToken.None);

        Assert.Null(release);
        Assert.NotNull(checker.LastError);
    }

    [Theory]
    [InlineData("0.5.0", "0.5.1", -1)]
    [InlineData("0.5.1", "0.5.0", 1)]
    [InlineData("v0.5.1", "0.5.1", 0)]
    [InlineData("0.10.0", "0.9.0", 1)]
    [InlineData("1.0.0", "0.99.99", 1)]
    public void CompareOrdersSemanticVersions(string left, string right, int expected)
    {
        Assert.Equal(expected, Math.Sign(UpdateChecker.Compare(left, right)));
    }

    [Theory]
    [InlineData("v0.5.1", "0.5.1")]
    [InlineData("0.5.1-rc1", "0.5.1")]
    [InlineData("0.5.1+build", "0.5.1")]
    [InlineData(" 0.5.1 ", "0.5.1")]
    public void NormalizeStripsPrefixesAndSuffixes(string input, string expected) =>
        Assert.Equal(expected, UpdateChecker.Normalize(input));

    [Fact]
    public void IsSemVerRequiresThreeComponents()
    {
        Assert.True(UpdateChecker.IsSemVer("0.5.1"));
        Assert.False(UpdateChecker.IsSemVer("0.5"));
        Assert.False(UpdateChecker.IsSemVer("0.5.1.2"));
        Assert.False(UpdateChecker.IsSemVer("dev"));
    }
}
