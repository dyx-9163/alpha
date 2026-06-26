package docker

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
			VerifyRemoved:  "verify Docker service removal",
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
			VerifyRemoved:  "校验 Docker 服务已删除",
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
