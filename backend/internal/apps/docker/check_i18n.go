package docker

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
