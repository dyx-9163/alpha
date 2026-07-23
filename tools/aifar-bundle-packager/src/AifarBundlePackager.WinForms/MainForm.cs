using System.Diagnostics;
using AifarBundlePackager.Core;

namespace AifarBundlePackager.WinForms;

public sealed partial class MainForm : Form
{
    private readonly PackagingFormState _state = new(
        ServiceCatalog.All.Select(item => item.Service));
    private string? _lastOutputDirectory;

    public MainForm()
    {
        InitializeLayout();
        WireEvents();
        RefreshState();
    }

    private void WireEvents()
    {
        _selectJavaButton.Click += (_, _) => SelectJavaRoot();
        _selectWebButton.Click += (_, _) => SelectWebRoot();
        _selectOutputButton.Click += (_, _) => SelectOutputPath();
        _selectAllButton.Click += (_, _) => SetAllServices(selected: true);
        _clearAllButton.Click += (_, _) => SetAllServices(selected: false);
        _startButton.Click += StartButton_Click;
        _openOutputButton.Click += (_, _) => OpenOutputDirectory();
        FormClosing += MainForm_FormClosing;

        foreach (var pair in _serviceCheckBoxes)
        {
            var service = pair.Key;
            pair.Value.CheckedChanged += (_, _) =>
            {
                _state.SetServiceSelected(service, pair.Value.Checked);
                RefreshState();
            };
        }
    }

    private void SelectJavaRoot()
    {
        using var dialog = new FolderBrowserDialog
        {
            Description = "选择 Alpha Java Cloud 源码根目录",
            UseDescriptionForTitle = true,
            ShowNewFolderButton = false,
        };
        if (dialog.ShowDialog(this) == DialogResult.OK &&
            _state.TrySetJavaSourceRoot(dialog.SelectedPath))
        {
            RefreshState();
        }
    }

    private void SelectWebRoot()
    {
        using var dialog = new FolderBrowserDialog
        {
            Description = "选择 alpha-web-vue3 的 dist 目录",
            UseDescriptionForTitle = true,
            ShowNewFolderButton = false,
        };
        if (dialog.ShowDialog(this) == DialogResult.OK &&
            _state.TrySetWebDistRoot(dialog.SelectedPath))
        {
            RefreshState();
        }
    }

    private void SelectOutputPath()
    {
        using var dialog = new SaveFileDialog
        {
            Title = "选择批量更新包输出位置",
            Filter = "ZIP 压缩包 (*.zip)|*.zip",
            DefaultExt = "zip",
            AddExtension = true,
            CheckPathExists = true,
            OverwritePrompt = true,
            FileName = string.Empty,
        };
        if (dialog.ShowDialog(this) == DialogResult.OK &&
            _state.TrySetOutputPath(dialog.FileName))
        {
            RefreshState();
        }
    }

    private void SetAllServices(bool selected)
    {
        if (selected)
        {
            _state.SelectAllServices();
        }
        else
        {
            _state.ClearServices();
        }

        foreach (var checkBox in _serviceCheckBoxes.Values)
        {
            checkBox.Checked = selected;
        }

        RefreshState();
    }

    private async void StartButton_Click(object? sender, EventArgs eventArgs)
    {
        _ = sender;
        _ = eventArgs;
        if (!_state.CanPackage)
        {
            return;
        }

        _state.IsBusy = true;
        _progressBar.Minimum = 0;
        _progressBar.Maximum = Math.Max(1, _state.SelectedServices.Count);
        _progressBar.Value = 0;
        _statusLabel.Text = "正在准备打包...";
        AppendLog("开始创建 AIFAR 批量更新包");
        RefreshState();

        var request = new BundleRequest(
            _state.JavaSourceRoot,
            _state.WebDistRoot,
            _state.OutputPath,
            _state.SelectedServices);
        var progress = new Progress<BundleProgress>(UpdateProgress);

        try
        {
            var result = await Task.Run(() => BundlePackager.Package(request, progress));
            _lastOutputDirectory = Path.GetDirectoryName(result.OutputPath);
            _statusLabel.Text =
                $"打包成功：{result.OutputPath}（{FormatBytes(result.Size)}）";
            AppendLog($"打包成功，服务：{string.Join(", ", result.Services)}");
            AppendLog($"输出文件：{result.OutputPath}");
            MessageBox.Show(
                this,
                $"批量更新包已生成。\n\n文件：{result.OutputPath}\n大小：{FormatBytes(result.Size)}\n服务：{string.Join(", ", result.Services)}",
                "打包成功",
                MessageBoxButtons.OK,
                MessageBoxIcon.Information);
        }
        catch (Exception error)
        {
            _statusLabel.Text = "打包失败，请查看日志。";
            AppendLog($"打包失败：{error.Message}");
            MessageBox.Show(
                this,
                error.Message,
                "打包失败",
                MessageBoxButtons.OK,
                MessageBoxIcon.Error);
        }
        finally
        {
            _state.IsBusy = false;
            RefreshState();
        }
    }

    private void UpdateProgress(BundleProgress progress)
    {
        _statusLabel.Text = progress.Message;
        _progressBar.Maximum = Math.Max(1, progress.Total);
        _progressBar.Value = Math.Clamp(progress.Completed, 0, _progressBar.Maximum);
        AppendLog(progress.Message);
    }

    private void OpenOutputDirectory()
    {
        if (string.IsNullOrWhiteSpace(_lastOutputDirectory) ||
            !Directory.Exists(_lastOutputDirectory))
        {
            return;
        }

        try
        {
            Process.Start(new ProcessStartInfo(_lastOutputDirectory)
            {
                UseShellExecute = true,
            });
        }
        catch (Exception error)
        {
            AppendLog($"打开输出目录失败：{error.Message}");
            MessageBox.Show(
                this,
                error.Message,
                "无法打开输出目录",
                MessageBoxButtons.OK,
                MessageBoxIcon.Error);
        }
    }

    private void MainForm_FormClosing(object? sender, FormClosingEventArgs eventArgs)
    {
        _ = sender;
        if (!_state.IsBusy)
        {
            return;
        }

        eventArgs.Cancel = true;
        MessageBox.Show(
            this,
            "打包正在进行，请等待任务结束后再关闭窗口。",
            "正在打包",
            MessageBoxButtons.OK,
            MessageBoxIcon.Information);
    }

    private void RefreshState()
    {
        _javaPathTextBox.Text = _state.JavaSourceRoot;
        _webPathTextBox.Text = _state.WebDistRoot;
        _outputPathTextBox.Text = _state.OutputPath;
        _startButton.Enabled = _state.CanPackage;
        _openOutputButton.Enabled =
            !_state.IsBusy &&
            !string.IsNullOrWhiteSpace(_lastOutputDirectory) &&
            Directory.Exists(_lastOutputDirectory);

        var mutableEnabled = !_state.IsBusy;
        _selectJavaButton.Enabled = mutableEnabled;
        _selectWebButton.Enabled = mutableEnabled;
        _selectOutputButton.Enabled = mutableEnabled;
        _selectAllButton.Enabled = mutableEnabled;
        _clearAllButton.Enabled = mutableEnabled;
        foreach (var checkBox in _serviceCheckBoxes.Values)
        {
            checkBox.Enabled = mutableEnabled;
        }

        if (_state.IsBusy)
        {
            _pathRequirementLabel.Text = "打包进行中，请勿关闭窗口。";
            _pathRequirementLabel.ForeColor = Color.FromArgb(22, 119, 255);
            return;
        }

        var missing = new List<string>();
        if (_state.RequiresJavaSource && string.IsNullOrWhiteSpace(_state.JavaSourceRoot))
        {
            missing.Add("Java 源码根目录");
        }
        if (_state.RequiresWebDist && string.IsNullOrWhiteSpace(_state.WebDistRoot))
        {
            missing.Add("Web dist 目录");
        }
        if (string.IsNullOrWhiteSpace(_state.OutputPath))
        {
            missing.Add("输出 ZIP");
        }
        if (_state.SelectedServices.Count == 0)
        {
            missing.Add("至少一个服务");
        }

        var categoryMessage = _state.RequiresJavaSource && !_state.RequiresWebDist
            ? "本次仅打包 Java 服务，Web dist 路径不使用。"
            : !_state.RequiresJavaSource && _state.RequiresWebDist
                ? "本次仅打包 web-vue3，Java 源码路径不使用。"
                : string.Empty;
        _pathRequirementLabel.Text = missing.Count == 0
            ? string.IsNullOrEmpty(categoryMessage)
                ? "路径和服务已选择，可以开始打包。"
                : categoryMessage
            : $"请手动选择：{string.Join("、", missing)}" +
                (string.IsNullOrEmpty(categoryMessage) ? string.Empty : $"；{categoryMessage}");
        _pathRequirementLabel.ForeColor = missing.Count == 0
            ? Color.FromArgb(56, 158, 13)
            : Color.FromArgb(217, 119, 6);
    }

    private void AppendLog(string message)
    {
        _logTextBox.AppendText($"[{DateTime.Now:HH:mm:ss}] {message}{Environment.NewLine}");
        _logTextBox.SelectionStart = _logTextBox.TextLength;
        _logTextBox.ScrollToCaret();
    }

    private static string FormatBytes(long bytes)
    {
        string[] units = ["B", "KB", "MB", "GB", "TB"];
        var value = (double)bytes;
        var unitIndex = 0;
        while (value >= 1024 && unitIndex < units.Length - 1)
        {
            value /= 1024;
            unitIndex++;
        }

        return $"{value:0.##} {units[unitIndex]}";
    }
}
