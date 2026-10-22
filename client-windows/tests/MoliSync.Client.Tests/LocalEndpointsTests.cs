using System.Net;
using MoliSync.Client.Net;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class LocalEndpointsTests
{
    [Theory]
    [InlineData("10.0.0.5", true)]
    [InlineData("172.16.4.2", true)]
    [InlineData("172.31.255.1", true)]
    [InlineData("192.168.1.20", true)]
    [InlineData("100.64.0.1", true)]
    [InlineData("100.127.255.254", true)]
    [InlineData("172.32.0.1", false)]
    [InlineData("100.128.0.1", false)]
    [InlineData("169.254.10.10", false)]
    [InlineData("8.8.8.8", false)]
    [InlineData("127.0.0.1", false)]
    public void IsPrivateAcceptsOnlyRoutablePrivateRanges(string address, bool expected)
    {
        Assert.Equal(expected, LocalEndpoints.IsPrivate(IPAddress.Parse(address)));
    }

    [Fact]
    public void CandidatesAreEmptyWhenTheListenerIsNotBound()
    {
        Assert.Empty(LocalEndpoints.Candidates(0));
        Assert.Empty(LocalEndpoints.Candidates(70000));
    }

    [Fact]
    public void CandidatesUseTheListenerPortAndOnlyPrivateAddresses()
    {
        var endpoints = LocalEndpoints.Candidates(47001);

        Assert.All(endpoints, endpoint =>
        {
            Assert.Equal(47001, endpoint.Port);
            Assert.True(LocalEndpoints.IsPrivate(IPAddress.Parse(endpoint.Ip)));
            Assert.Contains(endpoint.Network, new[] { "lan", "vpn" });
        });
        Assert.True(endpoints.Count <= 8);
    }
}
