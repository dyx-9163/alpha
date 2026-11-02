package i18n

func init() {
	values := map[Locale]map[string]string{
		Zh: {
			"tokenRequired": "请通过 AIFAR_API_TOKEN 环境变量提供面板登录令牌", "httpsRequired": "非本机面板连接必须使用 HTTPS", "requestFailed": "迁移 API 请求失败", "waitExpired": "等待超时；远端任务可能仍在执行，请查询原任务后再决定是否续作",
			"preflight": "检查实例、Agent 能力及迁移输入", "bootstrap": "迁移或识别已切换的 Agent 配置", "readback": "核对服务 generation 与配置摘要", "archive": "归档旧配置", "commit": "提交控制面迁移记录",
			"preflightPassed": "运行时迁移预检通过：服务数=%d", "committed": "运行时迁移已提交：服务数=%d，模型=%s",
			"failed": "运行时迁移失败；修复原因后可使用同一实例的迁移入口继续对账", "notFound": "AIFAR 实例不存在", "invalid": "迁移参数或续作任务无效", "active": "迁移任务仍在执行，请查看现有任务", "unavailable": "应用模块不支持运行时迁移",
		},
		En: {
			"tokenRequired": "Provide the panel session token through AIFAR_API_TOKEN", "httpsRequired": "HTTPS is required for a remote panel", "requestFailed": "Migration API request failed", "waitExpired": "Wait expired; the remote task may still be running. Inspect it before resuming",
			"preflight": "Check instance, Agent capabilities and migration inputs", "bootstrap": "Migrate or detect already switched Agent configuration", "readback": "Verify service generations and configuration hashes", "archive": "Archive legacy configuration", "commit": "Commit control-plane migration records",
			"preflightPassed": "Runtime migration preflight passed: services=%d", "committed": "Runtime migration committed: services=%d model=%s",
			"failed": "Runtime migration failed; correct the cause and use the same instance migration entry to resume reconciliation", "notFound": "AIFAR instance was not found", "invalid": "Invalid migration parameters or resume task", "active": "The migration task is still active; inspect the existing task", "unavailable": "The application module does not support runtime migration",
		},
	}
	for locale, messages := range values {
		for key, value := range messages {
			catalogs[locale]["aifar.migration."+key] = value
		}
	}
}
