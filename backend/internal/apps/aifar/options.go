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
	defaultTopology      = "single"
	defaultNetworkName   = "alpha-network"
	defaultTimezone      = "Asia/Phnom_Penh"
	defaultDBNameNacos   = "alpha_cloud_nacos"
	defaultNacosUser     = "nacos"
	defaultNacosPassword = "oversea.nacos"
	defaultNacosNS       = "dyx"
	defaultAppCPUs       = "2.0"
	defaultMemoryLimit   = "2GB"
	defaultGatewayPort   = 38000
	defaultWebPort       = 8080
	defaultNacosWebPort  = 30099
	defaultNacosAPIPort  = 31099
	defaultDBPort        = 3306
)

var serviceOrder = []string{
	"nacos",
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
	"nacos",
}

type InstallOptions struct {
	Timezone       string
	NetworkName    string
	AppCPUs        string
	AppMemoryLimit string
	GatewayPort    int
	WebPort        int
	NacosWebPort   int
	NacosAPIPort   int
	NacosUser      string
	NacosPassword  string
	NacosNamespace string
	DBHost         string
	DBPort         int
	DBNameNacos    string
	DBUser         string
	DBPassword     string
	InitSQL        bool
}

type Bundle struct {
	Version string
	Root    string
	AppDir  string
	SQLDir  string
}

func optionsFromParameters(parameters map[string]any) InstallOptions {
	opts := InstallOptions{
		Timezone:       defaultTimezone,
		NetworkName:    defaultNetworkName,
		AppCPUs:        defaultAppCPUs,
		AppMemoryLimit: defaultMemoryLimit,
		GatewayPort:    defaultGatewayPort,
		WebPort:        defaultWebPort,
		NacosWebPort:   defaultNacosWebPort,
		NacosAPIPort:   defaultNacosAPIPort,
		NacosUser:      defaultNacosUser,
		NacosPassword:  defaultNacosPassword,
		NacosNamespace: defaultNacosNS,
		DBPort:         defaultDBPort,
		DBNameNacos:    defaultDBNameNacos,
		DBUser:         "root",
	}
	opts.Timezone = stringParam(parameters, "timezone", opts.Timezone)
	opts.NetworkName = stringParam(parameters, "networkName", opts.NetworkName)
	opts.AppCPUs = stringParam(parameters, "appCPUs", opts.AppCPUs)
	opts.AppMemoryLimit = stringParam(parameters, "appMemoryLimit", opts.AppMemoryLimit)
	opts.GatewayPort = intParam(parameters, "gatewayPort", opts.GatewayPort)
	opts.WebPort = intParam(parameters, "webPort", opts.WebPort)
	opts.NacosWebPort = intParam(parameters, "nacosWebPort", opts.NacosWebPort)
	opts.NacosAPIPort = intParam(parameters, "nacosApiPort", opts.NacosAPIPort)
	opts.NacosUser = stringParam(parameters, "nacosUser", opts.NacosUser)
	opts.NacosPassword = stringParam(parameters, "nacosPassword", opts.NacosPassword)
	opts.NacosNamespace = stringParam(parameters, "nacosNamespace", opts.NacosNamespace)
	opts.DBHost = stringParam(parameters, "dbHost", opts.DBHost)
	opts.DBPort = intParam(parameters, "dbPort", opts.DBPort)
	opts.DBNameNacos = stringParam(parameters, "dbNameNacos", opts.DBNameNacos)
	opts.DBUser = stringParam(parameters, "dbUser", opts.DBUser)
	opts.DBPassword = stringParam(parameters, "dbPassword", opts.DBPassword)
	opts.InitSQL = boolParam(parameters, "initSql", false)
	return opts
}

func (o InstallOptions) Validate() error {
	if strings.TrimSpace(o.DBHost) == "" {
		return fmt.Errorf("database host is required")
	}
	if strings.TrimSpace(o.DBUser) == "" {
		return fmt.Errorf("database user is required")
	}
	if strings.TrimSpace(o.DBPassword) == "" {
		return fmt.Errorf("database password is required")
	}
	if strings.TrimSpace(o.DBNameNacos) == "" {
		return fmt.Errorf("nacos database name is required")
	}
	if !validPort(o.DBPort) || !validPort(o.GatewayPort) || !validPort(o.WebPort) || !validPort(o.NacosWebPort) || !validPort(o.NacosAPIPort) {
		return fmt.Errorf("ports must be between 1 and 65535")
	}
	for name, value := range map[string]string{
		"timezone":       o.Timezone,
		"networkName":    o.NetworkName,
		"appCPUs":        o.AppCPUs,
		"appMemoryLimit": o.AppMemoryLimit,
		"nacosUser":      o.NacosUser,
		"nacosPassword":  o.NacosPassword,
		"nacosNamespace": o.NacosNamespace,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s must not contain newlines", name)
		}
	}
	return nil
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
	for _, service := range []string{"nacos", "gateway", "web-vue3"} {
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

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}
