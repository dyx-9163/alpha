package i18n

import "testing"

func TestAIFARRollbackValidationMessagesUseBothLocaleCatalogs(t *testing.T) {
	// Production break caught: missing rollback-validation catalog entries would
	// expose machine keys to users instead of the request language's message.
	for _, test := range []struct {
		language string
		key      string
		args     []any
		want     string
	}{
		{language: "en", key: "aifar.rollback.auditRecord", want: "rollback audit record cannot be selected as a rollback target"},
		{language: "zh-CN", key: "aifar.rollback.auditRecord", want: "回滚审计记录不能作为回滚目标"},
		{language: "en", key: "aifar.rollback.alreadyActive", args: []any{"oauth"}, want: "target release is already active for service oauth"},
		{language: "zh-CN", key: "aifar.rollback.alreadyActive", args: []any{"oauth"}, want: "目标版本已是服务 oauth 的当前版本"},
		{language: "en", key: "aifar.rollback.unavailable", want: "target release is not rollback-capable"},
		{language: "zh-CN", key: "aifar.rollback.unavailable", want: "目标版本不具备可回滚条件"},
	} {
		if got := Text(test.language, test.key, test.args...); got != test.want {
			t.Fatalf("Text(%q, %q) = %q, want %q", test.language, test.key, got, test.want)
		}
	}
}
