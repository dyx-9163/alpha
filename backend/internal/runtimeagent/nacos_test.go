package runtimeagent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSyncNacosProxyRegistrationsDeregistersAgentProxyInstances(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"token-1"}`))
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hostPort := strings.TrimPrefix(server.URL, "http://")
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST="+hostPort+"\nNACOS_PORT_WEB=8848\nNACOS_USER=nacos\nNACOS_NS=prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-secrets.env"), []byte("NACOS_PASSWORD=secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := SyncNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{
		Specs: []RuntimeSpec{{
			InstanceID:  "admin",
			InstallRoot: installRoot,
			Services: []ServiceSpec{
				{Name: "file", AppName: "alpha-file", Port: 38005},
				{Name: "web-vue3", Port: 8080},
			},
		}},
		Action:  NacosProxyDeregister,
		AgentIP: "192.168.74.132",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{
		"POST /nacos/v1/auth/users/login?",
		"DELETE /nacos/v1/ns/instance?",
		"serviceName=alpha-file",
		"ip=192.168.74.132",
		"port=38005",
		"namespaceId=prod",
		"ephemeral=true",
		"accessToken=token-1",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected Nacos request containing %q, got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "web-vue3") {
		t.Fatalf("web-vue3 must not be registered in Nacos, got:\n%s", joined)
	}
}

func TestSyncNacosDiscoveryEventLoadsInstanceEnvWithoutManifestNacosState(t *testing.T) {
	var requestsMu sync.Mutex
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		requestsMu.Unlock()
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			_, _ = w.Write([]byte(`{"accessToken":"test-token"}`))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	hostPort := strings.TrimPrefix(server.URL, "http://")
	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST="+hostPort+"\nNACOS_NS=prod\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-secrets.env"), []byte("NACOS_PASSWORD=test-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := InstanceConfig{InstanceID: "admin", InstallRoot: installRoot}
	event := readyDiscoveryEvent("permission", "permission-1")
	event.ListenPort = 38010
	if err := syncNacosDiscoveryEvent(context.Background(), config, event, NacosProxyRegister); err != nil {
		t.Fatal(err)
	}
	requestsMu.Lock()
	joined := strings.Join(requests, "\n")
	requestsMu.Unlock()
	for _, want := range []string{"POST /nacos/v1/auth/users/login?", "POST /nacos/v1/ns/instance?", "ip=", "port=38010", "serviceName=alpha-permission"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected request containing %q, got:\n%s", want, joined)
		}
	}
}

func TestLoadRuntimeSpecsForNacosSkipsNewModelEvenWithDesiredReadyReplica(t *testing.T) {
	stateDir := t.TempDir()
	store := &ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(controllerTestConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	deploymentDir := filepath.Join(stateDir, "admin", "deployments")
	if err := os.WriteFile(filepath.Join(deploymentDir, "file.json"), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	specs, err := loadRuntimeSpecsForNacos(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 {
		t.Fatalf("global Nacos replay included new-model manifests: %+v", specs)
	}
}

func TestLoadRuntimeSpecsForNacosKeepsLegacyFallback(t *testing.T) {
	stateDir := t.TempDir()
	manager := NewManager(ManagerOptions{StateDir: stateDir, Runner: &fakeRunner{}})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "legacy",
		InstallRoot: t.TempDir(),
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{{ServiceName: "permission", Image: "permission:rev-1", PodRevision: "rev-1", Replicas: 1}},
		Services:    []ServiceSpec{{Name: "permission", AppName: "alpha-permission", Port: 38010}},
	})
	if err := manager.writeSpec(spec); err != nil {
		t.Fatal(err)
	}
	specs, err := loadRuntimeSpecsForNacos(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].InstanceID != "legacy" {
		t.Fatalf("legacy runtime replay missing: %+v", specs)
	}
}

func TestNacosErrorsSanitizeTokenizedTransportURLAndResponseBody(t *testing.T) {
	spec := nacosErrorTestSpec(t)
	t.Run("transport", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/nacos/v1/auth/users/login" {
				return nacosTestResponse(request, http.StatusOK, `{"accessToken":"secret-token"}`), nil
			}
			return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: errors.New("transport-secret")}
		})}
		err := SyncNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{Specs: []RuntimeSpec{spec}, Client: client})
		if err == nil {
			t.Fatal("expected transport failure")
		}
		assertNacosSecretAbsent(t, err.Error(), "secret-token", "accessToken", "transport-secret", "/nacos/v1/ns/instance?")
	})

	t.Run("response-body", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/nacos/v1/auth/users/login" {
				return nacosTestResponse(request, http.StatusOK, `{"accessToken":"secret-token"}`), nil
			}
			return nacosTestResponse(request, http.StatusInternalServerError, "password=body-secret token=response-secret"), nil
		})}
		err := SyncNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{Specs: []RuntimeSpec{spec}, Client: client})
		if err == nil {
			t.Fatal("expected HTTP failure")
		}
		assertNacosSecretAbsent(t, err.Error(), "body-secret", "response-secret", "password=")
	})
}

func TestStartNacosProxyHeartbeatLogsOnlySanitizedContext(t *testing.T) {
	spec := nacosErrorTestSpec(t)
	requested := make(chan struct{}, 4)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/nacos/v1/auth/users/login" {
			return nacosTestResponse(request, http.StatusOK, `{"accessToken":"secret-token"}`), nil
		}
		select {
		case requested <- struct{}{}:
		default:
		}
		return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: errors.New("transport-secret")}
	})}
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		StartNacosProxyHeartbeat(ctx, NacosProxySyncOptions{Specs: []RuntimeSpec{spec}, Client: client, Log: &log})
		close(done)
	}()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("heartbeat request did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not stop")
	}
	got := log.String()
	if !strings.Contains(got, "heartbeat failed") {
		t.Fatalf("missing stable heartbeat context: %q", got)
	}
	assertNacosSecretAbsent(t, got, "secret-token", "accessToken", "transport-secret", "NACOS_PASSWORD")
}

func nacosErrorTestSpec(t *testing.T) RuntimeSpec {
	t.Helper()
	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST=127.0.0.1:8848\nNACOS_USER=nacos-user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-secrets.env"), []byte("NACOS_PASSWORD=password-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NormalizeSpec(RuntimeSpec{
		InstanceID:  "legacy",
		InstallRoot: installRoot,
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{{ServiceName: "permission", Image: "permission:rev-1", PodRevision: "rev-1", Replicas: 1}},
		Services:    []ServiceSpec{{Name: "permission", AppName: "alpha-permission", Port: 38010}},
	})
}

func nacosTestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    request,
	}
}

func assertNacosSecretAbsent(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(value, secret) {
			t.Fatalf("Nacos error/log leaked %q: %q", secret, value)
		}
	}
}

func TestSyncNacosProxyRegistrationsRegistersByDeletingThenPosting(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			_, _ = w.Write([]byte(`{"accessToken":"token-1"}`))
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST="+strings.TrimPrefix(server.URL, "http://")+"\nNACOS_NS=prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{
		Specs: []RuntimeSpec{{
			InstanceID:  "admin",
			InstallRoot: installRoot,
			Services:    []ServiceSpec{{Name: "gateway", Port: 38000}},
		}},
		Action:  NacosProxyRegister,
		AgentIP: "192.168.74.132",
		Client:  server.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	deleteIndex := strings.Index(joined, "DELETE /nacos/v1/ns/instance?")
	postIndex := strings.Index(joined, "POST /nacos/v1/ns/instance?")
	if deleteIndex < 0 || postIndex < 0 || deleteIndex > postIndex {
		t.Fatalf("register should delete stale instance before post, got:\n%s", joined)
	}
}

func TestSyncNacosProxyRegistrationsUsesAgentIPStrategy(t *testing.T) {
	t.Setenv("AIFAR_AGENT_IP", "10.19.0.7")
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			_, _ = w.Write([]byte(`{"accessToken":"token-1"}`))
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST="+strings.TrimPrefix(server.URL, "http://")+"\nNACOS_NS=prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SyncNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{
		Specs: []RuntimeSpec{{
			InstanceID:  "admin",
			InstallRoot: installRoot,
			Services:    []ServiceSpec{{Name: "permission", AppName: "alpha-permission", Port: 38010}},
			Nacos:       NacosSpec{AgentIPStrategy: "env"},
		}},
		Action: NacosProxyRegister,
		Client: server.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "serviceName=alpha-permission") || !strings.Contains(joined, "ip=10.19.0.7") {
		t.Fatalf("expected env strategy IP in Nacos request, got:\n%s", joined)
	}
}

func TestSyncNacosProxyRegistrationsRepairsServiceTypeConflict(t *testing.T) {
	requests := []string{}
	instancePosts := 0
	var logs strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			_, _ = w.Write([]byte(`{"accessToken":"token-1"}`))
			return
		}
		if r.URL.Path == "/nacos/v1/ns/instance" && r.Method == http.MethodPost {
			instancePosts++
			if r.URL.Query().Get("ephemeral") == "true" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`errCode: 400, errMsg: Current service DEFAULT_GROUP@@alpha-oauth is persistent service, can't register ephemeral instance.`))
				return
			}
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST="+strings.TrimPrefix(server.URL, "http://")+"\nNACOS_NS=prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{
		Specs: []RuntimeSpec{{
			InstanceID:  "admin",
			InstallRoot: installRoot,
			Services:    []ServiceSpec{{Name: "oauth", AppName: "alpha-oauth", Port: 38001}},
			Nacos:       NacosSpec{Group: "DEFAULT_GROUP"},
		}},
		Action:  NacosProxyRegister,
		AgentIP: "192.168.74.132",
		Client:  server.Client(),
		Log:     &logs,
	}); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(requests, "\n")
	firstPost := strings.Index(joined, "POST /nacos/v1/ns/instance?")
	serviceDelete := strings.Index(joined, "DELETE /nacos/v1/ns/service?")
	fallbackPost := strings.LastIndex(joined, "POST /nacos/v1/ns/instance?")
	if firstPost < 0 || serviceDelete < 0 || fallbackPost <= firstPost || !(firstPost < serviceDelete && serviceDelete < fallbackPost) {
		t.Fatalf("expected instance POST, service DELETE, then instance POST fallback, got:\n%s", joined)
	}
	for _, want := range []string{
		"serviceName=alpha-oauth",
		"serviceName=DEFAULT_GROUP%40%40alpha-oauth",
		"groupName=DEFAULT_GROUP",
		"namespaceId=prod",
		"ephemeral=true",
		"ephemeral=false",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected Nacos repair request containing %q, got:\n%s", want, joined)
		}
	}
	if !strings.Contains(logs.String(), "cleanup service metadata: alpha-oauth") {
		t.Fatalf("expected repair log, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "fallback: alpha-oauth ephemeral=false") {
		t.Fatalf("expected persistent fallback log, got %q", logs.String())
	}
	if instancePosts < 3 {
		t.Fatalf("expected initial post, retry, and fallback post, got %d requests:\n%s", instancePosts, joined)
	}
}

func TestHeartbeatNacosProxyRegistrationsIgnoresPersistentServiceConflict(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			_, _ = w.Write([]byte(`{"accessToken":"token-1"}`))
			return
		}
		if r.URL.Path == "/nacos/v1/ns/instance/beat" && r.Method == http.MethodPut {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`errCode: 400, errMsg: Current service DEFAULT_GROUP@@alpha-oauth is persistent service, can't register ephemeral instance.`))
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer server.Close()

	installRoot := t.TempDir()
	envDir := filepath.Join(installRoot, "runtime", "env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "java-common.env"), []byte("NACOS_HOST="+strings.TrimPrefix(server.URL, "http://")+"\nNACOS_NS=prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := HeartbeatNacosProxyRegistrations(context.Background(), NacosProxySyncOptions{
		Specs: []RuntimeSpec{{
			InstanceID:  "admin",
			InstallRoot: installRoot,
			Services:    []ServiceSpec{{Name: "oauth", AppName: "alpha-oauth", Port: 38001}},
			Nacos:       NacosSpec{Group: "DEFAULT_GROUP"},
		}},
		AgentIP: "192.168.74.132",
		Client:  server.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "PUT /nacos/v1/ns/instance/beat?") || !strings.Contains(joined, "serviceName=alpha-oauth") {
		t.Fatalf("expected heartbeat request, got:\n%s", joined)
	}
}
