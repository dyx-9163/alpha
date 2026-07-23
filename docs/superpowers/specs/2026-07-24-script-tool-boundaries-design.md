# AIFAR 脚本与辅助工具目录边界设计

## 目标

让根目录 `scripts/` 只保留 AIFAR Deployment 本身的开发、启动、构建、测试、发布和离线资源恢复脚本；把为其他能力准备输入或生成交付文件的独立辅助工具迁入 `tools/`，并保持现有行为、产物协议和测试覆盖不变。

## 边界

保留在 `scripts/` 的内容包括：

- AIFAR 服务端与前端的启动、停止、开发和构建入口。
- AIFAR 运行包的打包、校验、资源解压和 tar 权限修正。
- 后端、前端、脚本、发布、只读验收和可变更验收门禁。
- 对 `extras/` 中安装脚本和运行时契约的仓库级测试。

迁入 `tools/` 的内容包括：

- AIFAR Bundle Packager 的构建脚本与构建契约测试。
- 与图形化打包器生成相同 `aifar-artifact-bundle-v1` 协议的 PowerShell/CMD 命令行打包工具及其测试。
- 从 Alpha Java 工程复制可运行 JAR 到 AIFAR Runtime 资源目录的导出工具及其测试。

`scripts/build_openlab_inventory_docx.py` 没有仓库调用方，且内嵌环境清单和登录信息。`tools/` 不作为一次性文件或敏感数据的归档区，因此本轮不迁移该文件，只将其列为后续需要单独授权处理的清理项。

## 目标结构

```text
tools/
├─ aifar-bundle-packager/
│  ├─ build.ps1
│  ├─ build.test.mjs
│  ├─ README.md
│  ├─ cli/
│  │  ├─ package-aifar-artifact-bundle.cmd
│  │  ├─ package-aifar-artifact-bundle.ps1
│  │  └─ package-aifar-artifact-bundle.test.mjs
│  ├─ src/
│  └─ tests/
├─ alpha-jar-export/
│  ├─ README.md
│  ├─ export-alpha-jars.cmd
│  ├─ export-alpha-jars.ps1
│  └─ export-alpha-jars.test.mjs
└─ test-tools.mjs
```

## 调用与路径

- `tools/aifar-bundle-packager/build.ps1` 从自身目录向上解析仓库根目录，继续测试解决方案、发布 Windows x64 自包含单文件，并原子替换 `deploy/bin/AIFARBundlePackager.exe`。
- CLI 打包工具仍从显式 Java/Web 输入生成更新包，不调用图形化程序，也不改变 manifest、SHA256、文件大小、服务顺序或失败不覆盖旧产物的契约。
- Alpha JAR 导出工具在未传 `TargetRoot` 时从工具目录解析仓库根目录，默认写入 `resources/aifar/runtime-v2/services/<service>/target/`；显式 `TargetRoot` 行为保持不变。
- 前端更新包提示改为指向 `AIFARBundlePackager.exe`，不再错误声称 JAR 导出脚本会生成 ZIP。
- 历史设计和实施记录保留当时的路径语境；工具 README 和当前 CI 配置作为现行入口说明。

## 测试组织

新增 `tools/test-tools.mjs`，递归发现 `tools/` 下的 `*.test.mjs`，但排除构建输出目录。根 `package.json` 新增 `test:tools`，并将它接入：

- `scripts/test-local.mjs` 的完整本地门禁。
- CI 的跨平台脚本/工具契约阶段。

原 `scripts/test-scripts.mjs` 继续只发现 `scripts/` 顶层的 `*.test.mjs`，从而让产品脚本测试和独立工具测试边界清晰。Windows CI 继续单独运行 .NET 测试与单文件发布；非 Windows 环境中的 PowerShell 依赖测试沿用现有跳过条件。

## 错误处理与安全

- 移动只改变位置，不放宽路径校验、临时目录清理、失败不覆盖旧产物和单文件交付检查。
- 工具测试必须验证新路径和 CI 调用，防止移动后出现未执行的“孤儿测试”。
- `.gitignore` 将具体的偶发 staging 文件改为可复用的临时路径模式，避免以后再次跟踪更新包 staging 内容。
- 不删除、改写或迁移 OpenLab DOCX 生成器中的数据；该问题需要独立确认处理方式。

## 验收标准

- `scripts/` 中不再存在 Bundle Packager、更新包 CLI 或 Alpha JAR 导出工具文件。
- `pnpm test:scripts` 通过，并且只覆盖 AIFAR 项目脚本。
- `pnpm test:tools` 通过，并覆盖三个辅助工具边界。
- Windows 上 Bundle Packager 的 .NET 测试和 `build.ps1` 单文件发布通过。
- `pnpm test:web` 或对应前端规则测试通过，确认更新包提示键仍被正确使用。
- `git diff --check` 通过，活动代码、CI、README 和用户可见提示中没有旧工具路径。
