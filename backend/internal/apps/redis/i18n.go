package redis

import "strings"

type Copy struct {
	CategoryLabel       string
	SourceLabel         string
	Description         string
	UsingArchive        string
	UsingRPMs           string
	StepStart           string
	StepDone            string
	StepFailed          string
	LoadServer          string
	VerifyResource      string
	InstallStandalone   string
	ConfigureSentinel   string
	EnableClusterNode   string
	BootstrapCluster    string
	RecordInstance      string
	LoadFailed          string
	InstallFailed       string
	RecordFailed        string
	Installed           string
	ClusterInstalled    string
	BatchFailed         string
	TargetRequired      string
	SingleTargetOnly    string
	SentinelNeedNodes   string
	ClusterNeedNodes    string
	TopologyUnsupported string
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
			CategoryLabel:       "Database",
			SourceLabel:         "Official source archive",
			Description:         "Build and install Redis standalone, Sentinel, or Cluster topology from the offline source archive.",
			UsingArchive:        "using Redis archive: %s",
			UsingRPMs:           "using %d RPM dependency package(s)",
			StepStart:           "Redis step %d/%d started: %s",
			StepDone:            "Redis step %d/%d completed: %s",
			StepFailed:          "Redis step %d/%d failed: %s: %v",
			LoadServer:          "load target server",
			VerifyResource:      "verify Redis offline archive",
			InstallStandalone:   "compile and install Redis base service",
			ConfigureSentinel:   "configure Redis Sentinel topology",
			EnableClusterNode:   "enable Redis Cluster node",
			BootstrapCluster:    "bootstrap Redis Cluster",
			RecordInstance:      "record Redis app instance",
			LoadFailed:          "load server failed: %s",
			InstallFailed:       "Redis install failed: %s",
			RecordFailed:        "record Redis instance failed: %s",
			Installed:           "Redis standalone installed, instance recorded: %s",
			ClusterInstalled:    "Redis %s topology installed, %d instance record(s) created",
			BatchFailed:         "Redis install finished with %d failure(s): %s",
			TargetRequired:      "Redis install requires target server(s)",
			SingleTargetOnly:    "Redis standalone install supports only one target server",
			SentinelNeedNodes:   "Redis Sentinel requires at least 3 target servers",
			ClusterNeedNodes:    "Redis Cluster requires at least 3 target servers",
			TopologyUnsupported: "Redis topology is not supported: %s",
		}
	default:
		return Copy{
			CategoryLabel:       "数据库",
			SourceLabel:         "官方源码包",
			Description:         "基于离线源码包安装 Redis 单体、Sentinel 或 Cluster 拓扑。",
			UsingArchive:        "使用 Redis 资源包：%s",
			UsingRPMs:           "使用 %d 个 RPM 依赖包",
			StepStart:           "Redis 步骤 %d/%d 开始：%s",
			StepDone:            "Redis 步骤 %d/%d 完成：%s",
			StepFailed:          "Redis 步骤 %d/%d 失败：%s：%v",
			LoadServer:          "读取目标服务器",
			VerifyResource:      "校验 Redis 离线包",
			InstallStandalone:   "编译并安装 Redis 基础服务",
			ConfigureSentinel:   "配置 Redis Sentinel 拓扑",
			EnableClusterNode:   "启用 Redis Cluster 节点",
			BootstrapCluster:    "初始化 Redis Cluster",
			RecordInstance:      "记录 Redis 应用实例",
			LoadFailed:          "读取服务器失败：%s",
			InstallFailed:       "Redis 安装失败：%s",
			RecordFailed:        "记录 Redis 实例失败：%s",
			Installed:           "Redis 单体已安装，实例已记录：%s",
			ClusterInstalled:    "Redis %s 拓扑已安装，已记录 %d 个实例",
			BatchFailed:         "Redis 安装完成，但有 %d 个失败：%s",
			TargetRequired:      "Redis 安装需要选择目标服务器",
			SingleTargetOnly:    "Redis 单体安装只支持一个目标服务器",
			SentinelNeedNodes:   "Redis Sentinel 至少需要 3 台目标服务器",
			ClusterNeedNodes:    "Redis Cluster 至少需要 3 台目标服务器",
			TopologyUnsupported: "Redis 不支持该拓扑：%s",
		}
	}
}

func DeleteCopyFor(lang string) DeleteCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return DeleteCopy{
			RemoveRemote:   "remove Redis service and files from target server",
			DeleteInstance: "delete Redis app instance record",
			StepStart:      "Redis delete step %d/%d started: %s",
			StepDone:       "Redis delete step %d/%d completed: %s",
			StepFailed:     "Redis delete step %d/%d failed: %s: %v",
			DeleteFailed:   "Redis deployed service delete failed: %s",
			Deleted:        "Redis deployed service deleted: %s",
		}
	default:
		return DeleteCopy{
			RemoveRemote:   "从目标服务器移除 Redis 服务和文件",
			DeleteInstance: "删除 Redis 应用实例记录",
			StepStart:      "Redis 删除步骤 %d/%d 开始：%s",
			StepDone:       "Redis 删除步骤 %d/%d 完成：%s",
			StepFailed:     "Redis 删除步骤 %d/%d 失败：%s：%v",
			DeleteFailed:   "Redis 部署服务删除失败：%s",
			Deleted:        "Redis 部署服务已删除：%s",
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
