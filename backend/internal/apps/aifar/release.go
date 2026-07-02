package aifar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

const (
	releaseLayout     = "release-v1"
	releaseKeepCount  = 3
	releasesDirName   = "releases"
	currentLinkName   = "current"
	releaseEnvDirName = "env"
)

func newReleaseID(version string, t time.Time) string {
	t = t.UTC()
	return t.Format("20060102T150405.000000000Z") + "-" + sanitizeReleasePart(version)
}

func sanitizeReleasePart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "release"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		return "release"
	}
	return out
}

func installConfigHash(options InstallOptions) string {
	data, _ := json.Marshal(map[string]any{
		"timezone":                options.Timezone,
		"networkName":             options.NetworkName,
		"appCPUs":                 options.AppCPUs,
		"appMemoryLimit":          options.AppMemoryLimit,
		"gatewayPort":             options.GatewayPort,
		"webPort":                 options.WebPort,
		"nacosWebPort":            options.NacosWebPort,
		"nacosAPIPort":            options.NacosAPIPort,
		"nacosSource":             options.NacosSource,
		"nacosInstanceId":         options.NacosInstanceID,
		"nacosHost":               options.NacosHost,
		"nacosUser":               options.NacosUser,
		"nacosNamespace":          options.NacosNamespace,
		"dbSource":                options.DBSource,
		"dbInstanceId":            options.DBInstanceID,
		"dbHost":                  options.DBHost,
		"dbPort":                  options.DBPort,
		"dbNameNacos":             options.DBNameNacos,
		"dbUser":                  options.DBUser,
		"redisSource":             options.RedisSource,
		"redisInstanceId":         options.RedisInstanceID,
		"redisMode":               options.RedisMode,
		"redisHost":               options.RedisHost,
		"redisPort":               options.RedisPort,
		"redisDatabase":           options.RedisDatabase,
		"redisSentinelMasterName": options.RedisSentinelMasterName,
		"redisSentinelNodes":      options.RedisSentinelNodes,
		"redisClusterNodes":       options.RedisClusterNodes,
		"initSql":                 options.InitSQL,
		"services":                serviceOrder,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
