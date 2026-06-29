package mysql

type CheckCopy struct {
	StepStart      string
	StepDone       string
	StepFailed     string
	CheckRuntime   string
	DetectPrimary  string
	UpdateInstance string
	CheckFailed    string
	Checked        string
}

func CheckCopyFor(lang string) CheckCopy {
	switch normalizeLanguage(lang) {
	case "en":
		return CheckCopy{
			StepStart:      "MySQL check step %d/%d started: %s",
			StepDone:       "MySQL check step %d/%d completed: %s",
			StepFailed:     "MySQL check step %d/%d failed: %s: %v",
			CheckRuntime:   "check MySQL service runtime",
			DetectPrimary:  "detect InnoDB Cluster primary",
			UpdateInstance: "update MySQL instance status",
			CheckFailed:    "MySQL check failed: %s",
			Checked:        "MySQL check completed, status=%s",
		}
	default:
		return CheckCopy{
			StepStart:      "MySQL 检测步骤 %d/%d 开始：%s",
			StepDone:       "MySQL 检测步骤 %d/%d 完成：%s",
			StepFailed:     "MySQL 检测步骤 %d/%d 失败：%s：%v",
			CheckRuntime:   "检测 MySQL 服务运行状态",
			DetectPrimary:  "识别 InnoDB Cluster 当前 Primary",
			UpdateInstance: "更新 MySQL 实例状态",
			CheckFailed:    "MySQL 检测失败：%s",
			Checked:        "MySQL 检测完成，状态=%s",
		}
	}
}
