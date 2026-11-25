package i18n

var middlewareUpgradeMessages = map[string][2]string{
	"confirm":     {"确认已暂停受影响业务的写入和定时任务后再升级", "Confirm that application writes and scheduled jobs are paused before upgrading."},
	"path":        {"当前版本和拓扑不支持此升级路径", "This upgrade path is unavailable for the current version and topology."},
	"group":       {"升级组成员或当前状态已变化，刷新后检查版本和拓扑", "Upgrade group membership or state changed. Refresh and check the versions and topology."},
	"resources":   {"缺少目标版本的完整离线介质，请重新扫描资源", "The offline upgrade artifacts are incomplete. Rescan the resources."},
	"unavailable": {"升级模块不可用", "The upgrade module is unavailable."},
	"execute":     {"备份、切换和验证升级组", "Back up, upgrade, and verify the group"},
	"mysqlWriteSetHandled": {
		"节点 %s 的预检查报告了旧版 MySQL 的 WRITESET 配置。已从 MySQL 8.4 候选配置移除该参数，并通过配置校验。",
		"The upgrade checker on %s reported the old MySQL WRITESET setting. The MySQL 8.4 candidate configuration omits the setting and passed validation.",
	},
	"mysqlResumeOldPhaseNotIdle": {"无法确认原 MySQL 升级脚本已经退出，保留原任务锁", "Could not confirm that the previous MySQL upgrade command exited. Its operation locks were kept."},
	"mysqlResumeCheckingIdle":    {"正在检查重启前的 MySQL 升级任务 %s 是否仍有远程脚本运行", "Checking for remote commands from MySQL upgrade task %s before taking over its operation locks."},
	"mysqlResumeTakeoverFailed":  {"无法接管重启中断的 MySQL 升级锁，请检查当前任务和升级断点后重试", "Could not take over the interrupted MySQL upgrade locks. Check the current tasks and upgrade checkpoint before retrying."},
	"siloResumeCheckingIdle":     {"正在检查重启前的 MinIO 到 Silo 迁移任务 %s 是否仍有远程脚本运行", "Checking for remote commands from MinIO to Silo migration task %s before taking over its operation locks."},
	"siloResumeTakeoverFailed":   {"无法接管重启中断的 MinIO 到 Silo 迁移锁，请检查当前任务和迁移断点后重试", "Could not take over the interrupted MinIO to Silo migration locks. Check the current tasks and migration checkpoint before retrying."},
	"mysqlCheckpointRead":        {"读取 MySQL 升级断点失败", "Could not read the MySQL upgrade checkpoint."},
	"mysqlCheckpointWrite":       {"保存 MySQL 升级断点或完成状态失败，请保留原冷备", "Could not save the MySQL upgrade checkpoint or final status. Keep the original cold backups."},
	"mysqlCheckpointInvalid":     {"MySQL 升级断点与当前节点、拓扑或离线包不一致，请保留原冷备并检查现场", "The MySQL upgrade checkpoint does not match the current nodes, topology, or offline bundle. Keep the original cold backups and inspect the deployment."},
	"mysqlCheckpointChanged":     {"面板中的 MySQL 升级记录已变化，无法结束断点", "The MySQL upgrade records changed and the checkpoint could not be completed."},
	"mysqlCheckpointResuming":    {"正在核对并续跑原 MySQL 升级任务 %s，将继续使用其冷备目录", "Checking and resuming MySQL upgrade %s using its original cold backups."},
	"mysqlPhaseTimeout":          {"远程升级阶段超时，请检查当前服务状态和原冷备", "The remote upgrade phase reached its deadline. Check the current service state and original cold backups."},
	"mysqlProbeNotListening": {
		"MySQL 节点 %s 暂未接受连接。",
		"MySQL on %s is not accepting connections yet.",
	},
}

func MiddlewareUpgradeText(language, key string) string {
	index := 0
	if Resolve(language) == En {
		index = 1
	}
	return middlewareUpgradeMessages[key][index]
}
