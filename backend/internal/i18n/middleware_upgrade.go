package i18n

var middlewareUpgradeMessages = map[string][2]string{
	"confirm":     {"确认已暂停受影响业务的写入和定时任务后再升级", "Confirm that application writes and scheduled jobs are paused before upgrading."},
	"path":        {"当前版本和拓扑不支持此升级路径", "This upgrade path is unavailable for the current version and topology."},
	"group":       {"升级组成员或当前状态已变化，刷新后检查版本和拓扑", "Upgrade group membership or state changed. Refresh and check the versions and topology."},
	"resources":   {"缺少目标版本的完整离线介质，请重新扫描资源", "The offline upgrade artifacts are incomplete. Rescan the resources."},
	"unavailable": {"升级模块不可用", "The upgrade module is unavailable."},
	"execute":     {"备份、切换和验证升级组", "Back up, upgrade, and verify the group"},
}

func MiddlewareUpgradeText(language, key string) string {
	index := 0
	if Resolve(language) == En {
		index = 1
	}
	return middlewareUpgradeMessages[key][index]
}
