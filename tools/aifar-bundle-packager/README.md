# AIFAR Bundle Packager

该目录包含生成 `aifar-artifact-bundle-v1` 更新包的 Windows 工具。

## 图形化工具

`AifarBundlePackager.sln` 包含 Core、WinForms 和 xUnit 测试。使用以下命令执行测试、发布 Windows x64 自包含单文件，并把唯一交付物写入 `deploy/bin/AIFARBundlePackager.exe`：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/aifar-bundle-packager/build.ps1
```

如需指定 .NET：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/aifar-bundle-packager/build.ps1 -DotNetPath D:\tools\dotnet\dotnet.exe
```

## 命令行工具

`cli/` 保留 CMD + PowerShell 的命令行打包方式。编辑 CMD 顶部的 Java、Web 和输出路径后，可打全量或指定服务：

```bat
tools\aifar-bundle-packager\cli\package-aifar-artifact-bundle.cmd
tools\aifar-bundle-packager\cli\package-aifar-artifact-bundle.cmd gateway,im,meeting,web-vue3
```

图形化工具与命令行工具生成相同协议，但运行时互不调用。

## 测试

```powershell
pnpm test:tools
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release
```

## GitHub Release

推送形如 `aifar-bundle-packager-vX.Y.Z` 的已存在标签后，Windows CI 会从该标签对应的提交重新测试和构建，并创建 Draft Release。Draft 只包含以下三个资产：

- `AIFARBundlePackager.exe`
- `AIFARBundlePackager.exe.sha256`
- `release-manifest.json`

Draft Release 不是最终发布。发布前需下载并复核校验和与 manifest，并在未安装 .NET Runtime 的 Windows x64 环境完成启动、Java-only、Web-only 和 mixed 三类验收。

生成的 EXE is not stored in Git；`deploy/bin/AIFARBundlePackager.exe` 仅用于本地构建和烟测，正式交付以通过人工验收后发布的 GitHub Release 资产为准。
