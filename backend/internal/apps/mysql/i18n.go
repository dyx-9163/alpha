package mysql

import (
	"strings"

	globali18n "aifar-deployment/backend/internal/i18n"
)

// MySQLBackupErrorText turns an approved stable error code into a localized,
// non-secret user message. Unknown codes intentionally fall back to the code.
func MySQLBackupErrorText(lang, code string) string {
	return globali18n.MySQLBackupErrorText(lang, code)
}

type BackupCopy struct {
	StepStart                string
	StepDone                 string
	StepFailed               string
	RemoteCleanupFailed      string
	RetentionSelected        string
	RetentionCleanupFailed   string
	VerificationFailed       string
	VerificationRecordFailed string
	StepTitles               map[string]string
}

func BackupCopyFor(lang string) BackupCopy {
	names := []string{"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql", "check-backup-space", "prepare-workdir", "dry-run-dump", "dump-instance", "build-manifest", "package-backup", "transfer-backup", "verify-checksum", "record-backup", "apply-retention", "cleanup-workdir"}
	titles := make(map[string]string, len(names))
	for _, name := range names {
		titles[name] = globali18n.Text(lang, "mysql.backup.step."+name)
	}
	for _, name := range []string{"load-backup", "verify-manifest", "verify-checksum", "record-verification"} {
		titles[name] = globali18n.Text(lang, "mysql.backup.verify.step."+name)
	}
	return BackupCopy{
		StepStart:                globali18n.Text(lang, "mysql.backup.stepStart"),
		StepDone:                 globali18n.Text(lang, "mysql.backup.stepDone"),
		StepFailed:               globali18n.Text(lang, "mysql.backup.stepFailed"),
		RemoteCleanupFailed:      globali18n.Text(lang, "mysql.backup.remoteCleanupFailed"),
		RetentionSelected:        globali18n.Text(lang, "mysql.backup.retentionSelected"),
		RetentionCleanupFailed:   globali18n.Text(lang, "mysql.backup.retentionCleanupFailed"),
		VerificationFailed:       globali18n.Text(lang, "mysql.backup.verificationFailed"),
		VerificationRecordFailed: globali18n.Text(lang, "mysql.backup.verificationRecordFailed"),
		StepTitles:               titles,
	}
}

type Copy struct {
	CategoryLabel         string
	SourceLabel           string
	Description           string
	UsingArchive          string
	UsingRPMs             string
	MissingRPMWarning     string
	StepStart             string
	StepDone              string
	StepFailed            string
	LoadServer            string
	VerifyResource        string
	InstallStandalone     string
	BootstrapCluster      string
	RecordInstance        string
	LoadRouterServer      string
	InstallRouter         string
	RecordRouterInstance  string
	LoadFailed            string
	InstallFailed         string
	RouterInstallFailed   string
	RecordFailed          string
	Installed             string
	ClusterNodeInstalled  string
	ClusterInstalled      string
	InstallRouterGroup    string
	RouterInstalled       string
	TargetRequired        string
	SingleTargetOnly      string
	ClusterNeedNodes      string
	RouterTargetsRequired string
	ClusterUnsupported    string
}

type DeleteCopy struct {
	RemoveRemote   string
	DeleteInstance string
	StepStart      string
	StepDone       string
	StepFailed     string
	DeleteFailed   string
	Deleted        string
}

func CopyFor(lang string) Copy {
	switch normalizeLanguage(lang) {
	case "en":
		return Copy{
			CategoryLabel:         "Database",
			SourceLabel:           "Official binary bundle",
			Description:           "Install MySQL standalone or InnoDB Cluster from the offline official 8.0 binary bundle.",
			UsingArchive:          "using MySQL official bundle: %s",
			UsingRPMs:             "using %d RPM dependency package(s)",
			MissingRPMWarning:     "MySQL RPM cache is empty; the installer will continue if runtime dependencies already exist on the target server",
			StepStart:             "MySQL step %d/%d started: %s",
			StepDone:              "MySQL step %d/%d completed: %s",
			StepFailed:            "MySQL step %d/%d failed: %s: %v",
			LoadServer:            "load target server",
			VerifyResource:        "verify MySQL offline bundle",
			InstallStandalone:     "install MySQL base service",
			BootstrapCluster:      "bootstrap MySQL InnoDB Cluster",
			RecordInstance:        "record MySQL app instance",
			LoadRouterServer:      "load MySQL Router target server",
			InstallRouter:         "install MySQL Router service",
			RecordRouterInstance:  "record MySQL Router app instance",
			LoadFailed:            "load server failed: %s",
			InstallFailed:         "MySQL install failed: %s",
			RouterInstallFailed:   "MySQL Router install failed: %s",
			RecordFailed:          "record MySQL instance failed: %s",
			Installed:             "MySQL standalone installed, instance recorded: %s",
			ClusterNodeInstalled:  "MySQL InnoDB Cluster node installed, instance recorded: %s",
			ClusterInstalled:      "MySQL InnoDB Cluster installed, %d instance record(s) created",
			InstallRouterGroup:    "install MySQL Router on %d target server(s)",
			RouterInstalled:       "MySQL Router installed, instance recorded: %s",
			TargetRequired:        "MySQL install requires target server(s)",
			SingleTargetOnly:      "MySQL standalone install supports only one target server",
			ClusterNeedNodes:      "MySQL InnoDB Cluster requires at least 3 target servers",
			RouterTargetsRequired: "MySQL Router install requires at least one target server",
			ClusterUnsupported:    "MySQL topology is not supported: %s",
		}
	default:
		return Copy{
			CategoryLabel:         "数据库",
			SourceLabel:           "官方二进制包",
			Description:           "基于离线官方 8.0 二进制包安装 MySQL 单体或 InnoDB Cluster。",
			UsingArchive:          "使用 MySQL 官方离线包：%s",
			UsingRPMs:             "使用 %d 个 RPM 依赖包",
			MissingRPMWarning:     "MySQL RPM 缓存为空；如果目标服务器已经具备运行依赖，安装会继续执行",
			StepStart:             "MySQL 步骤 %d/%d 开始：%s",
			StepDone:              "MySQL 步骤 %d/%d 完成：%s",
			StepFailed:            "MySQL 步骤 %d/%d 失败：%s：%v",
			LoadServer:            "读取目标服务器",
			VerifyResource:        "校验 MySQL 离线包",
			InstallStandalone:     "安装 MySQL 基础服务",
			BootstrapCluster:      "初始化 MySQL InnoDB Cluster",
			RecordInstance:        "记录 MySQL 应用实例",
			LoadRouterServer:      "读取 MySQL Router 目标服务器",
			InstallRouter:         "安装 MySQL Router 服务",
			RecordRouterInstance:  "记录 MySQL Router 应用实例",
			LoadFailed:            "读取服务器失败：%s",
			InstallFailed:         "MySQL 安装失败：%s",
			RouterInstallFailed:   "MySQL Router 安装失败：%s",
			RecordFailed:          "记录 MySQL 实例失败：%s",
			Installed:             "MySQL 单体已安装，实例已记录：%s",
			ClusterNodeInstalled:  "MySQL InnoDB Cluster 节点已安装，实例已记录：%s",
			ClusterInstalled:      "MySQL InnoDB Cluster 已安装，已记录 %d 个实例",
			InstallRouterGroup:    "在 %d 台目标服务器上安装 MySQL Router",
			RouterInstalled:       "MySQL Router 已安装，实例已记录：%s",
			TargetRequired:        "MySQL 安装需要选择目标服务器",
			SingleTargetOnly:      "MySQL 单体安装只支持一个目标服务器",
			ClusterNeedNodes:      "MySQL InnoDB Cluster 至少需要 3 台目标服务器",
			RouterTargetsRequired: "MySQL Router 安装至少需要 1 台目标服务器",
			ClusterUnsupported:    "MySQL 不支持该拓扑：%s",
		}
	}
}

func DeleteCopyFor(lang string) DeleteCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return DeleteCopy{
			RemoveRemote:   "remove MySQL service and files from target server",
			DeleteInstance: "delete MySQL app instance record",
			StepStart:      "MySQL delete step %d/%d started: %s",
			StepDone:       "MySQL delete step %d/%d completed: %s",
			StepFailed:     "MySQL delete step %d/%d failed: %s: %v",
			DeleteFailed:   "MySQL deployed service delete failed: %s",
			Deleted:        "MySQL deployed service deleted: %s",
		}
	default:
		return DeleteCopy{
			RemoveRemote:   "从目标服务器移除 MySQL 服务和文件",
			DeleteInstance: "删除 MySQL 应用实例记录",
			StepStart:      "MySQL 删除步骤 %d/%d 开始：%s",
			StepDone:       "MySQL 删除步骤 %d/%d 完成：%s",
			StepFailed:     "MySQL 删除步骤 %d/%d 失败：%s：%v",
			DeleteFailed:   "MySQL 部署服务删除失败：%s",
			Deleted:        "MySQL 部署服务已删除：%s",
		}
	}
}

type CheckCopy struct {
	StepStart      string
	StepDone       string
	StepFailed     string
	CheckRuntime   string
	DetectPrimary  string
	UpdateInstance string
	CheckFailed    string
	Checked        string
}

type ClusterStartCopy struct {
	StepStart        string
	StepDone         string
	StepFailed       string
	LoadCluster      string
	StartCluster     string
	DetectPrimary    string
	UpdateInstance   string
	StartFailed      string
	Started          string
	ClusterRequired  string
	ClusterMixed     string
	ClusterNoServers string
}

func CheckCopyFor(lang string) CheckCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return CheckCopy{
			StepStart:      "MySQL check step %d/%d started: %s",
			StepDone:       "MySQL check step %d/%d completed: %s",
			StepFailed:     "MySQL check step %d/%d failed: %s: %v",
			CheckRuntime:   "check MySQL service runtime",
			DetectPrimary:  "detect InnoDB Cluster primary",
			UpdateInstance: "update MySQL instance status",
			CheckFailed:    "MySQL check failed: %s",
			Checked:        "MySQL check completed, status=%s",
		}
	default:
		return CheckCopy{
			StepStart:      "MySQL 检测步骤 %d/%d 开始：%s",
			StepDone:       "MySQL 检测步骤 %d/%d 完成：%s",
			StepFailed:     "MySQL 检测步骤 %d/%d 失败：%s：%v",
			CheckRuntime:   "检测 MySQL 服务运行状态",
			DetectPrimary:  "识别 InnoDB Cluster 当前 Primary",
			UpdateInstance: "更新 MySQL 实例状态",
			CheckFailed:    "MySQL 检测失败：%s",
			Checked:        "MySQL 检测完成，状态=%s",
		}
	}
}

func ClusterStartCopyFor(lang string) ClusterStartCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return ClusterStartCopy{
			StepStart:        "MySQL cluster start step %d/%d started: %s",
			StepDone:         "MySQL cluster start step %d/%d completed: %s",
			StepFailed:       "MySQL cluster start step %d/%d failed: %s: %v",
			LoadCluster:      "load InnoDB Cluster topology",
			StartCluster:     "start MySQL InnoDB Cluster",
			DetectPrimary:    "detect InnoDB Cluster primary",
			UpdateInstance:   "update MySQL cluster instance status",
			StartFailed:      "MySQL InnoDB Cluster start failed: %s",
			Started:          "MySQL InnoDB Cluster started, primary=%s",
			ClusterRequired:  "select MySQL InnoDB Cluster instances",
			ClusterMixed:     "selected instances are not in the same MySQL InnoDB Cluster",
			ClusterNoServers: "MySQL InnoDB Cluster has no linked servers",
		}
	default:
		return ClusterStartCopy{
			StepStart:        "MySQL 集群启动步骤 %d/%d 开始：%s",
			StepDone:         "MySQL 集群启动步骤 %d/%d 完成：%s",
			StepFailed:       "MySQL 集群启动步骤 %d/%d 失败：%s：%v",
			LoadCluster:      "读取 InnoDB Cluster 拓扑",
			StartCluster:     "启动 MySQL InnoDB Cluster",
			DetectPrimary:    "识别 InnoDB Cluster 当前 Primary",
			UpdateInstance:   "更新 MySQL 集群实例状态",
			StartFailed:      "MySQL InnoDB Cluster 启动失败：%s",
			Started:          "MySQL InnoDB Cluster 已启动，Primary=%s",
			ClusterRequired:  "请选择 MySQL InnoDB Cluster 实例",
			ClusterMixed:     "所选实例不属于同一个 MySQL InnoDB Cluster",
			ClusterNoServers: "MySQL InnoDB Cluster 没有关联服务器",
		}
	}
}

func normalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(lang, "en") {
		return "en"
	}
	return "zh"
}
