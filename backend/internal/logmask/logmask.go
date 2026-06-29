package logmask

import "regexp"

const sensitiveKeyPattern = `password|passwd|pwd|token|secret|secretkey|accesskey|privatekey|authorization|credential|serverpassword|rootpassword|mysqlpassword|redispassword|miniopassword`

var (
	privateKeyBlockPattern = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
	authHeaderPattern      = regexp.MustCompile(`(?i)\bAuthorization\s*:\s*(?:Bearer\s+)?[^\s,;]+`)
	bearerTokenPattern     = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	urlSecretPattern       = regexp.MustCompile(`(?i)([?&](?:` + sensitiveKeyPattern + `)=)[^&\s]+`)
	jsonSecretPattern      = regexp.MustCompile(`(?i)(["'](?:` + sensitiveKeyPattern + `)["']\s*:\s*["'])[^"']*(["'])`)
	keyValueSecretPattern  = regexp.MustCompile(`(?i)\b(` + sensitiveKeyPattern + `)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
)

func Mask(text string) string {
	if text == "" {
		return ""
	}
	out := privateKeyBlockPattern.ReplaceAllString(text, "[REDACTED_PRIVATE_KEY]")
	out = authHeaderPattern.ReplaceAllString(out, "Authorization: [REDACTED]")
	out = bearerTokenPattern.ReplaceAllString(out, "Bearer [REDACTED]")
	out = urlSecretPattern.ReplaceAllString(out, "${1}[REDACTED]")
	out = jsonSecretPattern.ReplaceAllString(out, "${1}[REDACTED]${2}")
	out = keyValueSecretPattern.ReplaceAllString(out, "${1}${2}[REDACTED]")
	return out
}
