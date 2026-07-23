using System.IO.Compression;

namespace AifarBundlePackager.Core;

internal static class ZipUtilities
{
    private const int CleanupAttempts = 20;
    private static readonly TimeSpan CleanupDelay = TimeSpan.FromMilliseconds(500);

    public static void CreateFromDirectory(string sourceDirectory, string destinationPath)
    {
        var sourceFullPath = Path.GetFullPath(sourceDirectory);
        var files = Directory
            .EnumerateFiles(sourceFullPath, "*", SearchOption.AllDirectories)
            .Select(path => new
            {
                FullPath = path,
                EntryName = Path.GetRelativePath(sourceFullPath, path).Replace('\\', '/'),
            })
            .OrderBy(item => item.EntryName, StringComparer.Ordinal)
            .ToArray();

        using var destination = new FileStream(
            destinationPath,
            FileMode.CreateNew,
            FileAccess.ReadWrite,
            FileShare.None);
        using var archive = new ZipArchive(destination, ZipArchiveMode.Create, leaveOpen: false);
        foreach (var file in files)
        {
            var entry = archive.CreateEntry(file.EntryName, CompressionLevel.Optimal);
            using var input = File.OpenRead(file.FullPath);
            using var output = entry.Open();
            input.CopyTo(output);
        }
    }

    public static void DeleteDirectoryWithRetry(string? path)
    {
        if (string.IsNullOrWhiteSpace(path) || !Directory.Exists(path))
        {
            return;
        }

        RetryDelete(path, () => Directory.Delete(path, recursive: true));
    }

    public static void DeleteFileWithRetry(string? path)
    {
        if (string.IsNullOrWhiteSpace(path) || !File.Exists(path))
        {
            return;
        }

        RetryDelete(path, () => File.Delete(path));
    }

    private static void RetryDelete(string path, Action delete)
    {
        Exception? lastError = null;
        for (var attempt = 1; attempt <= CleanupAttempts; attempt++)
        {
            try
            {
                delete();
                return;
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException)
            {
                lastError = error;
                if (attempt < CleanupAttempts)
                {
                    Thread.Sleep(CleanupDelay);
                }
            }
        }

        throw new IOException(
            $"Unable to remove temporary path after {CleanupAttempts} attempts: {path}",
            lastError);
    }
}
