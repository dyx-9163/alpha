namespace AifarBundlePackager.Core;

public static class ServiceCatalog
{
    private static readonly IReadOnlyList<ServiceDefinition> Definitions =
    [
        Java("oauth", "alpha-oauth", "alpha-oauth", "alpha-oauth-server", "target"),
        Java("permission", "alpha-permission", "alpha-permission", "alpha-permission-server", "target"),
        Java("system", "alpha-system", "alpha-system", "alpha-system-server", "target"),
        Java("file", "alpha-file", "alpha-file", "alpha-file-server", "target"),
        Java("message", "alpha-message", "alpha-message", "alpha-message-server", "target"),
        Java("im", "alpha-im", "alpha-im", "alpha-im-core", "target"),
        Java("contacts", "alpha-contacts", "alpha-contacts", "alpha-contacts-core", "target"),
        Java("meeting", "alpha-meeting", "alpha-meeting", "alpha-meeting-core", "target"),
        Java("gateway", "alpha-gateway", "alpha-gateway", "target"),
        new("web-vue3", "web-vue3", Array.Empty<string>(), IsWeb: true),
    ];

    public static IReadOnlyList<ServiceDefinition> All => Definitions;

    public static ServiceDefinition Get(string service)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(service);

        return Definitions.FirstOrDefault(
                   item => string.Equals(item.Service, service.Trim(), StringComparison.OrdinalIgnoreCase))
               ?? throw new ArgumentException($"Unsupported service '{service}'.", nameof(service));
    }

    public static IReadOnlyList<ServiceDefinition> Select(IEnumerable<string> services)
    {
        ArgumentNullException.ThrowIfNull(services);

        var requested = new HashSet<string>(
            services.Where(service => !string.IsNullOrWhiteSpace(service)).Select(service => service.Trim()),
            StringComparer.OrdinalIgnoreCase);
        if (requested.Count == 0)
        {
            throw new ArgumentException("At least one service must be selected.", nameof(services));
        }

        foreach (var service in requested)
        {
            _ = Get(service);
        }

        return Definitions.Where(item => requested.Contains(item.Service)).ToArray();
    }

    private static ServiceDefinition Java(
        string service,
        string module,
        params string[] targetParts) => new(service, module, targetParts);
}
