package runtimeagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type NacosProxyAction string

const (
	NacosProxyRegister   NacosProxyAction = "register"
	NacosProxyDeregister NacosProxyAction = "deregister"
)

type NacosProxySyncOptions struct {
	StateDir string
	Specs    []RuntimeSpec
	Action   NacosProxyAction
	AgentIP  string
	Client   *http.Client
	Log      io.Writer
}

type nacosRuntimeEnv struct {
	BaseURL   string
	HostPort  string
	Namespace string
	Group     string
	User      string
	Password  string
}

func SyncNacosProxyRegistrations(ctx context.Context, options NacosProxySyncOptions) error {
	action := options.Action
	if action == "" {
		action = NacosProxyRegister
	}
	specs := options.Specs
	if len(specs) == 0 {
		loaded, err := loadRuntimeSpecsForNacos(options.StateDir)
		if err != nil {
			return err
		}
		specs = loaded
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	var errs []string
	for _, raw := range specs {
		spec := NormalizeSpec(raw)
		env, ok := nacosEnvForSpec(spec)
		if !ok {
			continue
		}
		agentIP := strings.TrimSpace(options.AgentIP)
		if agentIP == "" {
			agentIP = localIPForNacos(env.HostPort)
		}
		if agentIP == "" {
			errs = append(errs, fmt.Sprintf("%s: resolve agent host IP", spec.InstanceID))
			continue
		}
		token := nacosAccessToken(ctx, client, env)
		for _, service := range spec.Services {
			appName := serviceAppName(service)
			if appName == "" || service.Port <= 0 {
				continue
			}
			if err := syncNacosProxy(ctx, client, env, action, spec, appName, agentIP, service.Port, token); err != nil {
				errs = append(errs, fmt.Sprintf("%s/%s: %v", spec.InstanceID, service.Name, err))
				continue
			}
			logf(options.Log, "AIFAR Nacos proxy %s: %s -> %s:%d\n", action, appName, agentIP, service.Port)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("sync AIFAR Nacos proxies: %s", strings.Join(errs, "; "))
	}
	return nil
}

func StartNacosProxyHeartbeat(ctx context.Context, options NacosProxySyncOptions) {
	interval := 5 * time.Second
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 5 * time.Second}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := HeartbeatNacosProxyRegistrations(ctx, options); err != nil {
			logf(options.Log, "AIFAR Nacos proxy heartbeat failed, replay registrations: %v\n", err)
			register := options
			register.Action = NacosProxyRegister
			if replayErr := SyncNacosProxyRegistrations(ctx, register); replayErr != nil {
				logf(options.Log, "AIFAR Nacos proxy registration replay failed: %v\n", replayErr)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func HeartbeatNacosProxyRegistrations(ctx context.Context, options NacosProxySyncOptions) error {
	specs := options.Specs
	if len(specs) == 0 {
		loaded, err := loadRuntimeSpecsForNacos(options.StateDir)
		if err != nil {
			return err
		}
		specs = loaded
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	var errs []string
	for _, raw := range specs {
		spec := NormalizeSpec(raw)
		if !specNacosEphemeral(spec) {
			continue
		}
		env, ok := nacosEnvForSpec(spec)
		if !ok {
			continue
		}
		agentIP := strings.TrimSpace(options.AgentIP)
		if agentIP == "" {
			agentIP = localIPForNacos(env.HostPort)
		}
		if agentIP == "" {
			errs = append(errs, fmt.Sprintf("%s: resolve agent host IP", spec.InstanceID))
			continue
		}
		token := nacosAccessToken(ctx, client, env)
		for _, service := range spec.Services {
			appName := serviceAppName(service)
			if appName == "" || service.Port <= 0 {
				continue
			}
			endpoint := nacosHeartbeatURL(env, spec, appName, agentIP, service.Port, token)
			if err := doNacosRequest(ctx, client, http.MethodPut, endpoint, false); err != nil {
				errs = append(errs, fmt.Sprintf("%s/%s: %v", spec.InstanceID, service.Name, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("heartbeat AIFAR Nacos proxies: %s", strings.Join(errs, "; "))
	}
	return nil
}

func loadRuntimeSpecsForNacos(stateDir string) ([]RuntimeSpec, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		stateDir = DefaultStateDir
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	specs := make([]RuntimeSpec, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		spec, err := readSpecFile(filepath.Join(stateDir, entry.Name(), "runtime-spec.json"))
		if err != nil {
			continue
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func nacosEnvForSpec(spec RuntimeSpec) (nacosRuntimeEnv, bool) {
	envDir := filepath.Join(spec.InstallRoot, "runtime", "env")
	common := readEnvFile(filepath.Join(envDir, "java-common.env"))
	if len(common) == 0 {
		return nacosRuntimeEnv{}, false
	}
	secrets := readEnvFile(filepath.Join(envDir, "java-secrets.env"))
	hostPort := strings.TrimSpace(common["NACOS_HOST"])
	if hostPort == "" {
		return nacosRuntimeEnv{}, false
	}
	if strings.HasPrefix(hostPort, "http://") || strings.HasPrefix(hostPort, "https://") {
		parsed, err := url.Parse(hostPort)
		if err == nil {
			hostPort = parsed.Host
		}
	}
	if port := strings.TrimSpace(common["NACOS_PORT_WEB"]); port != "" && !strings.Contains(hostPort, ":") {
		hostPort = net.JoinHostPort(hostPort, port)
	}
	namespace := strings.TrimSpace(common["NACOS_NS"])
	if namespace == "" {
		namespace = "prod"
	}
	group := strings.TrimSpace(common["NACOS_GROUP"])
	if group == "" {
		group = strings.TrimSpace(spec.Nacos.Group)
	}
	user := strings.TrimSpace(common["NACOS_USER"])
	if user == "" {
		user = "nacos"
	}
	return nacosRuntimeEnv{
		BaseURL:   "http://" + hostPort,
		HostPort:  hostPort,
		Namespace: namespace,
		Group:     group,
		User:      user,
		Password:  strings.TrimSpace(secrets["NACOS_PASSWORD"]),
	}, true
}

func readEnvFile(path string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
				value = value[1 : len(value)-1]
			}
		}
		if key != "" {
			values[key] = value
		}
	}
	return values
}

func serviceAppName(service ServiceSpec) string {
	if appName := strings.TrimSpace(service.AppName); appName != "" {
		return appName
	}
	switch strings.TrimSpace(service.Name) {
	case "gateway":
		return "alpha-gateway"
	case "oauth":
		return "alpha-oauth"
	case "permission":
		return "alpha-permission"
	case "system":
		return "alpha-system"
	case "file":
		return "alpha-file"
	case "message":
		return "alpha-message"
	case "im":
		return "alpha-im"
	case "contacts":
		return "alpha-contacts"
	case "meeting":
		return "alpha-meeting"
	default:
		return ""
	}
}

func nacosAccessToken(ctx context.Context, client *http.Client, env nacosRuntimeEnv) string {
	form := url.Values{}
	form.Set("username", env.User)
	form.Set("password", env.Password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.BaseURL+"/nacos/v1/auth/users/login", strings.NewReader(form.Encode()))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	var body struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.AccessToken)
}

func syncNacosProxy(ctx context.Context, client *http.Client, env nacosRuntimeEnv, action NacosProxyAction, spec RuntimeSpec, appName, ip string, port int, token string) error {
	endpoint := nacosInstanceURL(env, appName, ip, port, token, specNacosEphemeral(spec))
	switch action {
	case NacosProxyRegister:
		_ = doNacosRequest(ctx, client, http.MethodDelete, endpoint, true)
		return doNacosRequest(ctx, client, http.MethodPost, endpoint, false)
	case NacosProxyDeregister:
		return doNacosRequest(ctx, client, http.MethodDelete, endpoint, true)
	default:
		return fmt.Errorf("unsupported Nacos proxy action %q", action)
	}
}

func nacosInstanceURL(env nacosRuntimeEnv, appName, ip string, port int, token string, ephemeral bool) string {
	query := url.Values{}
	query.Set("serviceName", appName)
	query.Set("ip", ip)
	query.Set("port", strconv.Itoa(port))
	query.Set("namespaceId", env.Namespace)
	query.Set("ephemeral", strconv.FormatBool(ephemeral))
	if strings.TrimSpace(env.Group) != "" {
		query.Set("groupName", strings.TrimSpace(env.Group))
	}
	if token = strings.TrimSpace(token); token != "" {
		query.Set("accessToken", token)
	}
	return env.BaseURL + "/nacos/v1/ns/instance?" + query.Encode()
}

func nacosHeartbeatURL(env nacosRuntimeEnv, spec RuntimeSpec, appName, ip string, port int, token string) string {
	query := url.Values{}
	query.Set("serviceName", appName)
	query.Set("ip", ip)
	query.Set("port", strconv.Itoa(port))
	query.Set("namespaceId", env.Namespace)
	query.Set("ephemeral", strconv.FormatBool(specNacosEphemeral(spec)))
	if group := strings.TrimSpace(env.Group); group != "" {
		query.Set("groupName", group)
	}
	beat := map[string]any{
		"serviceName": appName,
		"ip":          ip,
		"port":        port,
	}
	if group := strings.TrimSpace(env.Group); group != "" {
		beat["groupName"] = group
	}
	data, _ := json.Marshal(beat)
	query.Set("beat", string(data))
	if token = strings.TrimSpace(token); token != "" {
		query.Set("accessToken", token)
	}
	return env.BaseURL + "/nacos/v1/ns/instance/beat?" + query.Encode()
}

func specNacosEphemeral(spec RuntimeSpec) bool {
	spec = NormalizeSpec(spec)
	return spec.Nacos.Ephemeral == nil || *spec.Nacos.Ephemeral
}

func doNacosRequest(ctx context.Context, client *http.Client, method, endpoint string, allowNotFound bool) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && allowNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s", method, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func localIPForNacos(hostPort string) string {
	target := strings.TrimSpace(hostPort)
	if target != "" {
		if _, _, err := net.SplitHostPort(target); err != nil {
			if host, port, ok := strings.Cut(target, ":"); ok && host != "" && port != "" {
				target = net.JoinHostPort(host, port)
			} else {
				target = net.JoinHostPort(target, "80")
			}
		}
		conn, err := net.DialTimeout("udp", target, time.Second)
		if err == nil {
			defer conn.Close()
			if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
				return addr.IP.String()
			}
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addrs {
			var ip net.IP
			switch addr := raw.(type) {
			case *net.IPNet:
				ip = addr.IP
			case *net.IPAddr:
				ip = addr.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return ""
}
