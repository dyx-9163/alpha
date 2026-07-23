using AifarBundlePackager.Core;
using Xunit;

namespace AifarBundlePackager.Tests;

public sealed class JarLocatorTests
{
    [Fact]
    public void FindRunnableJar_ReturnsOnlyRunnableCandidateFromMappedTarget()
    {
        using var workspace = new TestWorkspace();
        var expected = workspace.CreateFile(
            "alpha-permission/alpha-permission-server/target/alpha-permission-2.4.1.jar");

        var actual = JarLocator.FindRunnableJar(workspace.Root, ServiceCatalog.Get("permission"));

        Assert.Equal(expected, actual);
    }

    [Fact]
    public void FindRunnableJar_RejectsMissingTargetWithServiceAndPath()
    {
        using var workspace = new TestWorkspace();
        var definition = ServiceCatalog.Get("gateway");
        var expectedPath = workspace.Combine("alpha-gateway", "target");

        var error = Assert.Throws<DirectoryNotFoundException>(
            () => JarLocator.FindRunnableJar(workspace.Root, definition));

        Assert.Contains("gateway", error.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains(expectedPath, error.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void FindRunnableJar_RejectsWhenOnlyExcludedArtifactsExist()
    {
        using var workspace = new TestWorkspace();
        const string target = "alpha-im/alpha-im-core/target";
        workspace.CreateFile($"{target}/original-alpha-im-1.0.jar");
        workspace.CreateFile($"{target}/alpha-im-sources.jar");
        workspace.CreateFile($"{target}/alpha-im-javadoc.jar");
        workspace.CreateFile($"{target}/alpha-im-test.jar");
        workspace.CreateFile($"{target}/alpha-im-tests.jar");
        workspace.CreateFile($"{target}/alpha-im-plain.jar");

        var error = Assert.Throws<FileNotFoundException>(
            () => JarLocator.FindRunnableJar(workspace.Root, ServiceCatalog.Get("im")));

        Assert.Contains("im", error.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("alpha-im-*.jar", error.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void FindRunnableJar_RejectsMultipleCandidatesAndListsTheirNames()
    {
        using var workspace = new TestWorkspace();
        const string target = "alpha-meeting/alpha-meeting-core/target";
        workspace.CreateFile($"{target}/alpha-meeting-1.0.jar");
        workspace.CreateFile($"{target}/alpha-meeting-2.0.jar");

        var error = Assert.Throws<InvalidDataException>(
            () => JarLocator.FindRunnableJar(workspace.Root, ServiceCatalog.Get("meeting")));

        Assert.Contains("meeting", error.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("alpha-meeting-1.0.jar", error.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("alpha-meeting-2.0.jar", error.Message, StringComparison.OrdinalIgnoreCase);
    }
}
