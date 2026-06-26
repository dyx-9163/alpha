package minio

import "strings"

type Copy struct {
	CategoryLabel          string
	SourceLabel            string
	Description            string
	UsingArchive           string
	UsingGoToolchain       string
	UsingGoModCache        string
	UsingRPMs              string
	MissingRPMWarning      string
	StepStart              string
	StepDone               string
	StepFailed             string
	LoadServer             string
	VerifyResource         string
	InstallStandalone      string
	ConfigureDistributed   string
	RecordInstance         string
	LoadFailed             string
	InstallFailed          string
	RecordFailed           string
	Installed              string
	DistributedInstalled   string
	TargetRequired         string
	SingleTargetOnly       string
	DistributedNeedNodes   string
	DistributedUnsupported string
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
			CategoryLabel:          "Storage",
			SourceLabel:            "Official source archive",
			Description:            "Build and install MinIO standalone or distributed topology from the offline source archive.",
			UsingArchive:           "using MinIO archive: %s",
			UsingGoToolchain:       "using Go toolchain: %s",
			UsingGoModCache:        "using Go module cache: %s",
			UsingRPMs:              "using %d RPM dependency package(s)",
			MissingRPMWarning:      "MinIO RPM cache is empty; the installer will continue if build dependencies already exist on the target server",
			StepStart:              "MinIO step %d/%d started: %s",
			StepDone:               "MinIO step %d/%d completed: %s",
			StepFailed:             "MinIO step %d/%d failed: %s: %v",
			LoadServer:             "load target server",
			VerifyResource:         "verify MinIO offline resource",
			InstallStandalone:      "build and install MinIO base service",
			ConfigureDistributed:   "configure MinIO distributed topology",
			RecordInstance:         "record MinIO app instance",
			LoadFailed:             "load server failed: %s",
			InstallFailed:          "MinIO install failed: %s",
			RecordFailed:           "record MinIO instance failed: %s",
			Installed:              "MinIO standalone installed, instance recorded: %s",
			DistributedInstalled:   "MinIO distributed topology installed, %d instance record(s) created",
			TargetRequired:         "MinIO install requires target server(s)",
			SingleTargetOnly:       "MinIO standalone install supports only one target server",
			DistributedNeedNodes:   "MinIO distributed topology requires at least 4 target servers",
			DistributedUnsupported: "MinIO topology is not supported: %s",
		}
	default:
		return Copy{
			CategoryLabel:          "对象存储",
			SourceLabel:            "官方源码包",
			Description:            "基于离线源码包安装 MinIO 单体或分布式拓扑。",
			UsingArchive:           "使用 MinIO 源码包：%s",
			UsingGoToolchain:       "使用 Go 工具链：%s",
			UsingGoModCache:        "使用 Go 模块缓存：%s",
			UsingRPMs:              "使用 %d 个 RPM 依赖包",
			MissingRPMWarning:      "MinIO RPM 缓存为空；如果目标服务器已经具备构建依赖，安装会继续执行",
			StepStart:              "MinIO 步骤 %d/%d 开始：%s",
			StepDone:               "MinIO 步骤 %d/%d 完成：%s",
			StepFailed:             "MinIO 步骤 %d/%d 失败：%s：%v",
			LoadServer:             "读取目标服务器",
			VerifyResource:         "校验 MinIO 离线资源",
			InstallStandalone:      "编译并安装 MinIO 基础服务",
			ConfigureDistributed:   "配置 MinIO 分布式拓扑",
			RecordInstance:         "记录 MinIO 应用实例",
			LoadFailed:             "读取服务器失败：%s",
			InstallFailed:          "MinIO 安装失败：%s",
			RecordFailed:           "记录 MinIO 实例失败：%s",
			Installed:              "MinIO 单体已安装，实例已记录：%s",
			DistributedInstalled:   "MinIO 分布式拓扑已安装，已记录 %d 个实例",
			TargetRequired:         "MinIO 安装需要选择目标服务器",
			SingleTargetOnly:       "MinIO 单体安装只支持一个目标服务器",
			DistributedNeedNodes:   "MinIO 分布式拓扑至少需要 4 台目标服务器",
			DistributedUnsupported: "MinIO 不支持该拓扑：%s",
		}
	}
}

func DeleteCopyFor(lang string) DeleteCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return DeleteCopy{
			RemoveRemote:   "remove MinIO service and files from target server",
			DeleteInstance: "delete MinIO app instance record",
			StepStart:      "MinIO delete step %d/%d started: %s",
			StepDone:       "MinIO delete step %d/%d completed: %s",
			StepFailed:     "MinIO delete step %d/%d failed: %s: %v",
			DeleteFailed:   "MinIO deployed service delete failed: %s",
			Deleted:        "MinIO deployed service deleted: %s",
		}
	default:
		return DeleteCopy{
			RemoveRemote:   "从目标服务器移除 MinIO 服务和文件",
			DeleteInstance: "删除 MinIO 应用实例记录",
			StepStart:      "MinIO 删除步骤 %d/%d 开始：%s",
			StepDone:       "MinIO 删除步骤 %d/%d 完成：%s",
			StepFailed:     "MinIO 删除步骤 %d/%d 失败：%s：%v",
			DeleteFailed:   "MinIO 部署服务删除失败：%s",
			Deleted:        "MinIO 部署服务已删除：%s",
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
