package aifar

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/store"
)

const (
	AppName              = "aifar"
	resourceApp          = "aifar"
	appBundleVersion     = "docker-apps"
	appBundleDir         = "docker-apps"
	sqlBundleDir         = "docker-sql"
	installDirName       = "admin"
	defaultTopology      = "single"
	defaultNetworkName   = "aifar-network"
	defaultTimezone      = "system"
	defaultDBNameNacos   = "aifar_nacos"
	defaultNacosUser     = "nacos"
	defaultNacosPassword = "oversea.nacos"
	defaultNacosNS       = "prod"
	defaultAppCPUs       = "2.0"
	defaultMemoryLimit   = "2GB"
	defaultGatewayPort   = 38000
	defaultWebPort       = 8080
	defaultNacosWebPort  = 8848
	defaultNacosAPIPort  = 9848
	defaultDBPort        = 3306
	defaultRedisHost     = "localhost"
	defaultRedisPort     = 6379
	defaultRedisDatabase = 1
	defaultMinioPlatform = "minio-1"
	defaultMinioBucket   = "aifar"
	defaultMinioAPIPort  = 9000
	dependencyManual     = "manual"
	dependencyExisting   = "existing"
	redisModeStandalone  = "standalone"
	redisModeSentinel    = "sentinel"
	redisModeCluster     = "cluster"
)

var serviceOrder = []string{
	"oauth",
	"permission",
	"system",
	"file",
	"message",
	"im",
	"contacts",
	"meeting",
	"gateway",
	"web-vue3",
}

var reverseServiceOrder = []string{
	"web-vue3",
	"gateway",
	"meeting",
	"contacts",
	"im",
	"message",
	"file",
	"system",
	"permission",
	"oauth",
}

type InstallOptions struct {
	Timezone                string
	NetworkName             string
	AppCPUs                 string
	AppMemoryLimit          string
	GatewayPort             int
	WebPort                 int
	NacosWebPort            int
	NacosAPIPort            int
	NacosSource             string
	NacosInstanceID         string
	NacosHost               string
	NacosUser               string
	NacosPassword           string
	NacosNamespace          string
	DBHost                  string
	DBPort                  int
	DBNameNacos             string
	DBUser                  string
	DBPassword              string
	RedisMode               string
	RedisHost               string
	RedisPort               int
	RedisPassword           string
	RedisDatabase           int
	RedisSentinelMasterName string
	RedisSentinelNodes      []string
	RedisClusterNodes       []string
	MinioEnableStorage      bool
	MinioPlatform           string
	MinioEndpoint           string
	MinioAccessKey          string
	MinioSecretKey          string
	MinioBucketName         string
	MinioDomain             string
	MinioBasePath           string
	InitSQL                 bool
}

type Bundle struct {
	Version string
	Root    string
	AppDir  string
	SQLDir  string
}

func optionsFromParameters(parameters map[string]any) InstallOptions {
	opts := InstallOptions{
		Timezone:           defaultTimezone,
		NetworkName:        defaultNetworkName,
		AppCPUs:            defaultAppCPUs,
		AppMemoryLimit:     defaultMemoryLimit,
		GatewayPort:        defaultGatewayPort,
		WebPort:            defaultWebPort,
		NacosWebPort:       defaultNacosWebPort,
		NacosAPIPort:       defaultNacosAPIPort,
		NacosSource:        dependencyManual,
		NacosUser:          defaultNacosUser,
		NacosPassword:      defaultNacosPassword,
		NacosNamespace:     defaultNacosNS,
		DBPort:             defaultDBPort,
		DBNameNacos:        defaultDBNameNacos,
		DBUser:             "root",
		RedisMode:          redisModeStandalone,
		RedisHost:          defaultRedisHost,
		RedisPort:          defaultRedisPort,
		RedisDatabase:      defaultRedisDatabase,
		MinioEnableStorage: true,
		MinioPlatform:      defaultMinioPlatform,
		MinioBucketName:    defaultMinioBucket,
	}
	opts.Timezone = stringParam(parameters, "timezone", opts.Timezone)
	opts.NetworkName = stringParam(parameters, "networkName", opts.NetworkName)
	opts.AppCPUs = stringParam(parameters, "appCPUs", opts.AppCPUs)
	opts.AppMemoryLimit = stringParam(parameters, "appMemoryLimit", opts.AppMemoryLimit)
	opts.GatewayPort = intParam(parameters, "gatewayPort", opts.GatewayPort)
	opts.WebPort = intParam(parameters, "webPort", opts.WebPort)
	opts.NacosWebPort = intParam(parameters, "nacosWebPort", opts.NacosWebPort)
	opts.NacosWebPort = intParam(parameters, "nacosPort", opts.NacosWebPort)
	opts.NacosAPIPort = intParam(parameters, "nacosApiPort", opts.NacosAPIPort)
	opts.NacosSource = normalizeDependencySource(stringParam(parameters, "nacosSource", opts.NacosSource))
	opts.NacosInstanceID = stringParam(parameters, "nacosInstanceId", opts.NacosInstanceID)
	opts.NacosHost = stringParam(parameters, "nacosHost", opts.NacosHost)
	opts.NacosUser = stringParam(parameters, "nacosUser", opts.NacosUser)
	opts.NacosPassword = stringParam(parameters, "nacosPassword", opts.NacosPassword)
	opts.NacosNamespace = stringParam(parameters, "nacosNamespace", opts.NacosNamespace)
	opts.DBHost = stringParam(parameters, "dbHost", opts.DBHost)
	opts.DBPort = intParam(parameters, "dbPort", opts.DBPort)
	opts.DBNameNacos = stringParam(parameters, "dbNameNacos", opts.DBNameNacos)
	opts.DBUser = stringParam(parameters, "dbUser", opts.DBUser)
	opts.DBPassword = stringParam(parameters, "dbPassword", opts.DBPassword)
	opts.RedisMode = normalizeRedisMode(stringParam(parameters, "redisMode", opts.RedisMode))
	opts.RedisHost = stringParam(parameters, "redisHost", opts.RedisHost)
	opts.RedisPort = intParam(parameters, "redisPort", opts.RedisPort)
	opts.RedisPassword = stringParam(parameters, "redisPassword", opts.RedisPassword)
	opts.RedisDatabase = intParam(parameters, "redisDatabase", opts.RedisDatabase)
	opts.RedisSentinelMasterName = stringParam(parameters, "redisSentinelMasterName", opts.RedisSentinelMasterName)
	opts.RedisSentinelNodes = stringListParam(parameters, "redisSentinelNodes", opts.RedisSentinelNodes)
	opts.RedisClusterNodes = stringListParam(parameters, "redisClusterNodes", opts.RedisClusterNodes)
	opts.MinioEnableStorage = boolParam(parameters, "minioEnableStorage", opts.MinioEnableStorage)
	opts.MinioPlatform = stringParam(parameters, "minioPlatform", opts.MinioPlatform)
	opts.MinioEndpoint = stringParam(parameters, "minioEndpoint", opts.MinioEndpoint)
	opts.MinioAccessKey = stringParam(parameters, "minioAccessKey", opts.MinioAccessKey)
	opts.MinioSecretKey = stringParam(parameters, "minioSecretKey", opts.MinioSecretKey)
	opts.MinioBucketName = stringParam(parameters, "minioBucketName", opts.MinioBucketName)
	opts.MinioDomain = stringParam(parameters, "minioDomain", opts.MinioDomain)
	opts.MinioBasePath = stringParam(parameters, "minioBasePath", opts.MinioBasePath)
	if strings.TrimSpace(opts.MinioDomain) == "" {
		opts.MinioDomain = deriveMinioDomain(opts.MinioEndpoint, opts.MinioBucketName)
	}
	opts.InitSQL = boolParam(parameters, "initSql", false)
	return opts
}

func (o InstallOptions) Validate() error {
	if strings.TrimSpace(o.DBHost) == "" {
		return fmt.Errorf("database host is required")
	}
	if o.InitSQL {
		if strings.TrimSpace(o.DBUser) == "" {
			return fmt.Errorf("database user is required")
		}
		if strings.TrimSpace(o.DBPassword) == "" {
			return fmt.Errorf("database password is required")
		}
		if strings.TrimSpace(o.DBNameNacos) == "" {
			return fmt.Errorf("nacos database name is required")
		}
	}
	if o.NacosSource == dependencyExisting && strings.TrimSpace(o.NacosInstanceID) == "" {
		return fmt.Errorf("nacos instance is required")
	}
	if strings.TrimSpace(o.NacosHost) == "" {
		return fmt.Errorf("nacos host is required")
	}
	if strings.TrimSpace(o.RedisHost) == "" {
		return fmt.Errorf("redis host is required")
	}
	if o.MinioEnableStorage {
		if strings.TrimSpace(o.MinioEndpoint) == "" {
			return fmt.Errorf("minio endpoint is required")
		}
	}
	if !validPort(o.DBPort) || !validPort(o.RedisPort) || !validPort(o.GatewayPort) || !validPort(o.WebPort) || !validPort(o.NacosWebPort) || !validPort(o.NacosAPIPort) {
		return fmt.Errorf("ports must be between 1 and 65535")
	}
	if o.RedisDatabase < 0 || o.RedisDatabase > 15 {
		return fmt.Errorf("redis database must be between 0 and 15")
	}
	switch normalizeRedisMode(o.RedisMode) {
	case redisModeStandalone:
	case redisModeSentinel:
		if strings.TrimSpace(o.RedisSentinelMasterName) == "" {
			return fmt.Errorf("redis sentinel master name is required")
		}
		if len(o.RedisSentinelNodes) == 0 {
			return fmt.Errorf("redis sentinel nodes are required")
		}
	case redisModeCluster:
		if len(o.RedisClusterNodes) == 0 {
			return fmt.Errorf("redis cluster nodes are required")
		}
	default:
		return fmt.Errorf("unsupported redis mode: %s", o.RedisMode)
	}
	for name, value := range map[string]string{
		"timezone":        o.Timezone,
		"networkName":     o.NetworkName,
		"appCPUs":         o.AppCPUs,
		"appMemoryLimit":  o.AppMemoryLimit,
		"nacosHost":       o.NacosHost,
		"nacosUser":       o.NacosUser,
		"nacosPassword":   o.NacosPassword,
		"nacosNamespace":  o.NacosNamespace,
		"dbHost":          o.DBHost,
		"redisHost":       o.RedisHost,
		"redisMode":       o.RedisMode,
		"minioPlatform":   o.MinioPlatform,
		"minioEndpoint":   o.MinioEndpoint,
		"minioAccessKey":  o.MinioAccessKey,
		"minioSecretKey":  o.MinioSecretKey,
		"minioBucketName": o.MinioBucketName,
		"minioDomain":     o.MinioDomain,
		"minioBasePath":   o.MinioBasePath,
	} {
		if strings.TrimSpace(value) == "" && !strings.HasPrefix(name, "minio") {
			return fmt.Errorf("%s is required", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s must not contain newlines", name)
		}
	}
	for name, value := range map[string]string{
		"redisSentinelMasterName": o.RedisSentinelMasterName,
	} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s must not contain newlines", name)
		}
	}
	return nil
}

func (o InstallOptions) RedisSentinelNodesCSV() string {
	return strings.Join(o.RedisSentinelNodes, ",")
}

func (o InstallOptions) RedisClusterNodesCSV() string {
	return strings.Join(o.RedisClusterNodes, ",")
}

func deriveMinioDomain(endpoint, bucket string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	bucket = strings.Trim(strings.TrimSpace(bucket), "/")
	if endpoint == "" || bucket == "" {
		return endpoint
	}
	return endpoint + "/" + bucket + "/"
}

func normalizeDependencySource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case dependencyExisting:
		return dependencyExisting
	default:
		return dependencyManual
	}
}

func normalizeRedisMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case redisModeSentinel:
		return redisModeSentinel
	case redisModeCluster:
		return redisModeCluster
	default:
		return redisModeStandalone
	}
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "latest" {
		version = appBundleVersion
	}
	if version != appBundleVersion {
		return Bundle{}, fmt.Errorf("AIFAR service resource version must be %s, got %s", appBundleVersion, version)
	}
	var candidates []store.Resource
	for _, res := range resources {
		if res.App != resourceApp || res.Part != "backend" || res.Version != appBundleVersion {
			continue
		}
		appDir := inferAppDir(res.Path)
		if appDir == "" {
			continue
		}
		candidates = append(candidates, store.Resource{Version: res.Version, Path: appDir})
	}
	if len(candidates) == 0 {
		return Bundle{}, fmt.Errorf("AIFAR Docker Compose bundle was not found under resources/aifar/%s", appBundleVersion)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	appDir := candidates[0].Path
	root := filepath.Dir(appDir)
	return Bundle{
		Version: appBundleVersion,
		Root:    root,
		AppDir:  appDir,
		SQLDir:  filepath.Join(root, sqlBundleDir),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if strings.TrimSpace(bundle.Root) == "" || strings.TrimSpace(bundle.AppDir) == "" {
		return fmt.Errorf("AIFAR bundle path is empty")
	}
	if info, err := os.Stat(bundle.AppDir); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("AIFAR app bundle is not a directory: %s", bundle.AppDir)
	}
	if _, err := os.Stat(filepath.Join(bundle.AppDir, ".env")); err != nil {
		return fmt.Errorf("AIFAR common .env is required: %w", err)
	}
	for _, image := range []string{"openjre-rocky-21.tar", "nginx-stable-alpine.tar"} {
		if _, err := os.Stat(filepath.Join(bundle.Root, "docker-images", image)); err != nil {
			return fmt.Errorf("AIFAR offline Docker image %s is required: %w", image, err)
		}
	}
	for _, service := range []string{"gateway", "web-vue3"} {
		if _, err := os.Stat(filepath.Join(bundle.AppDir, service, "docker-compose.yaml")); err != nil {
			return fmt.Errorf("AIFAR service %s docker-compose.yaml is required: %w", service, err)
		}
	}
	return nil
}

func CreateBundleArchive(bundle Bundle) (string, error) {
	if err := VerifyBundle(bundle); err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "aifar-service-bundle-*.tar.gz")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer file.Close()
	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	if err := filepath.WalkDir(bundle.Root, func(pathValue string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(bundle.Root, pathValue)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skipBundleEntry(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(pathValue)
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		source, err := os.Open(pathValue)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func skipBundleEntry(rel string) bool {
	slash := filepath.ToSlash(rel)
	return slash == appBundleDir+"/nacos" || strings.HasPrefix(slash, appBundleDir+"/nacos/")
}

func inferAppDir(pathValue string) string {
	pathValue = filepath.Clean(pathValue)
	info, err := os.Stat(pathValue)
	if err == nil && info.IsDir() && filepath.Base(pathValue) == appBundleDir {
		return pathValue
	}
	dir := filepath.Dir(pathValue)
	if filepath.Base(dir) == appBundleDir {
		return dir
	}
	if filepath.Base(pathValue) == appBundleDir {
		return pathValue
	}
	return ""
}

func serviceOrderText() string {
	return strings.Join(serviceOrder, " ")
}

func reverseServiceOrderText() string {
	return strings.Join(reverseServiceOrder, " ")
}

func stringParam(parameters map[string]any, name, fallback string) string {
	value, ok := parameters[name]
	if !ok || value == nil {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return fallback
	}
	return text
}

func intParam(parameters map[string]any, name string, fallback int) int {
	value, ok := parameters[name]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

func boolParam(parameters map[string]any, name string, fallback bool) bool {
	value, ok := parameters[name]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		v = strings.TrimSpace(strings.ToLower(v))
		return v == "true" || v == "1" || v == "yes"
	default:
		return fallback
	}
}

func stringListParam(parameters map[string]any, name string, fallback []string) []string {
	value, ok := parameters[name]
	if !ok || value == nil {
		return fallback
	}
	var out []string
	appendValue := func(item any) {
		for _, part := range strings.FieldsFunc(fmt.Sprint(item), func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		}) {
			if text := strings.TrimSpace(part); text != "" {
				out = append(out, text)
			}
		}
	}
	switch v := value.(type) {
	case []string:
		for _, item := range v {
			appendValue(item)
		}
	case []any:
		for _, item := range v {
			appendValue(item)
		}
	default:
		appendValue(v)
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}
