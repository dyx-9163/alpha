# AIFAR Bundle Packager WinForms 设计

## 目标

将 `scripts/package-aifar-artifact-bundle.cmd` 与 `scripts/package-aifar-artifact-bundle.ps1` 的能力封装为一个 Windows x64 图形化工具 `AIFARBundlePackager.exe`。交付物为 .NET 8 自包含单文件 EXE，目标机器无需预装 PowerShell 或 .NET 运行库。

EXE 必须继续生成与当前后端协议兼容的 `aifar-artifact-bundle-v1` ZIP，不改变服务清单、JAR 发现规则、Web 包根结构、SHA256、size 或失败清理语义。

## 技术方案

- UI：C# WinForms，目标框架 `net8.0-windows`。
- 核心逻辑：独立 Core 项目，不引用 WinForms，负责路径校验、服务选择、JAR 发现、Web ZIP、manifest、最终 ZIP、原子替换和设置持久化。
- 测试：独立测试项目直接验证 Core；GUI 只保留薄事件层。
- 发布：`win-x64`、`SelfContained=true`、`PublishSingleFile=true`、`PublishTrimmed=false`，避免 WinForms 裁剪兼容问题。
- 现有 CMD/PowerShell 在迁移期保留作兼容和结果对照，但发布给使用者的运行文件只有 EXE，EXE 运行时不调用或读取这两个脚本。

目录结构：

```text
tools/aifar-bundle-packager/
├── AifarBundlePackager.sln
├── src/
│   ├── AifarBundlePackager.Core/
│   └── AifarBundlePackager.WinForms/
└── tests/
    └── AifarBundlePackager.Tests/
```

## 界面

主窗口包含：

1. Java 源码根目录：文本框和文件夹选择按钮。
2. Web `dist` 目录：文本框和文件夹选择按钮。
3. 输出 ZIP：文本框和保存文件对话框按钮，过滤器只允许 `.zip`。
4. 服务复选框：`oauth`、`permission`、`system`、`file`、`message`、`im`、`contacts`、`meeting`、`gateway`、`web-vue3`。
5. “全选”和“清空”操作；首次启动默认全选。
6. “开始打包”主按钮、进度条、当前步骤文本和只读滚动日志。
7. 成功后显示输出文件、包大小和已打包服务，并提供“打开输出目录”。

打包期间路径、服务复选框和开始按钮禁用，防止重复提交；窗口仍保持响应。打包结束或失败后恢复操作。

## 设置持久化

三个路径保存在：

```text
%LocalAppData%\AIFAR\BundlePackager\settings.json
```

首次启动使用当前脚本中的默认值：

- Java：`D:\workspace\alpha\backend\alpha-java-cloud`
- Web：`D:\workspace\alpha\fronted\alpha-web-vue3\dist`
- 输出：当前工作目录下 `aifar-batch-update.zip`

成功读取设置后使用上次值。设置缺失、损坏或字段无效时回退默认值，不阻止程序启动。用户开始打包时保存当前三个路径；服务选择不持久化，每次启动默认全选。

## 打包数据流

1. 标准化三个路径并确认至少选择一个服务。
2. 输出路径必须以 `.zip` 结尾，不能指向目录。
3. 对每个所选 Java 服务，在固定模块 `target` 目录查找唯一可运行 JAR：名称匹配 `alpha-<service>-*.jar`，排除 `original-*`、sources、javadoc、test/tests 和 plain 包；零个或多个候选都失败。
4. `web-vue3` 要求 `dist/index.html` 存在，并把 `dist` 内部内容压成 `artifacts/web-vue3/web-vue3.zip`，不保留外层 `dist/`。
5. 所有制品复制到 staging 的 `artifacts/<service>/`，Java 文件规范化为 `alpha-<service>.jar`。
6. 对最终制品计算真实 SHA256 和字节大小，生成无 BOM 的 UTF-8 `manifest.json`。
7. ZIP entry 一律使用 `/`，最终结构为根 `manifest.json` 加 `artifacts/`。
8. staging 和临时 ZIP 位于输出目录，以 GUID 命名。只有全部成功后才原子替换目标 ZIP；任何失败不得覆盖已有目标文件。
9. `finally` 清理 staging 和临时 ZIP；对 Windows 瞬时文件锁执行有上限的重试，清理失败也作为失败结果显示。

manifest 固定字段：

```json
{
  "schema": "aifar-artifact-bundle-v1",
  "app": "aifar",
  "kind": "aifar-service-artifacts",
  "services": []
}
```

每个服务项包含 `service`、`module`、`artifact`、`fileName`、`sha256` 和 `size`。

## 进度与错误处理

Core 通过进度回调上报阶段和日志，WinForms 使用后台任务执行打包并切回 UI 线程更新界面。阶段包括：校验、发现制品、复制/压缩、计算摘要、生成 manifest、生成最终 ZIP、清理。

错误信息必须包含服务名和实际路径，不显示无上下文的异常。常见错误包括目录不存在、缺少 `index.html`、找不到 JAR、存在多个候选、输出扩展名错误、空间不足、访问拒绝和临时文件清理失败。失败时日志保留，弹出简短错误摘要，原输出 ZIP 保持不变。

## 测试与验收

自动测试覆盖：

- `all` 等价的全服务选择和任意部分服务选择。
- 每个 Java 模块目标目录及规范化文件名。
- 无候选、多候选和应排除 JAR 的发现规则。
- Web 内层 ZIP 直接以 `index.html`、`assets/` 为根。
- manifest 字段、服务顺序、SHA256 和 size。
- ZIP entry 使用 `/` 且不存在 staging 路径泄漏。
- 失败不覆盖已有输出，成功才替换。
- staging 与临时 ZIP 清理。
- 设置保存、恢复、损坏回退和默认值。
- WinForms 项目可构建，`dotnet publish` 产物只有可直接运行的主 EXE 和明确允许的符号文件；交付目录只保留 EXE。

真实验收使用当前 Alpha Java/Web 源目录分别生成全量包和 `gateway,im,meeting,web-vue3` 部分包，复验 ZIP 结构、manifest、逐项 SHA256/size，并与现有 PowerShell 结果保持协议等价。

## 非目标

- 不修改 AIFAR 后端的 bundle 上传协议。
- 不在 GUI 中编译 Java 或 Web 项目，只使用已有 `target` JAR 和 `dist`。
- 不上传包到面板，不连接服务器。
- 不做跨平台 GUI；首版仅支持 Windows x64。
