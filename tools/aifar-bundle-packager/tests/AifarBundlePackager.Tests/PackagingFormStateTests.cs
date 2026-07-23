using AifarBundlePackager.Core;
using Xunit;

namespace AifarBundlePackager.Tests;

public sealed class PackagingFormStateTests
{
    [Fact]
    public void Constructor_StartsWithEmptyPathsAndAllServicesSelected()
    {
        var state = CreateState();

        Assert.Equal(string.Empty, state.JavaSourceRoot);
        Assert.Equal(string.Empty, state.WebDistRoot);
        Assert.Equal(string.Empty, state.OutputPath);
        Assert.Equal(ServiceCatalog.All.Select(item => item.Service), state.SelectedServices);
        Assert.False(state.CanPackage);
    }

    [Fact]
    public void CanPackage_WebOnlyRequiresWebAndOutputButNotJava()
    {
        var state = CreateState();

        state.ClearServices();
        state.SetServiceSelected("web-vue3", selected: true);
        state.TrySetWebDistRoot(@"D:\web\dist");
        state.TrySetOutputPath(@"D:\output\bundle.zip");

        Assert.False(state.RequiresJavaSource);
        Assert.True(state.RequiresWebDist);
        Assert.True(state.CanPackage);
    }

    [Fact]
    public void CanPackage_JavaOnlyRequiresJavaAndOutputButNotWeb()
    {
        var state = CreateState();

        state.ClearServices();
        state.SetServiceSelected("gateway", selected: true);
        state.TrySetJavaSourceRoot(@"D:\java");
        state.TrySetOutputPath(@"D:\output\bundle.zip");

        Assert.True(state.RequiresJavaSource);
        Assert.False(state.RequiresWebDist);
        Assert.True(state.CanPackage);
    }

    [Fact]
    public void CanPackage_MixedSelectionRequiresBothSources()
    {
        var state = CreateState();

        state.ClearServices();
        state.SetServiceSelected("gateway", selected: true);
        state.SetServiceSelected("web-vue3", selected: true);
        state.TrySetJavaSourceRoot(@"D:\java");
        state.TrySetOutputPath(@"D:\output\bundle.zip");

        Assert.True(state.RequiresJavaSource);
        Assert.True(state.RequiresWebDist);
        Assert.False(state.CanPackage);
        state.TrySetWebDistRoot(@"D:\web\dist");
        Assert.True(state.CanPackage);
    }

    [Fact]
    public void ChangingServiceCategory_PreservesUnusedPaths()
    {
        var state = CreateReadyState();

        state.ClearServices();
        state.SetServiceSelected("web-vue3", selected: true);

        Assert.Equal(@"D:\java", state.JavaSourceRoot);
        Assert.Equal(@"D:\web\dist", state.WebDistRoot);
    }

    [Fact]
    public void CancelledDialogSelection_PreservesExistingPath()
    {
        var state = CreateState();
        state.TrySetJavaSourceRoot(@"D:\selected-java");

        var changed = state.TrySetJavaSourceRoot(null);

        Assert.False(changed);
        Assert.Equal(@"D:\selected-java", state.JavaSourceRoot);
    }

    [Fact]
    public void CanPackage_RequiresAtLeastOneServiceAndIdleState()
    {
        var state = CreateReadyState();

        state.ClearServices();
        Assert.False(state.CanPackage);

        state.SetServiceSelected("gateway", selected: true);
        Assert.True(state.CanPackage);

        state.IsBusy = true;
        Assert.False(state.CanPackage);
    }

    [Fact]
    public void SelectedServices_AlwaysFollowCatalogOrder()
    {
        var state = CreateReadyState();
        state.ClearServices();

        state.SetServiceSelected("web-vue3", selected: true);
        state.SetServiceSelected("gateway", selected: true);
        state.SetServiceSelected("im", selected: true);

        Assert.Equal(["im", "gateway", "web-vue3"], state.SelectedServices);
    }

    private static PackagingFormState CreateState() =>
        new(ServiceCatalog.All.Select(item => item.Service));

    private static PackagingFormState CreateReadyState()
    {
        var state = CreateState();
        state.TrySetJavaSourceRoot(@"D:\java");
        state.TrySetWebDistRoot(@"D:\web\dist");
        state.TrySetOutputPath(@"D:\output\bundle.zip");
        return state;
    }
}
