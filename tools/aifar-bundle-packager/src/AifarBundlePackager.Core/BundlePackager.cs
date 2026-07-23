using System.Runtime.ExceptionServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace AifarBundlePackager.Core;

public static class BundlePackager
{
    private static readonly JsonSerializerOptions ManifestJsonOptions = new()
    {
        WriteIndented = true,
    };

    public static BundleResult Package(
        BundleRequest request,
        IProgress<BundleProgress>? progress = null)
    {
        ArgumentNullException.ThrowIfNull(request);
        progress?.Report(new(BundleStage.Validating, "正在校验路径和服务选择", 0, 0));
        var context = Validate(request);

        string? stagingDirectory = null;
        string? temporaryArchive = null;
        BundleResult? result = null;
        Exception? primaryError = null;
        var cleanupErrors = new List<Exception>();

        try
        {
            Directory.CreateDirectory(context.OutputDirectory);
            var workId = Guid.NewGuid().ToString("N");
            stagingDirectory = Path.Combine(
                context.OutputDirectory,
                $".aifar-artifact-bundle-{workId}");
            temporaryArchive = Path.Combine(
                context.OutputDirectory,
                $".aifar-artifact-bundle-{workId}.zip");
            Directory.CreateDirectory(stagingDirectory);

            var manifestServices = new List<ManifestService>(context.Services.Count);
            for (var index = 0; index < context.Services.Count; index++)
            {
                var definition = context.Services[index];
                var sequence = index + 1;
                progress?.Report(new(
                    BundleStage.Discovering,
                    $"正在处理服务 {definition.Service}",
                    index,
                    context.Services.Count));
                var artifactDirectory = Path.Combine(stagingDirectory, "artifacts", definition.Service);
                Directory.CreateDirectory(artifactDirectory);

                string fileName;
                string artifactLocalPath;
                if (definition.IsWeb)
                {
                    fileName = "web-vue3.zip";
                    artifactLocalPath = Path.Combine(artifactDirectory, fileName);
                    progress?.Report(new(
                        BundleStage.Copying,
                        $"正在压缩服务 {definition.Service}: {context.WebDistRoot}",
                        index,
                        context.Services.Count));
                    ZipUtilities.CreateFromDirectory(context.WebDistRoot, artifactLocalPath);
                }
                else
                {
                    var sourceJar = JarLocator.FindRunnableJar(context.JavaSourceRoot, definition);
                    fileName = $"{definition.Module}.jar";
                    artifactLocalPath = Path.Combine(artifactDirectory, fileName);
                    progress?.Report(new(
                        BundleStage.Copying,
                        $"正在复制服务 {definition.Service}: {sourceJar}",
                        index,
                        context.Services.Count));
                    File.Copy(sourceJar, artifactLocalPath, overwrite: false);
                }

                progress?.Report(new(
                    BundleStage.Hashing,
                    $"正在计算服务 {definition.Service} 的 SHA256",
                    index,
                    context.Services.Count));
                var file = new FileInfo(artifactLocalPath);
                var artifactPath = $"artifacts/{definition.Service}/{fileName}";
                manifestServices.Add(new(
                    definition.Service,
                    definition.Module,
                    artifactPath,
                    fileName,
                    ComputeSha256(artifactLocalPath),
                    file.Length));
                progress?.Report(new(
                    BundleStage.Hashing,
                    $"服务 {definition.Service} 已准备完成",
                    sequence,
                    context.Services.Count));
            }

            progress?.Report(new(
                BundleStage.WritingManifest,
                "正在生成 manifest.json",
                context.Services.Count,
                context.Services.Count));
            var manifest = new BundleManifest(
                "aifar-artifact-bundle-v1",
                "aifar",
                "aifar-service-artifacts",
                manifestServices);
            var manifestJson = JsonSerializer.Serialize(manifest, ManifestJsonOptions);
            File.WriteAllText(
                Path.Combine(stagingDirectory, "manifest.json"),
                manifestJson,
                new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));

            progress?.Report(new(
                BundleStage.WritingBundle,
                $"正在生成最终 ZIP: {context.OutputPath}",
                context.Services.Count,
                context.Services.Count));
            ZipUtilities.CreateFromDirectory(stagingDirectory, temporaryArchive);

            progress?.Report(new(
                BundleStage.Cleaning,
                "正在清理暂存目录",
                context.Services.Count,
                context.Services.Count));
            ZipUtilities.DeleteDirectoryWithRetry(stagingDirectory);
            stagingDirectory = null;

            File.Move(temporaryArchive, context.OutputPath, overwrite: true);
            temporaryArchive = null;
            var output = new FileInfo(context.OutputPath);
            result = new(
                context.OutputPath,
                output.Length,
                context.Services.Select(item => item.Service).ToArray());
            progress?.Report(new(
                BundleStage.Completed,
                $"打包完成: {context.OutputPath}",
                context.Services.Count,
                context.Services.Count));
        }
        catch (Exception error)
        {
            primaryError = error;
        }
        finally
        {
            TryCleanup(
                () => ZipUtilities.DeleteDirectoryWithRetry(stagingDirectory),
                cleanupErrors);
            TryCleanup(
                () => ZipUtilities.DeleteFileWithRetry(temporaryArchive),
                cleanupErrors);
        }

        ThrowIfFailed(primaryError, cleanupErrors);
        return result!;
    }

    private static PackagingContext Validate(BundleRequest request)
    {
        RequireSelectedPath(request.JavaSourceRoot, "Java source root", nameof(request.JavaSourceRoot));
        RequireSelectedPath(request.WebDistRoot, "Web dist root", nameof(request.WebDistRoot));
        RequireSelectedPath(request.OutputPath, "Output path", nameof(request.OutputPath));

        var javaSourceRoot = Path.GetFullPath(request.JavaSourceRoot);
        var webDistRoot = Path.GetFullPath(request.WebDistRoot);
        var outputPath = Path.GetFullPath(request.OutputPath);
        var services = ServiceCatalog.Select(request.Services);

        if (!Directory.Exists(javaSourceRoot))
        {
            throw new DirectoryNotFoundException($"Java source root does not exist: {javaSourceRoot}");
        }

        if (!Directory.Exists(webDistRoot))
        {
            throw new DirectoryNotFoundException($"Web dist root does not exist: {webDistRoot}");
        }

        if (!string.Equals(Path.GetExtension(outputPath), ".zip", StringComparison.OrdinalIgnoreCase))
        {
            throw new ArgumentException($"Output path must end with .zip: {outputPath}", nameof(request.OutputPath));
        }

        if (Directory.Exists(outputPath))
        {
            throw new ArgumentException($"Output path points to a directory: {outputPath}", nameof(request.OutputPath));
        }

        if (services.Any(item => item.IsWeb))
        {
            var indexPath = Path.Combine(webDistRoot, "index.html");
            if (!File.Exists(indexPath))
            {
                throw new FileNotFoundException(
                    $"Service 'web-vue3' requires index.html in: {webDistRoot}",
                    indexPath);
            }
        }

        return new(
            javaSourceRoot,
            webDistRoot,
            outputPath,
            Path.GetDirectoryName(outputPath)!,
            services);
    }

    private static void RequireSelectedPath(string value, string label, string parameterName)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            throw new ArgumentException($"{label} must be selected.", parameterName);
        }
    }

    private static string ComputeSha256(string path)
    {
        using var stream = File.OpenRead(path);
        return Convert.ToHexString(SHA256.HashData(stream)).ToLowerInvariant();
    }

    private static void TryCleanup(Action cleanup, ICollection<Exception> errors)
    {
        try
        {
            cleanup();
        }
        catch (Exception error)
        {
            errors.Add(error);
        }
    }

    private static void ThrowIfFailed(Exception? primaryError, IReadOnlyCollection<Exception> cleanupErrors)
    {
        if (primaryError is not null && cleanupErrors.Count > 0)
        {
            throw new AggregateException(
                "Packaging failed and temporary files could not be completely cleaned.",
                new[] { primaryError }.Concat(cleanupErrors));
        }

        if (primaryError is not null)
        {
            ExceptionDispatchInfo.Capture(primaryError).Throw();
        }

        if (cleanupErrors.Count > 0)
        {
            throw new AggregateException("Temporary files could not be completely cleaned.", cleanupErrors);
        }
    }

    private sealed record PackagingContext(
        string JavaSourceRoot,
        string WebDistRoot,
        string OutputPath,
        string OutputDirectory,
        IReadOnlyList<ServiceDefinition> Services);

    private sealed record BundleManifest(
        [property: JsonPropertyName("schema")] string Schema,
        [property: JsonPropertyName("app")] string App,
        [property: JsonPropertyName("kind")] string Kind,
        [property: JsonPropertyName("services")] IReadOnlyList<ManifestService> Services);

    private sealed record ManifestService(
        [property: JsonPropertyName("service")] string Service,
        [property: JsonPropertyName("module")] string Module,
        [property: JsonPropertyName("artifact")] string Artifact,
        [property: JsonPropertyName("fileName")] string FileName,
        [property: JsonPropertyName("sha256")] string Sha256,
        [property: JsonPropertyName("size")] long Size);
}
