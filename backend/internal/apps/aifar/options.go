package aifar

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
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
	appBundleVersion     = "runtime-v2"
	appBundleSchema      = "aifar-runtime-bundle-v2"
	appBundleDir         = "services"
	imageBundleDir       = "images"
	runtimeBundleDir     = "runtime"
	bundleManifestName   = "manifest.json"
	runtimeDefaultsName  = "defaults.env"
	installDirName       = "admin"
	defaultTopology      = "single"
	defaultNetworkName   = "aifar-network"
	defaultTimezone      = "system"
	defaultNacosUser     = "nacos"
	defaultNacosPassword = "oversea.nacos"
	defaultNacosNS       = "prod"
	defaultAppCPUs       = "2.0"
	defaultMemoryLimit   = "2GB"
	defaultGatewayPort   = 38000
	defaultWebPort       = 8080
	defaultNacosWebPort  = 8848
	defaultNacosAPIPort  = 9848
	dependencyManual     = "manual"
	dependencyExisting   = "existing"
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
	JVMInitialRAMPercentage float64
	JVMMaxRAMPercentage     float64
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
	SelectedServices        []string
}

type Bundle struct {
	Version      string
	Root         string
	AppDir       string
	ImageDir     string
	RuntimeDir   string
	ManifestPath string
}

type runtimeBundleManifest struct {
	Schema   string   `json:"schema"`
	Version  string   `json:"version"`
	Services []string `json:"services"`
	Images   []string `json:"images"`
}

func optionsFromParameters(parameters map[string]any) InstallOptions {
	opts := InstallOptions{
		Timezone:                defaultTimezone,
		NetworkName:             defaultNetworkName,
		AppCPUs:                 defaultAppCPUs,
		AppMemoryLimit:          defaultMemoryLimit,
		JVMInitialRAMPercentage: defaultJVMInitialRAMPercentage,
		JVMMaxRAMPercentage:     defaultJVMMaxRAMPercentage,
		GatewayPort:             defaultGatewayPort,
		WebPort:                 defaultWebPort,
		NacosWebPort:            defaultNacosWebPort,
		NacosAPIPort:            defaultNacosAPIPort,
		NacosSource:             dependencyManual,
		NacosUser:               defaultNacosUser,
		NacosPassword:           defaultNacosPassword,
		NacosNamespace:          defaultNacosNS,
		SelectedServices:        defaultInstallServices(),
	}
	opts.Timezone = stringParam(parameters, "timezone", opts.Timezone)
	opts.NetworkName = stringParam(parameters, "networkName", opts.NetworkName)
	opts.AppCPUs = stringParam(parameters, "appCPUs", opts.AppCPUs)
	opts.AppMemoryLimit = stringParam(parameters, "appMemoryLimit", opts.AppMemoryLimit)
	opts.JVMInitialRAMPercentage = floatParam(parameters, "jvmInitialRAMPercentage", opts.JVMInitialRAMPercentage)
	opts.JVMMaxRAMPercentage = floatParam(parameters, "jvmMaxRAMPercentage", opts.JVMMaxRAMPercentage)
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
	opts.SelectedServices = normalizeSelectedServices(sliceParam(parameters, "selectedServices", opts.SelectedServices))
	return opts
}

func (o InstallOptions) Validate() error {
	if o.NacosSource == dependencyExisting && strings.TrimSpace(o.NacosInstanceID) == "" {
		return fmt.Errorf("nacos instance is required")
	}
	if strings.TrimSpace(o.NacosHost) == "" {
		return fmt.Errorf("nacos host is required")
	}
	if !validPort(o.GatewayPort) || !validPort(o.WebPort) || !validPort(o.NacosWebPort) || !validPort(o.NacosAPIPort) {
		return fmt.Errorf("ports must be between 1 and 65535")
	}
	for name, value := range map[string]string{
		"timezone":       o.Timezone,
		"networkName":    o.NetworkName,
		"appCPUs":        o.AppCPUs,
		"appMemoryLimit": o.AppMemoryLimit,
		"nacosHost":      o.NacosHost,
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
	if err := validateRuntimeConfigValues(RuntimeConfigValues{
		AppCPUs:                 o.AppCPUs,
		AppMemoryLimit:          o.AppMemoryLimit,
		JVMInitialRAMPercentage: o.JVMInitialRAMPercentage,
		JVMMaxRAMPercentage:     o.JVMMaxRAMPercentage,
	}, true); err != nil {
		return err
	}
	return nil
}

func defaultInstallServices() []string {
	return append([]string(nil), serviceOrder...)
}

func normalizeSelectedServices(values []string) []string {
	selected := make(map[string]bool, len(values)+2)
	for _, value := range values {
		service := cleanAIFARServiceName(value)
		if aifarServiceSupported(service) {
			selected[service] = true
		}
	}
	selected["gateway"] = true
	selected["web-vue3"] = true
	out := make([]string, 0, len(serviceOrder))
	for _, service := range serviceOrder {
		if selected[service] {
			out = append(out, service)
		}
	}
	if len(out) == 0 {
		return defaultInstallServices()
	}
	return out
}

func normalizeDependencySource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case dependencyExisting:
		return dependencyExisting
	default:
		return dependencyManual
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
		root := inferBundleRoot(res.Path)
		if root == "" {
			continue
		}
		candidates = append(candidates, store.Resource{Version: res.Version, Path: root})
	}
	if len(candidates) == 0 {
		return Bundle{}, fmt.Errorf("AIFAR runtime-v2 bundle was not found under resources/aifar/%s", appBundleVersion)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	root := candidates[0].Path
	return Bundle{
		Version:      appBundleVersion,
		Root:         root,
		AppDir:       filepath.Join(root, appBundleDir),
		ImageDir:     filepath.Join(root, imageBundleDir),
		RuntimeDir:   filepath.Join(root, runtimeBundleDir),
		ManifestPath: filepath.Join(root, bundleManifestName),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if strings.TrimSpace(bundle.Root) == "" || strings.TrimSpace(bundle.AppDir) == "" {
		return fmt.Errorf("AIFAR bundle path is empty")
	}
	manifest, err := readRuntimeBundleManifest(bundle.ManifestPath)
	if err != nil {
		return err
	}
	if manifest.Schema != appBundleSchema {
		return fmt.Errorf("AIFAR bundle schema must be %s, got %s", appBundleSchema, manifest.Schema)
	}
	if manifest.Version != "" && manifest.Version != appBundleVersion {
		return fmt.Errorf("AIFAR bundle manifest version must be %s, got %s", appBundleVersion, manifest.Version)
	}
	if info, err := os.Stat(bundle.AppDir); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("AIFAR app bundle is not a directory: %s", bundle.AppDir)
	}
	if _, err := os.Stat(filepath.Join(bundle.RuntimeDir, runtimeDefaultsName)); err != nil {
		return fmt.Errorf("AIFAR runtime defaults.env is required: %w", err)
	}
	requiredImages := manifest.Images
	if len(requiredImages) == 0 {
		requiredImages = []string{"openjre-rocky-21.tar", "nginx-stable-alpine.tar"}
	}
	for _, image := range requiredImages {
		if _, err := os.Stat(filepath.Join(bundle.ImageDir, image)); err != nil {
			return fmt.Errorf("AIFAR offline Docker image %s is required: %w", image, err)
		}
	}
	_, err = discoverBundleServices(bundle)
	return err
}

func readRuntimeBundleManifest(pathValue string) (runtimeBundleManifest, error) {
	var manifest runtimeBundleManifest
	if strings.TrimSpace(pathValue) == "" {
		return manifest, fmt.Errorf("AIFAR runtime-v2 manifest path is empty")
	}
	data, err := os.ReadFile(pathValue)
	if err != nil {
		return manifest, fmt.Errorf("AIFAR runtime-v2 manifest is required: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("AIFAR runtime-v2 manifest is invalid: %w", err)
	}
	return manifest, nil
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

func CreateServiceModuleArchive(bundle Bundle, services []string) (string, error) {
	selected := map[string]bool{}
	for _, service := range services {
		selected[cleanAIFARServiceName(service)] = true
	}
	file, err := os.CreateTemp("", "aifar-service-modules-*.tar.gz")
	if err != nil {
		return "", err
	}
	path := file.Name()
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	fail := func(err error) (string, error) {
		_ = tw.Close()
		_ = gz.Close()
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	for service := range selected {
		root := filepath.Join(bundle.AppDir, service)
		if err := filepath.WalkDir(root, func(pathValue string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(bundle.AppDir, pathValue)
			if err != nil {
				return err
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
			return fail(err)
		}
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func skipBundleEntry(rel string) bool {
	slash := filepath.ToSlash(rel)
	return slash == appBundleDir+"/nacos" ||
		strings.HasPrefix(slash, appBundleDir+"/nacos/") ||
		(strings.HasPrefix(slash, appBundleDir+"/") && strings.HasSuffix(slash, "/docker-compose.yaml"))
}

func inferBundleRoot(pathValue string) string {
	pathValue = filepath.Clean(pathValue)
	current := pathValue
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if filepath.Base(current) == appBundleVersion {
			return current
		}
		next := filepath.Dir(current)
		if next == current || next == "." {
			break
		}
		current = next
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

func floatParam(parameters map[string]any, name string, fallback float64) float64 {
	value, ok := parameters[name]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		n, err := v.Float64()
		if err == nil {
			return n
		}
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return n
		}
	}
	return fallback
}

func sliceParam(parameters map[string]any, name string, fallback []string) []string {
	value, ok := parameters[name]
	if !ok || value == nil {
		return append([]string(nil), fallback...)
	}
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		if len(out) > 0 {
			return out
		}
	case string:
		parts := strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
		})
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			text := strings.TrimSpace(part)
			if text != "" {
				out = append(out, text)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return append([]string(nil), fallback...)
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}
