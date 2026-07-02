package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderNacosRuntimeConfigSentinelAndDatasource(t *testing.T) {
	content := renderNacosRuntimeConfig(nacosConfigDocument{
		Datasource: &nacosDatasourceConfig{
			Host:     "10.0.0.10",
			Port:     6446,
			Database: "aifar_admin",
			Username: "root",
			Password: "db-secret",
		},
		Redis: &nacosRedisConfig{
			Topology: "sentinel",
			Password: "redis-secret",
			Database: 1,
			Master:   "aifar-master",
			Nodes:    []string{"10.0.0.11:26379", "10.0.0.12:26379"},
		},
	})

	for _, want := range []string{
		"spring:",
		"      sentinel:",
		"        master: aifar-master",
		"          - 10.0.0.11:26379",
		"    host: 10.0.0.10",
		"    port: 6446",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, content)
		}
	}
}

func TestRedactNacosConfigSecrets(t *testing.T) {
	content := "spring:\n  datasource:\n    password: db-secret\nminio:\n  - access-key: admin\n    secret-key: minio-secret\n"
	redacted := redactNacosConfigSecrets(content)
	if strings.Contains(redacted, "db-secret") || strings.Contains(redacted, "minio-secret") || strings.Contains(redacted, "admin") {
		t.Fatalf("redacted content leaked secret:\n%s", redacted)
	}
	if count := strings.Count(redacted, "******"); count != 3 {
		t.Fatalf("redacted count = %d, want 3:\n%s", count, redacted)
	}
}

func TestNacosConfigClientLoginErrorIsUserFriendly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nacos/v1/auth/users/login" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		http.Error(w, "user not found!", http.StatusForbidden)
	}))
	defer server.Close()

	client := newNacosConfigClient(nacosEndpointConfig{
		BaseURL:  server.URL,
		Username: "nacos",
		Password: "secret-password",
	})
	_, err := client.GetConfig(context.Background(), "prod", "DEFAULT_GROUP", "datasource.yml")
	if err == nil {
		t.Fatal("expected login error")
	}
	text := nacosConfigErrorText("en", err)
	if !strings.Contains(text, "saved user exists in Nacos") || !strings.Contains(text, "403") || !strings.Contains(text, "user not found") {
		t.Fatalf("unexpected friendly error: %s", text)
	}
	if strings.Contains(text, "secret-password") {
		t.Fatalf("login error leaked password: %s", text)
	}
}
