package docker

import "strings"

type Copy struct {
	Title           string
	CategoryLabel   string
	SourceLabel     string
	Description     string
	InstallAccepted string
	UsingArchive    string
	UsingRPMs       string
	StepStart       string
	StepDone        string
	StepFailed      string
	LoadServer      string
	InstallEngine   string
	UpdateServer    string
	RecordInstance  string
	LoadFailed      string
	InstallFailed   string
	UpdateFailed    string
	RecordFailed    string
	Installed       string
	BatchFailed     string
}

func CopyFor(lang string) Copy {
	switch normalizeLanguage(lang) {
	case "en":
		return Copy{
			Title:           "Docker Engine + Compose",
			CategoryLabel:   "DevOps",
			SourceLabel:     "Official binary bundle",
			Description:     "Install Docker Engine, Docker Compose, daemon remote API, and register Docker hosts.",
			InstallAccepted: "Docker install request accepted",
			UsingArchive:    "using Docker archive: %s",
			UsingRPMs:       "using %d RPM dependency package(s)",
			StepStart:       "[%d/%d] step %d/%d started: %s",
			StepDone:        "[%d/%d] step %d/%d completed: %s",
			StepFailed:      "[%d/%d] step %d/%d failed: %s: %v",
			LoadServer:      "load target server",
			InstallEngine:   "install Docker Engine and Compose",
			UpdateServer:    "update server Docker status",
			RecordInstance:  "record Docker app instance",
			LoadFailed:      "[%d/%d] load server failed: %s",
			InstallFailed:   "[%d/%d] Docker install failed: %s",
			UpdateFailed:    "[%d/%d] server update failed: %s",
			RecordFailed:    "[%d/%d] instance record failed: %s",
			Installed:       "[%d/%d] Docker installed, instance recorded: %s",
			BatchFailed:     "Docker batch install finished with %d failure(s): %s",
		}
	default:
		return Copy{
			Title:           "Docker Engine + Compose",
			CategoryLabel:   "DevOps",
			SourceLabel:     "官方二进制包",
			Description:     "安装 Docker Engine、Docker Compose 和 daemon 远程 API，并登记为 Docker 主机。",
			InstallAccepted: "Docker 安装请求已接收",
			UsingArchive:    "使用 Docker 资源包：%s",
			UsingRPMs:       "使用 %d 个 RPM 依赖包",
			StepStart:       "[%d/%d] 步骤 %d/%d 开始：%s",
			StepDone:        "[%d/%d] 步骤 %d/%d 完成：%s",
			StepFailed:      "[%d/%d] 步骤 %d/%d 失败：%s：%v",
			LoadServer:      "读取目标服务器",
			InstallEngine:   "安装 Docker Engine 和 Compose",
			UpdateServer:    "更新服务器 Docker 状态",
			RecordInstance:  "记录 Docker 应用实例",
			LoadFailed:      "[%d/%d] 读取服务器失败：%s",
			InstallFailed:   "[%d/%d] Docker 安装失败：%s",
			UpdateFailed:    "[%d/%d] 更新服务器状态失败：%s",
			RecordFailed:    "[%d/%d] 记录应用实例失败：%s",
			Installed:       "[%d/%d] Docker 已安装，实例已记录：%s",
			BatchFailed:     "Docker 批量安装完成，但有 %d 台失败：%s",
		}
	}
}

type CheckCopy struct {
	CheckRuntime   string
	UpdateInstance string
	StepStart      string
	StepDone       string
	StepFailed     string
	CheckFailed    string
	Checked        string
}

func CheckCopyFor(lang string) CheckCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return CheckCopy{
			CheckRuntime:   "check Docker runtime status",
			UpdateInstance: "update Docker instance status",
			StepStart:      "check step %d/%d started: %s",
			StepDone:       "check step %d/%d completed: %s",
			StepFailed:     "check step %d/%d failed: %s: %v",
			CheckFailed:    "Docker instance check failed: %s",
			Checked:        "Docker instance status checked: %s",
		}
	default:
		return CheckCopy{
			CheckRuntime:   "检测 Docker 运行状态",
			UpdateInstance: "更新 Docker 实例状态",
			StepStart:      "检测步骤 %d/%d 开始：%s",
			StepDone:       "检测步骤 %d/%d 完成：%s",
			StepFailed:     "检测步骤 %d/%d 失败：%s：%v",
			CheckFailed:    "Docker 实例检测失败：%s",
			Checked:        "Docker 实例状态已检测：%s",
		}
	}
}

type DeleteCopy struct {
	RemoveRemote   string
	VerifyRemoved  string
	UpdateServer   string
	DeleteInstance string
	StepStart      string
	StepDone       string
	StepFailed     string
	DeleteFailed   string
	Deleted        string
}

func DeleteCopyFor(lang string) DeleteCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return DeleteCopy{
			RemoveRemote:   "remove Docker service and files from target server",
			VerifyRemoved:  "verify AIFAR Docker deployment removal",
			UpdateServer:   "clear server Docker status",
			DeleteInstance: "delete Docker app instance record",
			StepStart:      "delete step %d/%d started: %s",
			StepDone:       "delete step %d/%d completed: %s",
			StepFailed:     "delete step %d/%d failed: %s: %v",
			DeleteFailed:   "Docker deployed service delete failed: %s",
			Deleted:        "Docker deployed service deleted: %s",
		}
	default:
		return DeleteCopy{
			RemoveRemote:   "从目标服务器移除 Docker 服务和文件",
			VerifyRemoved:  "校验 AIFAR Docker 部署已删除",
			UpdateServer:   "清理服务器 Docker 状态",
			DeleteInstance: "删除 Docker 应用实例记录",
			StepStart:      "删除步骤 %d/%d 开始：%s",
			StepDone:       "删除步骤 %d/%d 完成：%s",
			StepFailed:     "删除步骤 %d/%d 失败：%s：%v",
			DeleteFailed:   "Docker 部署服务删除失败：%s",
			Deleted:        "Docker 部署服务已删除：%s",
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
