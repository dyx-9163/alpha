namespace AifarBundlePackager.Core;

public sealed record ServiceDefinition(
    string Service,
    string Module,
    IReadOnlyList<string> TargetParts,
    bool IsWeb = false);

public sealed record BundleRequest(
    string JavaSourceRoot,
    string WebDistRoot,
    string OutputPath,
    IReadOnlyCollection<string> Services);

public sealed record BundleResult(
    string OutputPath,
    long Size,
    IReadOnlyList<string> Services);

public enum BundleStage
{
    Validating,
    Discovering,
    Copying,
    Hashing,
    WritingManifest,
    WritingBundle,
    Cleaning,
    Completed,
}

public sealed record BundleProgress(
    BundleStage Stage,
    string Message,
    int Completed,
    int Total);
