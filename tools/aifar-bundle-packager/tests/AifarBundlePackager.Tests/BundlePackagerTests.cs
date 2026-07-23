using System.IO.Compression;
using System.Security.Cryptography;
using System.Text.Json;
using AifarBundlePackager.Core;
using Xunit;

namespace AifarBundlePackager.Tests;

public sealed class BundlePackagerTests
{
    [Fact]
    public void Package_RequiresAllThreePathsEvenForPartialSelection()
    {
        using var workspace = new TestWorkspace();
        var output = workspace.Combine("output", "bundle.zip");

        var missingJava = Assert.Throws<ArgumentException>(() => BundlePackager.Package(
            new BundleRequest("", workspace.Root, output, ["web-vue3"])));
        var missingWeb = Assert.Throws<ArgumentException>(() => BundlePackager.Package(
            new BundleRequest(workspace.Root, "", output, ["gateway"])));
        var missingOutput = Assert.Throws<ArgumentException>(() => BundlePackager.Package(
            new BundleRequest(workspace.Root, workspace.Root, "", ["gateway"])));

        Assert.Contains("Java", missingJava.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("Web", missingWeb.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("output", missingOutput.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void Package_RejectsInvalidDirectoriesOutputAndServiceSelection()
    {
        using var workspace = new TestWorkspace();
        var javaRoot = workspace.CreateDirectory("java");
        var webRoot = workspace.CreateDirectory("web");
        var missing = workspace.Combine("missing");

        Assert.Throws<DirectoryNotFoundException>(() => BundlePackager.Package(
            new BundleRequest(missing, webRoot, workspace.Combine("bundle.zip"), ["gateway"])));
        Assert.Throws<DirectoryNotFoundException>(() => BundlePackager.Package(
            new BundleRequest(javaRoot, missing, workspace.Combine("bundle.zip"), ["gateway"])));
        Assert.Throws<ArgumentException>(() => BundlePackager.Package(
            new BundleRequest(javaRoot, webRoot, workspace.Combine("bundle.tar"), ["gateway"])));
        Assert.Throws<ArgumentException>(() => BundlePackager.Package(
            new BundleRequest(javaRoot, webRoot, workspace.Combine("bundle.zip"), [])));
    }

    [Fact]
    public void Package_RequiresWebIndexWhenWebIsSelected()
    {
        using var workspace = new TestWorkspace();
        var javaRoot = workspace.CreateDirectory("java");
        var webRoot = workspace.CreateDirectory("web");

        var error = Assert.Throws<FileNotFoundException>(() => BundlePackager.Package(
            new BundleRequest(javaRoot, webRoot, workspace.Combine("bundle.zip"), ["web-vue3"])));

        Assert.Contains("web-vue3", error.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("index.html", error.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains(webRoot, error.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void Package_WritesProtocolCompatiblePartialBundle()
    {
        using var workspace = new TestWorkspace();
        var javaRoot = workspace.CreateDirectory("java");
        workspace.CreateBytes(
            "java/alpha-im/alpha-im-core/target/alpha-im-1.0.jar",
            [1, 2, 3, 4]);
        workspace.CreateBytes(
            "java/alpha-meeting/alpha-meeting-core/target/alpha-meeting-1.0.jar",
            [5, 6, 7]);
        workspace.CreateBytes(
            "java/alpha-gateway/target/alpha-gateway-1.0.jar",
            [8, 9]);
        var webRoot = workspace.CreateDirectory("web");
        workspace.CreateFile("web/index.html", "<html>ok</html>");
        workspace.CreateFile("web/assets/app.js", "console.log('ok')");
        var output = workspace.Combine("output", "partial.zip");

        var result = BundlePackager.Package(new BundleRequest(
            javaRoot,
            webRoot,
            output,
            ["web-vue3", "gateway", "meeting", "im"]));

        Assert.Equal(Path.GetFullPath(output), result.OutputPath);
        Assert.Equal(new FileInfo(output).Length, result.Size);
        Assert.Equal(["im", "meeting", "gateway", "web-vue3"], result.Services);

        using var archive = ZipFile.OpenRead(output);
        Assert.All(archive.Entries, entry => Assert.DoesNotContain('\\', entry.FullName));
        Assert.NotNull(archive.GetEntry("manifest.json"));
        Assert.NotNull(archive.GetEntry("artifacts/im/alpha-im.jar"));
        Assert.NotNull(archive.GetEntry("artifacts/meeting/alpha-meeting.jar"));
        Assert.NotNull(archive.GetEntry("artifacts/gateway/alpha-gateway.jar"));
        var webArtifact = archive.GetEntry("artifacts/web-vue3/web-vue3.zip");
        Assert.NotNull(webArtifact);

        using var manifestDocument = ReadJson(archive.GetEntry("manifest.json")!);
        var root = manifestDocument.RootElement;
        Assert.Equal("aifar-artifact-bundle-v1", root.GetProperty("schema").GetString());
        Assert.Equal("aifar", root.GetProperty("app").GetString());
        Assert.Equal("aifar-service-artifacts", root.GetProperty("kind").GetString());
        var services = root.GetProperty("services").EnumerateArray().ToArray();
        Assert.Equal(
            ["im", "meeting", "gateway", "web-vue3"],
            services.Select(service => service.GetProperty("service").GetString()));

        foreach (var service in services)
        {
            var artifactPath = service.GetProperty("artifact").GetString()!;
            Assert.Equal(artifactPath.Replace('\\', '/'), artifactPath);
            var entry = archive.GetEntry(artifactPath);
            Assert.NotNull(entry);
            var bytes = ReadBytes(entry!);
            Assert.Equal(bytes.LongLength, service.GetProperty("size").GetInt64());
            Assert.Equal(
                Convert.ToHexString(SHA256.HashData(bytes)).ToLowerInvariant(),
                service.GetProperty("sha256").GetString());
            Assert.Equal(Path.GetFileName(artifactPath), service.GetProperty("fileName").GetString());
        }

        using var webBytes = new MemoryStream(ReadBytes(webArtifact!));
        using var webArchive = new ZipArchive(webBytes, ZipArchiveMode.Read);
        Assert.NotNull(webArchive.GetEntry("index.html"));
        Assert.NotNull(webArchive.GetEntry("assets/app.js"));
        Assert.DoesNotContain(webArchive.Entries, entry => entry.FullName.StartsWith("dist/", StringComparison.Ordinal));
    }

    [Fact]
    public void Package_FailureDoesNotReplaceExistingOutputAndCleansTemporaryPaths()
    {
        using var workspace = new TestWorkspace();
        var javaRoot = workspace.CreateDirectory("java");
        var webRoot = workspace.CreateDirectory("web");
        workspace.CreateFile("web/index.html");
        var outputDirectory = workspace.CreateDirectory("output");
        var output = workspace.CreateBytes("output/bundle.zip", [11, 22, 33]);

        Assert.ThrowsAny<IOException>(() => BundlePackager.Package(
            new BundleRequest(javaRoot, webRoot, output, ["gateway"])));

        Assert.Equal([11, 22, 33], File.ReadAllBytes(output));
        Assert.Empty(Directory.EnumerateFileSystemEntries(outputDirectory, ".aifar-artifact-bundle-*"));
    }

    [Fact]
    public void Package_SuccessReplacesExistingOutputAndCleansTemporaryPaths()
    {
        using var workspace = new TestWorkspace();
        var javaRoot = workspace.CreateDirectory("java");
        workspace.CreateBytes("java/alpha-gateway/target/alpha-gateway-1.0.jar", [1, 3, 5]);
        var webRoot = workspace.CreateDirectory("web");
        workspace.CreateFile("web/index.html");
        var outputDirectory = workspace.CreateDirectory("output");
        var output = workspace.CreateBytes("output/bundle.zip", [11, 22, 33]);

        BundlePackager.Package(new BundleRequest(javaRoot, webRoot, output, ["gateway"]));

        Assert.NotEqual([11, 22, 33], File.ReadAllBytes(output));
        using var archive = ZipFile.OpenRead(output);
        Assert.NotNull(archive.GetEntry("manifest.json"));
        Assert.Empty(Directory.EnumerateFileSystemEntries(outputDirectory, ".aifar-artifact-bundle-*"));
    }

    private static JsonDocument ReadJson(ZipArchiveEntry entry)
    {
        using var stream = entry.Open();
        return JsonDocument.Parse(stream);
    }

    private static byte[] ReadBytes(ZipArchiveEntry entry)
    {
        using var stream = entry.Open();
        using var buffer = new MemoryStream();
        stream.CopyTo(buffer);
        return buffer.ToArray();
    }
}
