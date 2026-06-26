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

func normalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(lang, "en") {
		return "en"
	}
	return "zh"
}
