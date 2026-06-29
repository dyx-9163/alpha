package mysql

import "strings"

type Copy struct {
	CategoryLabel        string
	SourceLabel          string
	Description          string
	UsingArchive         string
	UsingRPMs            string
	MissingRPMWarning    string
	StepStart            string
	StepDone             string
	StepFailed           string
	LoadServer           string
	VerifyResource       string
	InstallStandalone    string
	BootstrapCluster     string
	RecordInstance       string
	LoadFailed           string
	InstallFailed        string
	RecordFailed         string
	Installed            string
	ClusterNodeInstalled string
	ClusterInstalled     string
	TargetRequired       string
	SingleTargetOnly     string
	ClusterNeedNodes     string
	ClusterUnsupported   string
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
			CategoryLabel:        "Database",
			SourceLabel:          "Official binary bundle",
			Description:          "Install MySQL standalone or InnoDB Cluster from the offline official 8.0 binary bundle.",
			UsingArchive:         "using MySQL official bundle: %s",
			UsingRPMs:            "using %d RPM dependency package(s)",
			MissingRPMWarning:    "MySQL RPM cache is empty; the installer will continue if runtime dependencies already exist on the target server",
			StepStart:            "MySQL step %d/%d started: %s",
			StepDone:             "MySQL step %d/%d completed: %s",
			StepFailed:           "MySQL step %d/%d failed: %s: %v",
			LoadServer:           "load target server",
			VerifyResource:       "verify MySQL offline bundle",
			InstallStandalone:    "install MySQL base service",
			BootstrapCluster:     "bootstrap MySQL InnoDB Cluster",
			RecordInstance:       "record MySQL app instance",
			LoadFailed:           "load server failed: %s",
			InstallFailed:        "MySQL install failed: %s",
			RecordFailed:         "record MySQL instance failed: %s",
			Installed:            "MySQL standalone installed, instance recorded: %s",
			ClusterNodeInstalled: "MySQL InnoDB Cluster node installed, instance recorded: %s",
			ClusterInstalled:     "MySQL InnoDB Cluster installed, %d instance record(s) created",
			TargetRequired:       "MySQL install requires target server(s)",
			SingleTargetOnly:     "MySQL standalone install supports only one target server",
			ClusterNeedNodes:     "MySQL InnoDB Cluster requires at least 3 target servers",
			ClusterUnsupported:   "MySQL topology is not supported: %s",
		}
	default:
		return Copy{
			CategoryLabel:      "数据库",
			SourceLabel:        "官方二进制包",
			Description:        "基于离线官方 8.0 二进制包安装 MySQL 单体或 InnoDB Cluster。",
			UsingArchive:       "使用 MySQL 官方离线包：%s",
			UsingRPMs:          "使用 %d 个 RPM 依赖包",
			MissingRPMWarning:  "MySQL RPM 缓存为空；如果目标服务器已经具备运行依赖，安装会继续执行",
			StepStart:          "MySQL 步骤 %d/%d 开始：%s",
			StepDone:           "MySQL 步骤 %d/%d 完成：%s",
			StepFailed:         "MySQL 步骤 %d/%d 失败：%s：%v",
			LoadServer:         "读取目标服务器",
			VerifyResource:     "校验 MySQL 离线包",
			InstallStandalone:  "安装 MySQL 基础服务",
			BootstrapCluster:   "初始化 MySQL InnoDB Cluster",
			RecordInstance:     "记录 MySQL 应用实例",
			LoadFailed:         "读取服务器失败：%s",
			InstallFailed:      "MySQL 安装失败：%s",
			RecordFailed:       "记录 MySQL 实例失败：%s",
			Installed:          "MySQL 单体已安装，实例已记录：%s",
			ClusterInstalled:   "MySQL InnoDB Cluster 已安装，已记录 %d 个实例",
			TargetRequired:     "MySQL 安装需要选择目标服务器",
			SingleTargetOnly:   "MySQL 单体安装只支持一个目标服务器",
			ClusterNeedNodes:   "MySQL InnoDB Cluster 至少需要 3 台目标服务器",
			ClusterUnsupported: "MySQL 不支持该拓扑：%s",
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

func normalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(lang, "en") {
		return "en"
	}
	return "zh"
}
