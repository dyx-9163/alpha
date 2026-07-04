package runtimeagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if strings.Contains(call, " inspect ") {
		return CommandResult{Stdout: "true\n"}, nil
	}
	if strings.Contains(call, " port ") {
		return CommandResult{Stdout: "38000/tcp -> 0.0.0.0:38000\n8080/tcp -> 0.0.0.0:8080\n"}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func TestReconcileIngressCreatesNginxContainerAndVerifiesPorts(t *testing.T) {
	tmp := t.TempDir()
	runner := &fakeRunner{}
	spec := RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Ingress: IngressSpec{
			Container:      "aifar-admin-ingress",
			ConfigPath:     filepath.Join(tmp, "nginx.conf"),
			GatewayService: "aifar-svc-admin-gateway",
			WebService:     "aifar-svc-admin-web-vue3",
			GatewayPort:    38000,
			WebPort:        8080,
		},
	}
	if err := (Reconciler{Runner: runner}).ReconcileIngress(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(spec.Ingress.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, want := range []string{
		"server aifar-svc-admin-gateway:38000;",
		"server aifar-svc-admin-web-vue3:8080;",
		"listen 38000;",
		"listen 8080;",
		"proxy_pass http://aifar_gateway_service;",
		"proxy_pass http://aifar_web_service;",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("ingress config missing %q:\n%s", want, config)
		}
	}
	calls := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"docker rm -f aifar-admin-ingress",
		"docker run -d --name aifar-admin-ingress",
		"--label aifar.component=ingress",
		"--label aifar.install-root=/aifar/apps/admin",
		"--network aifar-network",
		"-p 38000:38000",
		"-p 8080:8080",
		"docker exec aifar-admin-ingress nginx -t",
		"docker inspect -f {{.State.Running}} aifar-admin-ingress",
		"docker port aifar-admin-ingress",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("expected docker call containing %q, got:\n%s", want, calls)
		}
	}
}

func TestRenderIngressConfigRoutesWebAPIToGateway(t *testing.T) {
	config := RenderIngressConfig(RuntimeSpec{})
	if !strings.Contains(config, "location /api/") || !strings.Contains(config, "location /im/ws/") {
		t.Fatalf("web ingress should route API and websocket paths to gateway:\n%s", config)
	}
}
