using AifarBundlePackager.Core;
using Xunit;

namespace AifarBundlePackager.Tests;

public sealed class ServiceCatalogTests
{
    [Fact]
    public void All_UsesProtocolServiceOrder()
    {
        Assert.Equal(
            ["oauth", "permission", "system", "file", "message", "im", "contacts", "meeting", "gateway", "web-vue3"],
            ServiceCatalog.All.Select(item => item.Service));
    }

    [Theory]
    [InlineData("oauth", "alpha-oauth", "alpha-oauth/alpha-oauth-server/target")]
    [InlineData("permission", "alpha-permission", "alpha-permission/alpha-permission-server/target")]
    [InlineData("system", "alpha-system", "alpha-system/alpha-system-server/target")]
    [InlineData("file", "alpha-file", "alpha-file/alpha-file-server/target")]
    [InlineData("message", "alpha-message", "alpha-message/alpha-message-server/target")]
    [InlineData("im", "alpha-im", "alpha-im/alpha-im-core/target")]
    [InlineData("contacts", "alpha-contacts", "alpha-contacts/alpha-contacts-core/target")]
    [InlineData("meeting", "alpha-meeting", "alpha-meeting/alpha-meeting-core/target")]
    [InlineData("gateway", "alpha-gateway", "alpha-gateway/target")]
    public void Get_UsesExactJavaTargetMapping(string service, string module, string expectedParts)
    {
        var definition = ServiceCatalog.Get(service);

        Assert.Equal(module, definition.Module);
        Assert.Equal(expectedParts.Split('/'), definition.TargetParts);
        Assert.False(definition.IsWeb);
    }

    [Fact]
    public void Get_MarksWebServiceWithoutJavaTargetParts()
    {
        var definition = ServiceCatalog.Get("web-vue3");

        Assert.True(definition.IsWeb);
        Assert.Empty(definition.TargetParts);
    }

    [Fact]
    public void Select_DeduplicatesAndRestoresCatalogOrder()
    {
        var selected = ServiceCatalog.Select(["meeting", "gateway", "meeting", "im"]);

        Assert.Equal(["im", "meeting", "gateway"], selected.Select(item => item.Service));
    }

    [Fact]
    public void Select_RejectsEmptySelection()
    {
        var error = Assert.Throws<ArgumentException>(() => ServiceCatalog.Select([]));

        Assert.Contains("service", error.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void Select_RejectsUnknownService()
    {
        var error = Assert.Throws<ArgumentException>(() => ServiceCatalog.Select(["unknown"]));

        Assert.Contains("unknown", error.Message, StringComparison.OrdinalIgnoreCase);
    }
}
