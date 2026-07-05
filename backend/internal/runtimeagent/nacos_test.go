package runtimeagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestSyncNacosProxyRegistrationsRepairsServiceTypeConflict(t *testing.T) {
	requests := []string{}
	instancePosts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == "/nacos/v1/auth/users/login" {
			_, _ = w.Write([]byte(`{"accessToken":"token-1"}`))
			return
		}
		if r.URL.Path == "/nacos/v1/ns/instance" && r.Method == http.MethodPost {
			instancePosts++
			if instancePosts == 1 {
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
	}); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(requests, "\n")
	firstPost := strings.Index(joined, "POST /nacos/v1/ns/instance?")
	serviceDelete := strings.Index(joined, "DELETE /nacos/v1/ns/service?")
	lastPost := strings.LastIndex(joined, "POST /nacos/v1/ns/instance?")
	if firstPost < 0 || serviceDelete < 0 || lastPost <= firstPost || !(firstPost < serviceDelete && serviceDelete < lastPost) {
		t.Fatalf("expected instance POST, service DELETE, then instance POST retry, got:\n%s", joined)
	}
	for _, want := range []string{
		"serviceName=alpha-oauth",
		"groupName=DEFAULT_GROUP",
		"namespaceId=prod",
		"ephemeral=true",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected Nacos repair request containing %q, got:\n%s", want, joined)
		}
	}
}
