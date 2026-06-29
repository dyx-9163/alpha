package mysqlrouter

import "strings"

type Copy struct {
	CategoryLabel     string
	SourceLabel       string
	Description       string
	UsingArchive      string
	UsingRPMs         string
	UsingCluster      string
	MissingRPMWarning string
	StepStart         string
	StepDone          string
	StepFailed        string
	LoadServer        string
	ResolveCluster    string
	VerifyResource    string
	InstallRouter     string
	RecordInstance    string
	UpdateInstance    string
	CheckRuntime      string
	LoadFailed        string
	InstallFailed     string
	RecordFailed      string
	Installed         string
	TargetRequired    string
	ClusterRequired   string
	ClusterMissing    string
	ClusterNoEndpoint string
	RouterUnsupported string
	CheckFailed       string
	Checked           string
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
			CategoryLabel:     "Database",
			SourceLabel:       "MySQL official bundle",
			Description:       "Install MySQL Router for an existing MySQL InnoDB Cluster and expose the cluster through stable router ports.",
			UsingArchive:      "using MySQL official bundle: %s",
			UsingRPMs:         "using %d RPM dependency package(s)",
			UsingCluster:      "using MySQL InnoDB Cluster: %s (%s)",
			MissingRPMWarning: "MySQL RPM cache is empty; the installer will continue if runtime dependencies already exist on the target server",
			StepStart:         "MySQL Router step %d/%d started: %s",
			StepDone:          "MySQL Router step %d/%d completed: %s",
			StepFailed:        "MySQL Router step %d/%d failed: %s: %v",
			LoadServer:        "load Router target server",
			ResolveCluster:    "resolve existing MySQL InnoDB Cluster",
			VerifyResource:    "verify MySQL Router offline bundle",
			InstallRouter:     "install MySQL Router",
			RecordInstance:    "record MySQL Router app instance",
			UpdateInstance:    "update MySQL Router app instance",
			CheckRuntime:      "check MySQL Router runtime",
			LoadFailed:        "load server failed: %s",
			InstallFailed:     "MySQL Router install failed: %s",
			RecordFailed:      "record MySQL Router instance failed: %s",
			Installed:         "MySQL Router installed, instance recorded: %s",
			TargetRequired:    "MySQL Router install requires target server(s)",
			ClusterRequired:   "MySQL Router requires selecting an existing MySQL InnoDB Cluster",
			ClusterMissing:    "selected MySQL InnoDB Cluster was not found",
			ClusterNoEndpoint: "selected MySQL InnoDB Cluster has no usable bootstrap endpoint",
			RouterUnsupported: "MySQL Router topology is not supported: %s",
			CheckFailed:       "MySQL Router check failed: %s",
			Checked:           "MySQL Router status checked: %s",
		}
	default:
		return Copy{
			CategoryLabel:     "数据库",
			SourceLabel:       "MySQL 官方离线包",
			Description:       "在已有 MySQL InnoDB Cluster 上安装 MySQL Router，通过稳定 Router 端口暴露集群入口。",
			UsingArchive:      "使用 MySQL 官方离线包：%s",
			UsingRPMs:         "使用 %d 个 RPM 依赖包",
			UsingCluster:      "使用 MySQL InnoDB Cluster：%s（%s）",
			MissingRPMWarning: "MySQL RPM 缓存为空；如果目标服务器已具备运行依赖，安装会继续执行",
			StepStart:         "MySQL Router 步骤 %d/%d 开始：%s",
			StepDone:          "MySQL Router 步骤 %d/%d 完成：%s",
			StepFailed:        "MySQL Router 步骤 %d/%d 失败：%s：%v",
			LoadServer:        "读取 Router 目标服务器",
			ResolveCluster:    "解析已有 MySQL InnoDB Cluster",
			VerifyResource:    "校验 MySQL Router 离线包",
			InstallRouter:     "安装 MySQL Router",
			RecordInstance:    "记录 MySQL Router 应用实例",
			UpdateInstance:    "更新 MySQL Router 应用实例",
			CheckRuntime:      "检查 MySQL Router 运行状态",
			LoadFailed:        "读取服务器失败：%s",
			InstallFailed:     "MySQL Router 安装失败：%s",
			RecordFailed:      "记录 MySQL Router 实例失败：%s",
			Installed:         "MySQL Router 已安装，实例已记录：%s",
			TargetRequired:    "MySQL Router 安装需要选择目标服务器",
			ClusterRequired:   "MySQL Router 需要先选择已有 MySQL InnoDB Cluster",
			ClusterMissing:    "未找到选择的 MySQL InnoDB Cluster",
			ClusterNoEndpoint: "选择的 MySQL InnoDB Cluster 没有可用的 bootstrap 端点",
			RouterUnsupported: "MySQL Router 不支持该拓扑：%s",
			CheckFailed:       "MySQL Router 检测失败：%s",
			Checked:           "MySQL Router 状态已检测：%s",
		}
	}
}

func DeleteCopyFor(lang string) DeleteCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return DeleteCopy{
			RemoveRemote:   "remove MySQL Router service and files from target server",
			DeleteInstance: "delete MySQL Router app instance record",
			StepStart:      "MySQL Router delete step %d/%d started: %s",
			StepDone:       "MySQL Router delete step %d/%d completed: %s",
			StepFailed:     "MySQL Router delete step %d/%d failed: %s: %v",
			DeleteFailed:   "MySQL Router deployed service delete failed: %s",
			Deleted:        "MySQL Router deployed service deleted: %s",
		}
	default:
		return DeleteCopy{
			RemoveRemote:   "从目标服务器移除 MySQL Router 服务和文件",
			DeleteInstance: "删除 MySQL Router 应用实例记录",
			StepStart:      "MySQL Router 删除步骤 %d/%d 开始：%s",
			StepDone:       "MySQL Router 删除步骤 %d/%d 完成：%s",
			StepFailed:     "MySQL Router 删除步骤 %d/%d 失败：%s：%v",
			DeleteFailed:   "MySQL Router 部署服务删除失败：%s",
			Deleted:        "MySQL Router 部署服务已删除：%s",
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
