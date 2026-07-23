using AifarBundlePackager.Core;

namespace AifarBundlePackager.WinForms;

public sealed partial class MainForm
{
    private readonly TextBox _javaPathTextBox = CreatePathTextBox();
    private readonly TextBox _webPathTextBox = CreatePathTextBox();
    private readonly TextBox _outputPathTextBox = CreatePathTextBox();
    private readonly Button _selectJavaButton = CreateBrowseButton();
    private readonly Button _selectWebButton = CreateBrowseButton();
    private readonly Button _selectOutputButton = CreateBrowseButton();
    private readonly Button _selectAllButton = new() { Text = "全选", AutoSize = true };
    private readonly Button _clearAllButton = new() { Text = "清空", AutoSize = true };
    private readonly Button _startButton = new()
    {
        Text = "开始打包",
        AutoSize = true,
        Padding = new(18, 6, 18, 6),
    };
    private readonly Button _openOutputButton = new()
    {
        Text = "打开输出目录",
        AutoSize = true,
        Padding = new(10, 6, 10, 6),
    };
    private readonly Label _pathRequirementLabel = new()
    {
        AutoSize = true,
        Padding = new(0, 2, 0, 8),
    };
    private readonly Label _statusLabel = new()
    {
        AutoSize = true,
        Text = "等待选择路径",
        Padding = new(0, 4, 0, 4),
    };
    private readonly ProgressBar _progressBar = new()
    {
        Dock = DockStyle.Fill,
        Height = 18,
        Style = ProgressBarStyle.Continuous,
    };
    private readonly RichTextBox _logTextBox = new()
    {
        Dock = DockStyle.Fill,
        ReadOnly = true,
        BackColor = Color.FromArgb(248, 250, 252),
        BorderStyle = BorderStyle.FixedSingle,
        Font = new Font("Consolas", 9F),
        DetectUrls = false,
    };
    private readonly Dictionary<string, CheckBox> _serviceCheckBoxes =
        new(StringComparer.OrdinalIgnoreCase);

    private void InitializeLayout()
    {
        SuspendLayout();
        Text = "AIFAR Bundle Packager";
        StartPosition = FormStartPosition.CenterScreen;
        MinimumSize = new(900, 650);
        ClientSize = new(980, 720);
        AutoScaleMode = AutoScaleMode.Dpi;
        Font = new Font("Microsoft YaHei UI", 9F);

        var root = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            Padding = new(22),
            ColumnCount = 1,
            RowCount = 8,
        };
        root.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        for (var index = 0; index < 7; index++)
        {
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }
        root.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        var title = new Label
        {
            Text = "AIFAR 批量更新包制作工具",
            AutoSize = true,
            Font = new Font("Microsoft YaHei UI", 17F, FontStyle.Bold),
            ForeColor = Color.FromArgb(31, 41, 55),
            Padding = new(0, 0, 0, 6),
        };
        var description = new Label
        {
            Text = "从已构建的 Java JAR 和 Web dist 生成带完整 manifest.json 的更新 ZIP。三个路径必须手动选择。",
            AutoSize = true,
            ForeColor = Color.FromArgb(75, 85, 99),
            Padding = new(0, 0, 0, 12),
        };

        root.Controls.Add(title, 0, 0);
        root.Controls.Add(description, 0, 1);
        root.Controls.Add(CreatePathsGroup(), 0, 2);
        root.Controls.Add(_pathRequirementLabel, 0, 3);
        root.Controls.Add(CreateServicesGroup(), 0, 4);
        root.Controls.Add(CreateActionPanel(), 0, 5);
        root.Controls.Add(CreateProgressPanel(), 0, 6);
        root.Controls.Add(_logTextBox, 0, 7);
        Controls.Add(root);
        ResumeLayout(performLayout: true);
    }

    private Control CreatePathsGroup()
    {
        var group = new GroupBox
        {
            Text = "路径",
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            Padding = new(12),
        };
        var table = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            ColumnCount = 3,
            RowCount = 3,
        };
        table.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 145F));
        table.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        table.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 100F));
        AddPathRow(table, 0, "Java 源码根目录", _javaPathTextBox, _selectJavaButton);
        AddPathRow(table, 1, "Web dist 目录", _webPathTextBox, _selectWebButton);
        AddPathRow(table, 2, "输出 ZIP", _outputPathTextBox, _selectOutputButton);
        group.Controls.Add(table);
        return group;
    }

    private Control CreateServicesGroup()
    {
        var group = new GroupBox
        {
            Text = "打包服务",
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            Padding = new(12),
        };
        var container = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            ColumnCount = 1,
            RowCount = 2,
        };
        var services = new FlowLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            WrapContents = true,
            Padding = new(0, 2, 0, 5),
        };
        foreach (var definition in ServiceCatalog.All)
        {
            var checkBox = new CheckBox
            {
                Text = definition.Service,
                Checked = true,
                AutoSize = true,
                Margin = new(3, 3, 18, 6),
            };
            _serviceCheckBoxes.Add(definition.Service, checkBox);
            services.Controls.Add(checkBox);
        }

        var commands = new FlowLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            FlowDirection = FlowDirection.LeftToRight,
        };
        commands.Controls.Add(_selectAllButton);
        commands.Controls.Add(_clearAllButton);
        container.Controls.Add(services, 0, 0);
        container.Controls.Add(commands, 0, 1);
        group.Controls.Add(container);
        return group;
    }

    private Control CreateActionPanel()
    {
        var panel = new FlowLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            FlowDirection = FlowDirection.RightToLeft,
            Padding = new(0, 10, 0, 8),
        };
        _startButton.BackColor = Color.FromArgb(22, 119, 255);
        _startButton.ForeColor = Color.White;
        _startButton.FlatStyle = FlatStyle.Flat;
        _startButton.FlatAppearance.BorderSize = 0;
        panel.Controls.Add(_startButton);
        panel.Controls.Add(_openOutputButton);
        return panel;
    }

    private Control CreateProgressPanel()
    {
        var panel = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            ColumnCount = 1,
            RowCount = 3,
            Padding = new(0, 0, 0, 10),
        };
        panel.Controls.Add(_progressBar, 0, 0);
        panel.Controls.Add(_statusLabel, 0, 1);
        panel.Controls.Add(new Label
        {
            Text = "执行日志",
            AutoSize = true,
            Font = new Font("Microsoft YaHei UI", 9F, FontStyle.Bold),
            Padding = new(0, 4, 0, 2),
        }, 0, 2);
        return panel;
    }

    private static void AddPathRow(
        TableLayoutPanel table,
        int row,
        string label,
        TextBox textBox,
        Button button)
    {
        table.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        table.Controls.Add(new Label
        {
            Text = label,
            AutoSize = true,
            Anchor = AnchorStyles.Left,
            Margin = new(3, 9, 3, 9),
        }, 0, row);
        textBox.Margin = new(3, 6, 8, 6);
        table.Controls.Add(textBox, 1, row);
        button.Margin = new(3, 5, 3, 5);
        table.Controls.Add(button, 2, row);
    }

    private static TextBox CreatePathTextBox() => new()
    {
        Dock = DockStyle.Fill,
        ReadOnly = true,
        BackColor = Color.White,
    };

    private static Button CreateBrowseButton() => new()
    {
        Text = "选择...",
        Dock = DockStyle.Fill,
        AutoSize = true,
    };
}
