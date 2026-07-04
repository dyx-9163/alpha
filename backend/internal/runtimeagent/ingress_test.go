package runtimeagent

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if strings.Contains(call, " ps ") {
		return CommandResult{Stdout: "aifar-pod-admin-gateway-r1\n"}, nil
	}
	if strings.Contains(call, " inspect ") {
		return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func TestManagerAppliesRuntimeSpecAsHostListeners(t *testing.T) {
	gatewayPort := freePort(t)
	webPort := freePort(t)
	runner := &fakeRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "gateway", AppName: "alpha-gateway", Port: gatewayPort},
			{Name: "web-vue3", Port: webPort},
		},
		Ingress: IngressSpec{
			GatewayService: "gateway",
			WebService:     "web-vue3",
			GatewayPort:    gatewayPort,
			WebPort:        webPort,
		},
	}
	if err := manager.Apply(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	listeners, _ := status["listeners"].([]int)
	if len(listeners) != 2 || listeners[0] != gatewayPort || listeners[1] != webPort {
		t.Fatalf("expected host listeners on gateway/web ports, got %#v", status["listeners"])
	}
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerDiscoversReadyDockerPodEndpoints(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "gateway", Port: 38000},
			{Name: "web-vue3", Port: 8080},
		},
	})
	endpoints, err := manager.discoverEndpoints(context.Background(), spec, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].Address != "172.20.0.10:38000" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
	calls := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"docker ps --filter label=aifar.app=aifar",
		"--filter label=aifar.component=pod",
		"--filter label=aifar.service=gateway",
		"docker inspect -f",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("expected docker call containing %q, got:\n%s", want, calls)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
