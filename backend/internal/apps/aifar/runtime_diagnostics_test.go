package aifar

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type runtimeDiagnosticStore struct {
	*fakeStore
	exports map[string]store.DiagnosticExport
}

func (s *runtimeDiagnosticStore) SaveDiagnosticExport(v store.DiagnosticExport) (store.DiagnosticExport, error) {
	if s.exports == nil {
		s.exports = map[string]store.DiagnosticExport{}
	}
	s.exports[v.ID] = v
	return v, nil
}

func (s *runtimeDiagnosticStore) GetDiagnosticExport(id string) (store.DiagnosticExport, error) {
	v, ok := s.exports[id]
	if !ok {
		return store.DiagnosticExport{}, errors.New("diagnostic export not found")
	}
	return v, nil
}

type runtimeDiagnosticRemote struct {
	calls   int
	command string
	stdout  string
	stderr  string
	err     error
}

func (r *runtimeDiagnosticRemote) Run(_ context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.calls++
	r.command = command
	return adapter.CommandResult{Stdout: r.stdout, Stderr: r.stderr}, r.err
}

func (*runtimeDiagnosticRemote) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

func TestEstimateRuntimeDiagnosticsRejectsDisabledAndUnknownServices(t *testing.T) {
	now := time.Now().UTC()
	instance := store.AppInstance{
		ID:       "instance-1",
		App:      AppName,
		ServerID: "server-1",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`,
	}
	db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
		{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1},
		{InstanceID: instance.ID, ServiceName: "oauth", DesiredReplicas: 0},
	}}}

	for _, service := range []string{"oauth", "../../etc"} {
		t.Run(service, func(t *testing.T) {
			remote := &runtimeDiagnosticRemote{}
			svc := NewService(db, remote)
			_, err := svc.EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
				Instance: instance,
				Server:   store.Server{ID: "server-1"},
				Services: []string{service},
				SinceAt:  now.Add(-time.Hour),
				UntilAt:  now.Add(-time.Minute),
			}, nil)
			if err == nil {
				t.Fatalf("expected service %q to be rejected", service)
			}
			if remote.calls != 0 {
				t.Fatalf("validation must reject %q before remote execution", service)
			}
		})
	}
}

func TestEstimateRuntimeDiagnosticsRendersTrustedSelectionAndComputesAllowed(t *testing.T) {
	now := time.Now().UTC()
	instance := store.AppInstance{
		ID:       "instance'; echo injected",
		App:      AppName,
		ServerID: "server-1",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin's runtime"}`,
	}
	db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
		{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1},
	}}}
	remote := &runtimeDiagnosticRemote{stdout: strings.Join([]string{
		"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
		"AIFAR_DIAG_TOTAL\t100\t200\t300\t9000000000\t1610613036",
		"AIFAR_DIAG_WARNING\tdocker-log-conservative\tgateway",
	}, "\n")}

	got, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		Instance: instance,
		Server:   store.Server{ID: "server-1"},
		Services: []string{"gateway"},
		SinceAt:  now.Add(-time.Hour),
		UntilAt:  now.Add(-time.Minute),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Allowed || got.TotalBytes != 300 || got.AvailableBytes != 9000000000 {
		t.Fatalf("unexpected estimate: %+v", got)
	}
	for _, want := range []string{
		`INSTALL_ROOT='/aifar/apps/admin'"'"'s runtime'`,
		`INSTANCE_ID='instance'"'"'; echo injected'`,
		`SERVICES='gateway'`,
	} {
		if !strings.Contains(remote.command, want) {
			t.Fatalf("rendered estimate command must quote trusted field %q:\n%s", want, remote.command)
		}
	}
}

func TestRuntimeDiagnosticEstimateRejectsHeredocDelimiterInjectionBeforeRemoteRun(t *testing.T) {
	now := time.Now().UTC()
	base := store.AppInstance{
		ID:       "instance-1",
		App:      AppName,
		ServerID: "server-1",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`,
	}
	tests := map[string]func(*store.AppInstance){
		"instance id": func(instance *store.AppInstance) {
			instance.ID = "instance-1\nAIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE\nprintf pwned"
		},
		"install root": func(instance *store.AppInstance) {
			instance.Metadata = `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin\nAIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE\nprintf pwned"}`
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			instance := base
			mutate(&instance)
			db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
				{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1},
			}}}
			remote := &runtimeDiagnosticRemote{}
			_, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
				Instance: instance,
				Server:   store.Server{ID: "server-1"},
				Services: []string{"gateway"},
				SinceAt:  now.Add(-time.Hour),
				UntilAt:  now.Add(-time.Minute),
			}, nil)
			if err == nil {
				t.Fatal("expected control-character injection to be rejected")
			}
			if remote.calls != 0 {
				t.Fatalf("unsafe heredoc input reached remote execution: calls=%d", remote.calls)
			}
		})
	}
}

func TestRuntimeDiagnosticEstimateScriptFailsClosedOnCandidateDiscovery(t *testing.T) {
	script, err := renderRuntimeDiagnosticEstimateScript(runtimeDiagnosticEstimateScriptData{
		InstallRoot: "'/aifar/apps/admin'",
		InstanceID:  "'instance-1'",
		Services:    "'gateway'",
		SinceUnix:   "'1'",
		UntilUnix:   "'2'",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"for file_size in $(find",
		"for container_id in $(docker ps",
		"docker inspect --format='{{.LogPath}}' \"$container_id\" 2>/dev/null || true",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("estimate script must not swallow candidate discovery failure with %q:\n%s", forbidden, script)
		}
	}
	for _, required := range []string{
		"if ! file_sizes=$(find",
		"if ! container_ids=$(docker ps",
		"if ! log_path=$(docker inspect --format='{{.LogPath}}'",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("estimate script must explicitly fail closed around %q:\n%s", required, script)
		}
	}
}

func TestEstimateRuntimeDiagnosticsRejectsInvalidDomainBeforeRemoteRun(t *testing.T) {
	now := time.Now().UTC()
	validInstance := store.AppInstance{ID: "instance-1", App: AppName, ServerID: "server-1", Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`}
	db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
		{InstanceID: validInstance.ID, ServiceName: "gateway", DesiredReplicas: 1},
	}}}
	valid := RuntimeDiagnosticRequest{
		Instance: validInstance,
		Server:   store.Server{ID: "server-1"},
		Services: []string{"gateway"},
		SinceAt:  now.Add(-time.Hour),
		UntilAt:  now.Add(-time.Minute),
	}
	tests := map[string]func(*RuntimeDiagnosticRequest){
		"legacy instance":      func(req *RuntimeDiagnosticRequest) { req.Instance.Metadata = `{}` },
		"wrong server":         func(req *RuntimeDiagnosticRequest) { req.Server.ID = "server-2" },
		"empty selection":      func(req *RuntimeDiagnosticRequest) { req.Services = nil },
		"unnormalized service": func(req *RuntimeDiagnosticRequest) { req.Services = []string{"Gateway"} },
		"reversed window":      func(req *RuntimeDiagnosticRequest) { req.SinceAt = req.UntilAt },
		"oversized window": func(req *RuntimeDiagnosticRequest) {
			req.SinceAt = req.UntilAt.Add(-runtimeDiagnosticRetention - time.Second)
		},
		"future window": func(req *RuntimeDiagnosticRequest) { req.UntilAt = now.Add(time.Hour) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := valid
			mutate(&req)
			remote := &runtimeDiagnosticRemote{}
			if _, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), req, nil); err == nil {
				t.Fatal("expected invalid request to be rejected")
			}
			if remote.calls != 0 {
				t.Fatal("domain validation must finish before remote execution")
			}
		})
	}
}

func TestEstimateRuntimeDiagnosticsRequiresDiagnosticStoreCapability(t *testing.T) {
	remote := &runtimeDiagnosticRemote{}
	now := time.Now().UTC()
	_, err := NewService(&fakeStore{}, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		Instance: store.AppInstance{ID: "instance-1", App: AppName, ServerID: "server-1", Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`},
		Server:   store.Server{ID: "server-1"},
		Services: []string{"gateway"},
		SinceAt:  now.Add(-time.Hour),
		UntilAt:  now.Add(-time.Minute),
	}, nil)
	if err == nil || remote.calls != 0 {
		t.Fatalf("expected missing diagnostic store capability before remote run, err=%v calls=%d", err, remote.calls)
	}
}

func TestParseRuntimeDiagnosticEstimate(t *testing.T) {
	raw := strings.Join([]string{
		"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
		"AIFAR_DIAG_SERVICE\toauth\t50\t0",
		"AIFAR_DIAG_TOTAL\t150\t200\t350\t9000000000\t1610613086",
		"AIFAR_DIAG_WARNING\tdocker-log-conservative\tgateway",
	}, "\n")
	got, err := parseRuntimeDiagnosticEstimate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalBytes != 350 || got.AvailableBytes != 9000000000 || len(got.Services) != 2 {
		t.Fatalf("unexpected estimate: %+v", got)
	}
}

func TestParseRuntimeDiagnosticEstimateRejectsRequiredBytesMismatchAndOverflow(t *testing.T) {
	maxInt64 := strconv.FormatInt(1<<63-1, 10)
	tests := map[string]string{
		"required bytes mismatch": strings.Join([]string{
			"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
			"AIFAR_DIAG_TOTAL\t100\t200\t300\t9000000000\t300",
		}, "\n"),
		"required bytes overflow": strings.Join([]string{
			"AIFAR_DIAG_SERVICE\tgateway\t" + maxInt64 + "\t0",
			"AIFAR_DIAG_TOTAL\t" + maxInt64 + "\t0\t" + maxInt64 + "\t" + maxInt64 + "\t" + maxInt64,
		}, "\n"),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimeDiagnosticEstimate(raw); err == nil {
				t.Fatal("expected invalid required byte estimate to be rejected")
			}
		})
	}
}

func TestParseRuntimeDiagnosticEstimateRejectsMalformedProtocol(t *testing.T) {
	tests := map[string]string{
		"duplicate total":     "AIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"malformed integer":   "AIFAR_DIAG_SERVICE\tgateway\tbad\t0\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"negative bytes":      "AIFAR_DIAG_SERVICE\tgateway\t-1\t0\nAIFAR_DIAG_TOTAL\t-1\t0\t-1\t1\t1",
		"unknown service":     "AIFAR_DIAG_SERVICE\t../../etc\t0\t0\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"extra service field": "AIFAR_DIAG_SERVICE\tgateway\t0\t0\textra\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"extra total field":   "AIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1\textra",
		"extra warning field": "AIFAR_DIAG_WARNING\tcode\t-\textra\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"unknown line":        "AIFAR_DIAG_OTHER\t0\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"missing total":       "AIFAR_DIAG_SERVICE\tgateway\t0\t0",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimeDiagnosticEstimate(raw); err == nil {
				t.Fatal("expected malformed protocol to be rejected")
			}
		})
	}
}
