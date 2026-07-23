using System.Text.RegularExpressions;

namespace AifarBundlePackager.Core;

public static partial class JarLocator
{
    public static string FindRunnableJar(string javaSourceRoot, ServiceDefinition definition)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(javaSourceRoot);
        ArgumentNullException.ThrowIfNull(definition);
        if (definition.IsWeb)
        {
            throw new ArgumentException(
                $"Service '{definition.Service}' is not a Java service.",
                nameof(definition));
        }

        var targetDirectory = definition.TargetParts.Aggregate(
            Path.GetFullPath(javaSourceRoot),
            Path.Combine);
        if (!Directory.Exists(targetDirectory))
        {
            throw new DirectoryNotFoundException(
                $"Service '{definition.Service}' target directory does not exist: {targetDirectory}");
        }

        var expectedPrefix = $"{definition.Module}-";
        var candidates = Directory
            .EnumerateFiles(targetDirectory, "*.jar", SearchOption.TopDirectoryOnly)
            .Where(path => IsRunnableCandidate(Path.GetFileName(path), expectedPrefix))
            .OrderBy(path => Path.GetFileName(path), StringComparer.OrdinalIgnoreCase)
            .ToArray();

        if (candidates.Length == 0)
        {
            throw new FileNotFoundException(
                $"Service '{definition.Service}' has no runnable JAR matching " +
                $"'{definition.Module}-*.jar' in: {targetDirectory}");
        }

        if (candidates.Length > 1)
        {
            throw new InvalidDataException(
                $"Service '{definition.Service}' has multiple runnable JAR candidates in " +
                $"'{targetDirectory}': {string.Join(", ", candidates.Select(Path.GetFileName))}");
        }

        return Path.GetFullPath(candidates[0]);
    }

    private static bool IsRunnableCandidate(string fileName, string expectedPrefix) =>
        !fileName.StartsWith("original-", StringComparison.OrdinalIgnoreCase) &&
        fileName.StartsWith(expectedPrefix, StringComparison.OrdinalIgnoreCase) &&
        !ExcludedSuffix().IsMatch(fileName);

    [GeneratedRegex(@"(-sources|-javadoc|-tests?|-plain)\.jar$", RegexOptions.IgnoreCase)]
    private static partial Regex ExcludedSuffix();
}
