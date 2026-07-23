# Alpha JAR Export

该工具从 Alpha Java 工程查找各服务的可运行 JAR，并复制到 AIFAR Runtime 离线资源目录。它只准备 Runtime 资源，不生成可上传的更新 ZIP。

默认源目录为 `D:\workspace\alpha\backend\alpha-java-cloud`，默认目标为当前仓库的 `resources\aifar\runtime-v2\services`：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/alpha-jar-export/export-alpha-jars.ps1
```

选择服务并要求全部存在：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/alpha-jar-export/export-alpha-jars.ps1 `
  -SourceRoot D:\workspace\alpha\backend\alpha-java-cloud `
  -Services gateway,im,meeting `
  -RequireAll
```

使用 `-TargetRoot` 可覆盖目标目录；`-Clean` 会先删除所选服务的整个 `target` 目录，未指定 `-Clean` 时只清理其中的旧 JAR。

运行测试：

```powershell
pnpm test:tools
```
