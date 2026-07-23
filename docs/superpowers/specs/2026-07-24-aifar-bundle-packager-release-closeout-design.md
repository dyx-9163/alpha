# AIFAR Bundle Packager Release 收口设计

## 1. 背景

`AIFARBundlePackager.exe` 的功能已完成，当前源码覆盖服务目录、JAR 发现、事务式更新包生成、WinForms 交互、按服务类型条件要求 Java/Web 路径，以及 Windows x64 自包含单文件发布。当前本地 EXE 大小为 161,672,360 字节，不应写入 Git 历史。

目前剩余的收口问题不是功能缺口，而是：

- 没有长期可下载的正式 EXE 发布渠道。
- EXE 没有与标签、Git commit、SHA256 和文件大小绑定的发布元数据。
- 两份实施计划仍保留未回填的步骤复选框，没有一个以当前源码、提交和测试为依据的完成状态矩阵。

GitHub Release 单个资产必须小于 2 GiB，且 Release 总大小与下载带宽不受该页面声明的额外限制；当前 EXE 符合该资产边界。依据：[GitHub About releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases)。

## 2. 目标

- 用 GitHub Release 发布 `AIFARBundlePackager.exe`，EXE 始终不进入 Git。
- 使标签版本、EXE 版本、Release 标题和发布 manifest 版本一致。
- 为每个 EXE 发布 SHA256、字节大小、Git commit、RID 和构建时间。
- 发布前执行自动化门禁，发布后对下载制品执行人工 GUI 与真实打包验收。
- 为旧实施计划建立可审计的任务级完成证据，不伪造无法重现的历史 RED 输出。

## 3. 非目标

- 不把 EXE 改为 Git LFS 对象。
- 不把 EXE 加入源码发布包或 `deploy/deployment` 平台包。
- 不重写已完成的 Core 打包协议、WinForms 页面或 CMD/PowerShell 兼容工具。
- 不在第一阶段引入代码签名证书、GitHub artifact attestation 或第三方制品库。
- 不从当前未跟踪的本地 EXE 直接上传；正式 Release 必须在标签对应的 GitHub Actions 运行中重新构建。

## 4. 发布标签与版本

正式标签格式为：

```text
aifar-bundle-packager-v<major>.<minor>.<patch>
```

示例：

```text
aifar-bundle-packager-v1.0.0
```

约束：

- 只允许三段数字稳定版本，第一阶段不接受预发布后缀。
- workflow 从 `github.ref_name` 移除前缀得到 `X.Y.Z`，并重新验证完整标签。
- `Version` 使用 `X.Y.Z`，`FileVersion` 与 `AssemblyVersion` 使用 `X.Y.Z.0`。
- `InformationalVersion` 使用 `X.Y.Z+<40-char-sha>`，附带完整 Git commit。
- 已发布的标签不复用；验收失败时修复源码并发布新 patch 版本。

## 5. 构建与资产契约

### 5.1 构建脚本

`tools/aifar-bundle-packager/build.ps1` 继续负责：

- 校验 .NET 8 执行文件。
- 执行 Bundle Packager solution 测试。
- 生成 Windows x64、自包含、单文件、未裁剪 EXE。
- 验证 publish 目录只有 `AIFARBundlePackager.exe`。
- 原子替换本地 `deploy/bin/AIFARBundlePackager.exe`。

脚本新增参数：

```powershell
[string]$Version
[string]$SourceRevisionId
```

两个参数必须同时提供或同时省略。本地开发省略时保持当前构建行为；Release workflow 必须显式提供。

### 5.2 Release 资产生成器

新增 `tools/aifar-bundle-packager/create-release-assets.ps1`，只从已构建的 EXE 生成发布资产，不再次编译源码。参数为：

```powershell
-ExecutablePath <absolute-or-repository-relative-path>
-Version <X.Y.Z>
-SourceRevisionId <40-char-sha>
-OutputDirectory <path>
```

输出目录必须在创建前为空或不存在，最终只允许三个文件：

```text
AIFARBundlePackager.exe
AIFARBundlePackager.exe.sha256
release-manifest.json
```

SHA256 文件使用小写十六进制和标准双空格格式：

```text
<64-char-lowercase-sha256>  AIFARBundlePackager.exe
```

`release-manifest.json` 契约为：

```json
{
  "schema": "aifar-bundle-packager-release-v1",
  "version": "1.0.0",
  "gitCommit": "0123456789abcdef0123456789abcdef01234567",
  "runtimeIdentifier": "win-x64",
  "fileName": "AIFARBundlePackager.exe",
  "size": 161672360,
  "sha256": "64-char-lowercase-sha256",
  "builtAt": "2026-07-24T00:00:00Z"
}
```

`builtAt` 使用 UTC ISO 8601。资产生成器必须通过 `FileVersionInfo` 复验 EXE 的 file version 精确为 `X.Y.Z.0`，product/informational version 包含 `X.Y.Z+<40-char-sha>`，并拒绝空文件、大于或等于 2 GiB 的文件、非 PE EXE 或不符合名称的文件。脚本先在输出目录同级的 GUID 临时目录生成并复验全部资产，成功后才将完整目录移入最终位置；失败时清理临时目录。

## 6. GitHub Actions 发布流程

新增 `.github/workflows/aifar-bundle-packager-release.yml`，仅匹配：

```yaml
on:
  push:
    tags:
      - "aifar-bundle-packager-v*"
```

workflow 使用 Windows runner，设置 `timeout-minutes: 30`，并限制权限：

```yaml
permissions:
  contents: write
```

单个 job 按以下顺序执行：

1. Checkout 完整的标签提交。
2. 安装 Node.js 22、pnpm 11.7.0 和 .NET 8。
3. 验证标签格式，导出 `PACKAGER_VERSION`，并证明标签解析到的 commit 与 `github.sha` 完全一致。
4. 执行 `pnpm test:tools`。
5. 执行 `pnpm test:scripts`。
6. 执行 `dotnet test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release`。
7. 检查 `git ls-files` 不包含 `deploy/bin/AIFARBundlePackager.exe`，且标签范围没有大于 100 MB 的 Git blob。
8. 调用 `build.ps1` 并注入版本与 `github.sha`。
9. 调用 `create-release-assets.ps1` 生成三个资产。
10. 独立复算 SHA256、文件大小、manifest 和 EXE 版本。
11. 使用 `gh release create <tag> <three-assets> --draft --verify-tag` 一次性创建 Draft Release 并上传三个资产；不使用 `--target` 重定向已存在标签。

Release 标题为：

```text
AIFAR Bundle Packager vX.Y.Z
```

Release notes 必须包含 Git commit、RID、EXE 字节大小、SHA256、三类服务选择规则和人工验收步骤。

workflow 失败时不创建可见的正式 Release。如 `gh release create` 发生部分失败，只允许保留或删除 Draft，不能将不完整 Draft 发布为正式版本。

## 7. Draft Release 人工验收

自动化成功后，发布人必须在发布 Draft 前完成：

1. 从 Draft Release 下载三个资产，不使用 workspace 内本地 EXE。
2. 使用 `Get-FileHash -Algorithm SHA256` 复算 EXE，与 `.sha256` 和 manifest 同时比较。
3. 复核 manifest 的版本、Git commit、RID、文件名和字节大小。
4. 在没有预装 .NET Desktop Runtime 的 Windows x64 机器启动 EXE。
5. 确认启动后三个路径为空且只读，取消选择框不改变已选路径。
6. 分别生成 Java-only、Web-only 和 Java+Web mixed 更新包。
7. 复算三个更新包中每个 artifact 的 SHA256 和 size，验证根 manifest、服务顺序、ZIP `/` 分隔符和 `web-vue3.zip` 根结构。
8. 验收通过后手工将 Draft 发布；失败时保留验收证据，修复后使用新 patch 标签重新发布。

## 8. 实施计划文档收口

更新：

- `docs/superpowers/plans/2026-07-23-aifar-bundle-packager-winforms.md`
- `docs/superpowers/plans/2026-07-24-aifar-bundle-conditional-source-paths.md`

每份计划顶部新增“实施状态与证据”章节，包含：

- 总体状态：已实现、已自动验证、待正式 Release 人工验收。
- 每个 Task 对应的最终提交、生产文件、测试文件和最新通过数量。
- 文件移动后的当前路径，特别是 `tools/aifar-bundle-packager/build.ps1` 和 `tools/aifar-bundle-packager/cli/`。
- 未保留独立日志的历史 RED 步骤明确标记为“历史过程证据未持久化，当前回归覆盖已通过”，不追溯生成伪造日志。

原始步骤复选框保留为历史执行脚本，不作为当前完成状态的唯一来源。新增的证据矩阵是权威状态。

## 9. 测试设计

### 9.1 PowerShell/Node 契约测试

扩展 `tools/aifar-bundle-packager/build.test.mjs`，验证：

- 版本和 commit 参数必须同时提供。
- 标签版本和 commit SHA 严格校验。
- publish 参数将版本传入 MSBuild。
- EXE 仍为唯一本地交付文件。
- `.gitignore` 继续覆盖 `deploy/bin/AIFARBundlePackager.exe`。
- Release workflow 只响应指定标签，并以最小 `contents: write` 权限创建 Draft。

新增资产生成器契约与动态测试，验证：

- 仅产生三个文件。
- SHA256、size、version、commit 和 RID 正确。
- 空文件、错误名称、非法版本、非法 commit、非 PE 文件、不匹配的 EXE 版本和非空输出目录均失败。
- 失败不留下半成品资产。

### 9.2 自动门禁

实施完成后必须通过：

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release
pnpm test:tools
pnpm test:scripts
$currentCommit = git -c safe.directory=D:/workspace/aifar-deployment rev-parse HEAD
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/aifar-bundle-packager/build.ps1 -DotNetPath D:\tools\dotnet\dotnet.exe -Version 1.0.0 -SourceRevisionId $currentCommit
git diff --check
```

最后一条带版本的本地构建只用于验证参数与 EXE 版本，正式发布仍必须从 GitHub 标签重建。

## 10. 失败与恢复

- 测试、构建、版本校验、大文件检查、SHA256 复验任一失败：workflow 非零退出，不创建 Release。
- 资产上传失败：Release 只能保持 Draft；验证三个资产完整后才允许人工发布。
- 人工验收失败：禁止发布 Draft，将验收结果写入修复记录，修复后使用新 patch 版本。
- 已发布资产发现问题：不原位覆盖旧资产，标记 Release 说明并发布新 patch 版本。
- 源码工作区不会因 Release 失败自动删除或重写已有本地 EXE。

## 11. 验收标准

- `aifar-bundle-packager-vX.Y.Z` 标签能稳定创建同版本 Draft Release。
- Draft 只含契约中的三个资产。
- 下载 EXE 的 SHA256、size、Git commit、EXE 版本和 manifest 一致。
- EXE 可在未预装 .NET Desktop Runtime 的 Windows x64 机器启动。
- Java-only、Web-only 和 mixed 更新包均符合 `aifar-artifact-bundle-v1`。
- 当前自动化门禁全部通过，无与本次改造有关的新警告。
- Git 跟踪列表不包含 EXE，本次提交范围不含大于 100 MB 的 blob。
- 两份历史实施计划都有任务级完成状态、提交、文件和测试证据。
