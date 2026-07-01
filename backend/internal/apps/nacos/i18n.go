package nacos

import "strings"

type Copy struct {
	CategoryLabel       string
	SourceLabel         string
	Description         string
	UsingArchive        string
	UsingJDK            string
	StepStart           string
	StepDone            string
	StepFailed          string
	LoadServer          string
	VerifyResource      string
	InstallNacos        string
	RecordInstance      string
	LoadFailed          string
	InstallFailed       string
	RecordFailed        string
	Installed           string
	ClusterInstalled    string
	BatchFailed         string
	TargetRequired      string
	SingleTargetOnly    string
	ClusterNeedNodes    string
	TopologyUnsupported string
	CheckRuntime        string
	UpdateInstance      string
	CheckFailed         string
	Checked             string
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
			CategoryLabel:       "DevOps",
			SourceLabel:         "Offline resources/nacos package",
			Description:         "Install Nacos standalone or three-node cluster mode from the offline Nacos package.",
			UsingArchive:        "using Nacos archive: %s",
			UsingJDK:            "using JDK archive: %s",
			StepStart:           "Nacos step %d/%d started: %s",
			StepDone:            "Nacos step %d/%d completed: %s",
			StepFailed:          "Nacos step %d/%d failed: %s: %v",
			LoadServer:          "load target server",
			VerifyResource:      "verify Nacos offline resources",
			InstallNacos:        "install and configure Nacos service",
			RecordInstance:      "record Nacos app instance",
			LoadFailed:          "load server failed: %s",
			InstallFailed:       "Nacos install failed: %s",
			RecordFailed:        "record Nacos instance failed: %s",
			Installed:           "Nacos instance recorded: %s",
			ClusterInstalled:    "Nacos cluster installed, %d instance record(s) created",
			BatchFailed:         "Nacos install finished with %d failure(s): %s",
			TargetRequired:      "Nacos install requires target server(s)",
			SingleTargetOnly:    "Nacos standalone install supports only one target server",
			ClusterNeedNodes:    "Nacos cluster mode requires exactly 3 target servers",
			TopologyUnsupported: "Nacos topology is not supported: %s",
			CheckRuntime:        "check Nacos runtime",
			UpdateInstance:      "update Nacos instance status",
			CheckFailed:         "Nacos check failed: %s",
			Checked:             "Nacos instance checked: %s",
		}
	default:
		return Copy{
			CategoryLabel:       "DevOps",
			SourceLabel:         "resources/nacos 离线包",
			Description:         "基于 resources/nacos 离线包安装 Nacos，支持单体和 3 节点 Cluster 模式。",
			UsingArchive:        "使用 Nacos 资源包：%s",
			UsingJDK:            "使用 JDK 资源包：%s",
			StepStart:           "Nacos 步骤 %d/%d 开始：%s",
			StepDone:            "Nacos 步骤 %d/%d 完成：%s",
			StepFailed:          "Nacos 步骤 %d/%d 失败：%s：%v",
			LoadServer:          "读取目标服务器",
			VerifyResource:      "校验 Nacos 离线资源",
			InstallNacos:        "安装并配置 Nacos 服务",
			RecordInstance:      "记录 Nacos 应用实例",
			LoadFailed:          "读取服务器失败：%s",
			InstallFailed:       "Nacos 安装失败：%s",
			RecordFailed:        "记录 Nacos 实例失败：%s",
			Installed:           "Nacos 实例已记录：%s",
			ClusterInstalled:    "Nacos 集群已安装，已记录 %d 个实例",
			BatchFailed:         "Nacos 安装完成，但有 %d 个失败：%s",
			TargetRequired:      "Nacos 安装需要选择目标服务器",
			SingleTargetOnly:    "Nacos 单体安装只支持一个目标服务器",
			ClusterNeedNodes:    "Nacos Cluster 模式必须选择 3 台目标服务器",
			TopologyUnsupported: "Nacos 不支持该拓扑：%s",
			CheckRuntime:        "检查 Nacos 运行状态",
			UpdateInstance:      "更新 Nacos 实例状态",
			CheckFailed:         "Nacos 检测失败：%s",
			Checked:             "Nacos 实例检测完成：%s",
		}
	}
}

func DeleteCopyFor(lang string) DeleteCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return DeleteCopy{
			RemoveRemote:   "remove Nacos service and files from target server",
			DeleteInstance: "delete Nacos app instance record",
			StepStart:      "Nacos delete step %d/%d started: %s",
			StepDone:       "Nacos delete step %d/%d completed: %s",
			StepFailed:     "Nacos delete step %d/%d failed: %s: %v",
			DeleteFailed:   "Nacos deployed service delete failed: %s",
			Deleted:        "Nacos deployed service deleted: %s",
		}
	default:
		return DeleteCopy{
			RemoveRemote:   "从目标服务器移除 Nacos 服务和文件",
			DeleteInstance: "删除 Nacos 应用实例记录",
			StepStart:      "Nacos 删除步骤 %d/%d 开始：%s",
			StepDone:       "Nacos 删除步骤 %d/%d 完成：%s",
			StepFailed:     "Nacos 删除步骤 %d/%d 失败：%s：%v",
			DeleteFailed:   "Nacos 部署服务删除失败：%s",
			Deleted:        "Nacos 部署服务已删除：%s",
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
