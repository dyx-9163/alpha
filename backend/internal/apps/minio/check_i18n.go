package minio

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
			CheckRuntime:   "check MinIO health endpoint",
			UpdateInstance: "update MinIO instance status",
			StepStart:      "MinIO check step %d/%d started: %s",
			StepDone:       "MinIO check step %d/%d completed: %s",
			StepFailed:     "MinIO check step %d/%d failed: %s: %v",
			CheckFailed:    "MinIO instance check failed: %s",
			Checked:        "MinIO instance status checked: %s",
		}
	default:
		return CheckCopy{
			CheckRuntime:   "检测 MinIO 健康端点",
			UpdateInstance: "更新 MinIO 实例状态",
			StepStart:      "MinIO 检测步骤 %d/%d 开始：%s",
			StepDone:       "MinIO 检测步骤 %d/%d 完成：%s",
			StepFailed:     "MinIO 检测步骤 %d/%d 失败：%s：%v",
			CheckFailed:    "MinIO 实例检测失败：%s",
			Checked:        "MinIO 实例状态已检测：%s",
		}
	}
}
