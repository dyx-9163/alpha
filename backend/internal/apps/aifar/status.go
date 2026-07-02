package aifar

import (
	"context"
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type StatusResult struct {
	Status              string
	Message             string
	InstallRoot         string
	CurrentRelease      string
	ReleaseID           string
	InstallRootExists   bool
	TotalContainers     int
	RunningContainers   int
	UnhealthyContainers int
	Containers          []string
}

type Inspector struct {
	remote Remote
}

func NewInspector(remote Remote) Inspector {
	return Inspector{remote: remote}
}

func (i Inspector) Check(ctx context.Context, server store.Server, installRoot string, log Logger) (StatusResult, error) {
	installRoot = strings.TrimSpace(installRoot)
	if installRoot == "" {
		installRoot = installRootFromDeployDir(server.DeployDir)
	}
	result, err := i.remote.Run(ctx, server, statusCommand(installRoot))
	installerkit.LogCommandResult(result, err, log)
	if err != nil {
		return StatusResult{Status: "error", InstallRoot: installRoot, Message: err.Error()}, fmt.Errorf("AIFAR service status check failed: %w", err)
	}
	status := parseStatusOutput(result.Stdout)
	status.InstallRoot = installRoot
	if status.Status == "" {
		status.Status = "unknown"
	}
	return status, nil
}

func statusCommand(installRoot string) string {
	return "sh -s <<'AIFAR_SERVICE_STATUS'\n" + `#!/usr/bin/env sh
set -u

INSTALL_ROOT=` + installerkit.ShellQuote(installRoot) + `
CURRENT_LINK="$INSTALL_ROOT/` + currentLinkName + `"
CURRENT_RELEASE=""
RELEASE_ID=""
APP_DIR="$INSTALL_ROOT/` + appBundleDir + `"
ENV_DIR=""
STATUS="missing"
INSTALL_ROOT_EXISTS="false"
TOTAL=0
RUNNING=0
UNHEALTHY=0
CONTAINERS=""

if [ -d "$INSTALL_ROOT" ]; then
  INSTALL_ROOT_EXISTS="true"
  STATUS="stopped"
fi

if [ -L "$CURRENT_LINK" ] || [ -d "$CURRENT_LINK" ]; then
  CURRENT_RELEASE="$(readlink -f "$CURRENT_LINK" 2>/dev/null || printf "%s" "$CURRENT_LINK")"
  APP_DIR="$CURRENT_LINK/` + appBundleDir + `"
  ENV_DIR="$CURRENT_LINK/` + releaseEnvDirName + `"
  if [ -f "$CURRENT_LINK/.aifar/manifest.json" ]; then
    RELEASE_ID="$(awk -F\" '/"releaseId"[[:space:]]*:/ {print $4; exit}' "$CURRENT_LINK/.aifar/manifest.json" 2>/dev/null || true)"
  fi
fi

if command -v docker >/dev/null 2>&1 && [ -d "$APP_DIR" ]; then
  for service in ` + serviceOrderText() + `; do
    env_file="$ENV_DIR/$service.env"
    [ -f "$env_file" ] || env_file="$APP_DIR/$service/.env"
    [ -f "$env_file" ] || continue
    name="$(awk -F= '$1=="APP_CONTAINER_NAME"{print $2}' "$env_file" | tail -n 1)"
    [ -n "$name" ] || continue
    TOTAL=$((TOTAL + 1))
    running="$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || echo false)"
    health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$name" 2>/dev/null || true)"
    CONTAINERS="${CONTAINERS}${name}:${running}:${health},"
    if [ "$running" = "true" ]; then
      RUNNING=$((RUNNING + 1))
    fi
    if [ "$health" = "unhealthy" ]; then
      UNHEALTHY=$((UNHEALTHY + 1))
    fi
  done
  if [ "$TOTAL" -gt 0 ] && [ "$RUNNING" -eq "$TOTAL" ] && [ "$UNHEALTHY" -eq 0 ]; then
    STATUS="running"
  elif [ "$RUNNING" -gt 0 ]; then
    STATUS="degraded"
  fi
fi

echo "status=$STATUS"
echo "installRootExists=$INSTALL_ROOT_EXISTS"
echo "currentRelease=$CURRENT_RELEASE"
echo "releaseId=$RELEASE_ID"
echo "totalContainers=$TOTAL"
echo "runningContainers=$RUNNING"
echo "unhealthyContainers=$UNHEALTHY"
echo "containers=$CONTAINERS"
` + "\nAIFAR_SERVICE_STATUS"
}

func parseStatusOutput(output string) StatusResult {
	result := StatusResult{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "status":
			result.Status = strings.TrimSpace(value)
		case "installRootExists":
			result.InstallRootExists = strings.EqualFold(strings.TrimSpace(value), "true")
		case "currentRelease":
			result.CurrentRelease = strings.TrimSpace(value)
		case "releaseId":
			result.ReleaseID = strings.TrimSpace(value)
		case "totalContainers":
			result.TotalContainers = atoi(value)
		case "runningContainers":
			result.RunningContainers = atoi(value)
		case "unhealthyContainers":
			result.UnhealthyContainers = atoi(value)
		case "containers":
			result.Containers = parseContainers(value)
		}
	}
	switch result.Status {
	case "running":
		result.Message = "AIFAR service containers are running"
	case "degraded":
		result.Message = "AIFAR service is degraded; one or more containers are not running or healthy"
	case "stopped":
		result.Message = "AIFAR service files exist, but containers are not running"
	case "missing":
		result.Message = "AIFAR service deployment is not present on target server"
	case "error":
		if result.Message == "" {
			result.Message = "AIFAR service status check failed"
		}
	default:
		result.Message = "AIFAR service status is unknown"
	}
	return result
}

func atoi(value string) int {
	n := 0
	for _, ch := range strings.TrimSpace(value) {
		if ch < '0' || ch > '9' {
			return n
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func parseContainers(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

type Copy struct {
	Title                 string
	CategoryLabel         string
	SourceLabel           string
	Description           string
	TopologySingle        string
	DockerRuntimeWarning  string
	DockerRuntimeRequired string
	StepStart             string
	StepDone              string
	StepFailed            string
	LoadServer            string
	VerifyResource        string
	UploadBundleStep      string
	DeployCompose         string
	RecordInstance        string
	TargetRequired        string
	SingleTargetOnly      string
	TopologyUnsupported   string
	PrepareWorkDir        string
	UploadBundle          string
	UploadBundleFailed    string
	UploadScript          string
	UploadScriptFailed    string
	Deploying             string
	RemoteCommandFailed   string
	InstallFailed         string
	RecordFailed          string
	Installed             string
}

type DeleteCopy struct {
	RemoveRemote                 string
	VerifyRemoved                string
	DeleteInstance               string
	StepStart                    string
	StepDone                     string
	StepFailed                   string
	RemoteCommandFailed          string
	DeleteFailed                 string
	Deleted                      string
	PasswordConfirmationRequired string
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

type UpdateCopy struct {
	ValidateRequest        string
	UploadArtifactStep     string
	ApplyUpdate            string
	RecordRelease          string
	StepStart              string
	StepDone               string
	StepFailed             string
	TargetRequired         string
	UnsupportedInstance    string
	UnsupportedService     string
	ArtifactRequired       string
	ArtifactTypeInvalid    string
	BundleRequired         string
	BundleManifestRequired string
	BundleEmpty            string
	BundleInvalid          string
	BundleDuplicateService string
	BundleArtifactMissing  string
	PrepareWorkDir         string
	UploadArtifact         string
	UploadArtifactFailed   string
	UploadScript           string
	UploadScriptFailed     string
	Deploying              string
	RemoteCommandFailed    string
	UpdateFailed           string
	RecordFailed           string
	Updated                string
	BundleUpdating         string
	BundleServiceUpdating  string
	BundleUpdated          string
}

func copyFor(lang string) Copy {
	if normalizeLanguage(lang) == "en" {
		return Copy{
			Title:                 "AIFAR Service",
			CategoryLabel:         "Application",
			SourceLabel:           "Docker Compose bundle",
			Description:           "Deploy AIFAR microservices from the offline Docker Compose application bundle.",
			TopologySingle:        "Single server",
			DockerRuntimeWarning:  "Target server must already have Docker Engine and Docker Compose available. SQL import requires mysql client on the target server.",
			DockerRuntimeRequired: "Target server must have a healthy Docker Engine + Docker Compose deployment before installing AIFAR service",
			StepStart:             "AIFAR service step %d/%d started: %s",
			StepDone:              "AIFAR service step %d/%d completed: %s",
			StepFailed:            "AIFAR service step %d/%d failed: %s: %v",
			LoadServer:            "load target server",
			VerifyResource:        "verify AIFAR offline bundle",
			UploadBundleStep:      "upload AIFAR service bundle",
			DeployCompose:         "deploy Docker Compose services",
			RecordInstance:        "record AIFAR service instance",
			TargetRequired:        "AIFAR service deployment requires one target server",
			SingleTargetOnly:      "AIFAR service deployment supports only one target server per task",
			TopologyUnsupported:   "AIFAR service topology is not supported: %s",
			PrepareWorkDir:        "preparing remote work directory: %s",
			UploadBundle:          "uploading AIFAR service bundle: %s",
			UploadBundleFailed:    "upload AIFAR service bundle",
			UploadScript:          "uploading AIFAR service install script",
			UploadScriptFailed:    "upload AIFAR service install script",
			Deploying:             "deploying AIFAR Docker Compose services",
			RemoteCommandFailed:   "AIFAR remote command failed",
			InstallFailed:         "AIFAR service install failed: %v",
			RecordFailed:          "record AIFAR service instance failed: %v",
			Installed:             "AIFAR service installed, instance recorded: %s",
		}
	}
	return Copy{
		Title:                 "AIFAR 服务",
		CategoryLabel:         "应用服务",
		SourceLabel:           "Docker Compose 离线包",
		Description:           "基于离线 Docker Compose 应用包部署 AIFAR 微服务。",
		TopologySingle:        "单服务器",
		DockerRuntimeWarning:  "目标服务器需要先具备 Docker Engine 和 Docker Compose；勾选 SQL 初始化时目标服务器还需要 mysql 客户端。",
		DockerRuntimeRequired: "安装 AIFAR 服务前，目标服务器必须已有健康的 Docker Engine 和 Docker Compose 部署",
		StepStart:             "AIFAR 服务步骤 %d/%d 开始：%s",
		StepDone:              "AIFAR 服务步骤 %d/%d 完成：%s",
		StepFailed:            "AIFAR 服务步骤 %d/%d 失败：%s：%v",
		LoadServer:            "读取目标服务器",
		VerifyResource:        "校验 AIFAR 离线应用包",
		UploadBundleStep:      "上传 AIFAR 服务包",
		DeployCompose:         "部署 Docker Compose 服务",
		RecordInstance:        "记录 AIFAR 服务实例",
		TargetRequired:        "AIFAR 服务部署需要选择一台目标服务器",
		SingleTargetOnly:      "AIFAR 服务每次部署任务只支持一台目标服务器",
		TopologyUnsupported:   "AIFAR 服务不支持该拓扑：%s",
		PrepareWorkDir:        "准备远程工作目录：%s",
		UploadBundle:          "上传 AIFAR 服务包：%s",
		UploadBundleFailed:    "上传 AIFAR 服务包失败",
		UploadScript:          "上传 AIFAR 服务安装脚本",
		UploadScriptFailed:    "上传 AIFAR 服务安装脚本失败",
		Deploying:             "正在部署 AIFAR Docker Compose 服务",
		RemoteCommandFailed:   "AIFAR 远程命令执行失败",
		InstallFailed:         "AIFAR 服务安装失败：%v",
		RecordFailed:          "记录 AIFAR 服务实例失败：%v",
		Installed:             "AIFAR 服务已安装，实例已记录：%s",
	}
}

func updateCopyFor(lang string) UpdateCopy {
	if normalizeLanguage(lang) == "en" {
		return UpdateCopy{
			ValidateRequest:        "validate AIFAR service artifact",
			UploadArtifactStep:     "upload service artifact",
			ApplyUpdate:            "create partial release and update service",
			RecordRelease:          "record partial release",
			StepStart:              "AIFAR update step %d/%d started: %s",
			StepDone:               "AIFAR update step %d/%d completed: %s",
			StepFailed:             "AIFAR update step %d/%d failed: %s: %v",
			TargetRequired:         "AIFAR service update requires a target server",
			UnsupportedInstance:    "only AIFAR service instances support artifact updates",
			UnsupportedService:     "unsupported AIFAR service: %s",
			ArtifactRequired:       "service artifact file is required",
			ArtifactTypeInvalid:    "artifact type is invalid for %s",
			BundleRequired:         "artifact bundle zip file is required",
			BundleManifestRequired: "artifact bundle manifest.json is required",
			BundleEmpty:            "artifact bundle does not contain any service artifact",
			BundleInvalid:          "artifact bundle is invalid: %v",
			BundleDuplicateService: "duplicate service in artifact bundle: %s",
			BundleArtifactMissing:  "artifact file is missing from bundle: %s",
			PrepareWorkDir:         "preparing remote update work directory: %s",
			UploadArtifact:         "uploading %s artifact: %s",
			UploadArtifactFailed:   "upload AIFAR service artifact",
			UploadScript:           "uploading AIFAR service update script",
			UploadScriptFailed:     "upload AIFAR service update script",
			Deploying:              "updating AIFAR service %s as a partial release",
			RemoteCommandFailed:    "AIFAR partial update remote command failed",
			UpdateFailed:           "AIFAR service update failed: %v",
			RecordFailed:           "record AIFAR partial release failed: %v",
			Updated:                "AIFAR service updated, release recorded: %s",
			BundleUpdating:         "updating %d AIFAR service artifact(s) from bundle",
			BundleServiceUpdating:  "bundle update service %d/%d: %s",
			BundleUpdated:          "AIFAR artifact bundle update completed on %s, services: %d",
		}
	}
	return UpdateCopy{
		ValidateRequest:        "校验 AIFAR 服务制品",
		UploadArtifactStep:     "上传服务制品",
		ApplyUpdate:            "创建部分更新版本并更新服务",
		RecordRelease:          "记录部分更新版本",
		StepStart:              "AIFAR 更新步骤 %d/%d 开始：%s",
		StepDone:               "AIFAR 更新步骤 %d/%d 完成：%s",
		StepFailed:             "AIFAR 更新步骤 %d/%d 失败：%s：%v",
		TargetRequired:         "AIFAR 服务更新需要目标服务器",
		UnsupportedInstance:    "只有 AIFAR 服务实例支持制品更新",
		UnsupportedService:     "不支持的 AIFAR 服务：%s",
		ArtifactRequired:       "请选择服务制品文件",
		ArtifactTypeInvalid:    "%s 的制品类型不正确",
		BundleRequired:         "请选择 AIFAR 制品批量包 zip 文件",
		BundleManifestRequired: "AIFAR 制品批量包缺少 manifest.json",
		BundleEmpty:            "AIFAR 制品批量包没有包含任何服务制品",
		BundleInvalid:          "AIFAR 制品批量包无效：%v",
		BundleDuplicateService: "AIFAR 制品批量包中服务重复：%s",
		BundleArtifactMissing:  "AIFAR 制品批量包中缺少制品文件：%s",
		PrepareWorkDir:         "准备远程更新工作目录：%s",
		UploadArtifact:         "上传 %s 制品：%s",
		UploadArtifactFailed:   "上传 AIFAR 服务制品失败",
		UploadScript:           "上传 AIFAR 服务更新脚本",
		UploadScriptFailed:     "上传 AIFAR 服务更新脚本失败",
		Deploying:              "正在以部分版本更新 AIFAR 服务 %s",
		RemoteCommandFailed:    "AIFAR 部分更新远程命令执行失败",
		UpdateFailed:           "AIFAR 服务更新失败：%v",
		RecordFailed:           "记录 AIFAR 部分更新版本失败：%v",
		Updated:                "AIFAR 服务已更新，版本已记录：%s",
		BundleUpdating:         "正在从批量包更新 %d 个 AIFAR 服务制品",
		BundleServiceUpdating:  "批量更新服务 %d/%d：%s",
		BundleUpdated:          "AIFAR 批量制品更新完成，目标：%s，服务数：%d",
	}
}

func deleteCopyFor(lang string) DeleteCopy {
	if normalizeLanguage(lang) == "en" {
		return DeleteCopy{
			RemoveRemote:                 "remove AIFAR Docker Compose services and files",
			VerifyRemoved:                "verify AIFAR service removal",
			DeleteInstance:               "delete AIFAR service instance record",
			StepStart:                    "AIFAR delete step %d/%d started: %s",
			StepDone:                     "AIFAR delete step %d/%d completed: %s",
			StepFailed:                   "AIFAR delete step %d/%d failed: %s: %v",
			RemoteCommandFailed:          "AIFAR remote uninstall failed",
			DeleteFailed:                 "AIFAR service delete failed: %v",
			Deleted:                      "AIFAR service deleted: %s",
			PasswordConfirmationRequired: "deleting AIFAR service requires server password confirmation",
		}
	}
	return DeleteCopy{
		RemoveRemote:                 "删除目标服务器上的 AIFAR Compose 服务和文件",
		VerifyRemoved:                "校验 AIFAR 服务已删除",
		DeleteInstance:               "删除 AIFAR 服务实例记录",
		StepStart:                    "AIFAR 删除步骤 %d/%d 开始：%s",
		StepDone:                     "AIFAR 删除步骤 %d/%d 完成：%s",
		StepFailed:                   "AIFAR 删除步骤 %d/%d 失败：%s：%v",
		RemoteCommandFailed:          "AIFAR 远程卸载失败",
		DeleteFailed:                 "AIFAR 服务删除失败：%v",
		Deleted:                      "AIFAR 服务已删除：%s",
		PasswordConfirmationRequired: "删除 AIFAR 服务需要先通过服务器密码确认",
	}
}

func checkCopyFor(lang string) CheckCopy {
	if normalizeLanguage(lang) == "en" {
		return CheckCopy{
			CheckRuntime:   "check AIFAR service containers",
			UpdateInstance: "update AIFAR service instance status",
			StepStart:      "AIFAR check step %d/%d started: %s",
			StepDone:       "AIFAR check step %d/%d completed: %s",
			StepFailed:     "AIFAR check step %d/%d failed: %s: %v",
			CheckFailed:    "AIFAR service check failed: %v",
			Checked:        "AIFAR service status checked: %s",
		}
	}
	return CheckCopy{
		CheckRuntime:   "检测 AIFAR 服务容器",
		UpdateInstance: "更新 AIFAR 服务实例状态",
		StepStart:      "AIFAR 检测步骤 %d/%d 开始：%s",
		StepDone:       "AIFAR 检测步骤 %d/%d 完成：%s",
		StepFailed:     "AIFAR 检测步骤 %d/%d 失败：%s：%v",
		CheckFailed:    "AIFAR 服务检测失败：%v",
		Checked:        "AIFAR 服务状态已检测：%s",
	}
}

func normalizeLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(lang, "en") {
		return "en"
	}
	return "zh"
}
