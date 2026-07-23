namespace AifarBundlePackager.Core;

public sealed record ServiceDefinition(
    string Service,
    string Module,
    IReadOnlyList<string> TargetParts,
    bool IsWeb = false);
