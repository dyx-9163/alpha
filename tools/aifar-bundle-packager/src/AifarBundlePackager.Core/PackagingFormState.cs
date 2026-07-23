namespace AifarBundlePackager.Core;

public sealed class PackagingFormState
{
    private readonly IReadOnlyList<string> _serviceOrder;
    private readonly HashSet<string> _selectedServices;

    public PackagingFormState(IEnumerable<string> services)
    {
        ArgumentNullException.ThrowIfNull(services);
        _serviceOrder = services
            .Where(service => !string.IsNullOrWhiteSpace(service))
            .Select(service => service.Trim())
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
        if (_serviceOrder.Count == 0)
        {
            throw new ArgumentException("At least one available service is required.", nameof(services));
        }

        _selectedServices = new(_serviceOrder, StringComparer.OrdinalIgnoreCase);
    }

    public string JavaSourceRoot { get; private set; } = string.Empty;

    public string WebDistRoot { get; private set; } = string.Empty;

    public string OutputPath { get; private set; } = string.Empty;

    public bool IsBusy { get; set; }

    public IReadOnlyList<string> SelectedServices =>
        _serviceOrder.Where(_selectedServices.Contains).ToArray();

    public bool CanPackage =>
        !IsBusy &&
        !string.IsNullOrWhiteSpace(JavaSourceRoot) &&
        !string.IsNullOrWhiteSpace(WebDistRoot) &&
        !string.IsNullOrWhiteSpace(OutputPath) &&
        _selectedServices.Count > 0;

    public bool TrySetJavaSourceRoot(string? selectedPath) =>
        TrySetPath(selectedPath, value => JavaSourceRoot = value);

    public bool TrySetWebDistRoot(string? selectedPath) =>
        TrySetPath(selectedPath, value => WebDistRoot = value);

    public bool TrySetOutputPath(string? selectedPath) =>
        TrySetPath(selectedPath, value => OutputPath = value);

    public void SetServiceSelected(string service, bool selected)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(service);
        var canonicalService = _serviceOrder.FirstOrDefault(
            item => string.Equals(item, service, StringComparison.OrdinalIgnoreCase));
        if (canonicalService is null)
        {
            throw new ArgumentException($"Unsupported service '{service}'.", nameof(service));
        }

        if (selected)
        {
            _selectedServices.Add(canonicalService);
        }
        else
        {
            _selectedServices.Remove(canonicalService);
        }
    }

    public void SelectAllServices()
    {
        foreach (var service in _serviceOrder)
        {
            _selectedServices.Add(service);
        }
    }

    public void ClearServices() => _selectedServices.Clear();

    private static bool TrySetPath(string? selectedPath, Action<string> assign)
    {
        if (string.IsNullOrWhiteSpace(selectedPath))
        {
            return false;
        }

        assign(selectedPath.Trim());
        return true;
    }
}
