package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/runtimeagent"
)

const maxAgentRequestBodyBytes int64 = 1 << 20

const verifiedRuntimeBootstrapPath = "/runtime/bootstrap-verified"

const (
	legacyReconcileRuntimeCommand = "reconcile-runtime"
	legacyRestartRuntimeCommand   = "restart-runtime"
)

var (
	agentInstancePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	agentServicePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	agentErrorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	agentSHA256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	perServiceFeatures    = []string{"service-manifest-v1", "service-generation-v1", "per-service-reconcile", "per-service-restart", "service-conditions-v1", "runtime-instance-snapshot-v1", "durable-legacy-archive-v1", "verified-bootstrap-stream-v1"}
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "health":
		if err := health(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(`{"status":"ok"}`)
	case "status":
		cmd := flag.NewFlagSet("status", flag.ExitOnError)
		addr := cmd.String("addr", "127.0.0.1:18081", "agent API address")
		_ = cmd.Parse(os.Args[2:])
		data, err := getAgentStatus(context.Background(), *addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	case legacyReconcileRuntimeCommand, "reconcile-ingress", "reconcile":
		cmd := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		specPath := cmd.String("spec", "", "path to runtime spec json")
		addr := cmd.String("addr", "127.0.0.1:18081", "agent API address")
		_ = cmd.Parse(os.Args[2:])
		if *specPath == "" {
			fmt.Fprintln(os.Stderr, "--spec is required")
			os.Exit(2)
		}
		spec, err := readSpec(*specPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := postRuntimeSpec(context.Background(), *addr, spec); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(`{"status":"reconciled"}`)
	case legacyRestartRuntimeCommand:
		cmd := flag.NewFlagSet(legacyRestartRuntimeCommand, flag.ExitOnError)
		specPath := cmd.String("spec", "", "path to runtime spec json")
		addr := cmd.String("addr", "127.0.0.1:18081", "agent API address")
		_ = cmd.Parse(os.Args[2:])
		if *specPath == "" {
			fmt.Fprintln(os.Stderr, "--spec is required")
			os.Exit(2)
		}
		spec, err := readSpec(*specPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := postRuntimeRestart(context.Background(), *addr, spec); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(`{"status":"restarted"}`)
	case "apply-deployment", "get-deployment", "get-instance-snapshot", "archive-legacy-runtime", "reconcile-deployment", "bootstrap-runtime", "bootstrap-runtime-stdin":
		if err := runServiceAgentCommandWithInput(os.Args[1], os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "remove-instance":
		cmd := flag.NewFlagSet("remove-instance", flag.ExitOnError)
		instance := cmd.String("instance", "admin", "runtime instance id")
		addr := cmd.String("addr", "127.0.0.1:18081", "agent API address")
		_ = cmd.Parse(os.Args[2:])
		if err := deleteRuntimeInstance(context.Background(), *addr, *instance); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(`{"status":"removed"}`)
	case "register-nacos", "register-nacos-proxies", "deregister-nacos", "deregister-nacos-proxies":
		if err := syncNacosProxiesCommand(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "serve":
		cmd := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := cmd.String("addr", "127.0.0.1:18081", "agent listen address")
		_ = cmd.Parse(os.Args[2:])
		if err := serve(*addr); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, agentUsageText())
}

func agentUsageText() string {
	return "usage: aifar-agent health | status | apply-deployment --manifest <file> | get-deployment --instance <id> --service <name> | get-instance-snapshot --instance <id> | archive-legacy-runtime --instance <id> --sha256 <digest> | reconcile-deployment --instance <id> --service <name> | bootstrap-runtime --spec <file> | bootstrap-runtime-stdin --instance <id> --sha256 <digest> | " +
		legacyReconcileRuntimeCommand + " --spec <file> | " + legacyRestartRuntimeCommand + " --spec <file> | reconcile-ingress --spec <file> | remove-instance [--instance admin] | register-nacos [--state-dir dir] | deregister-nacos [--state-dir dir] | serve [--addr 127.0.0.1:18081]"
}

func readSpec(path string) (runtimeagent.RuntimeSpec, error) {
	var spec runtimeagent.RuntimeSpec
	data, err := os.ReadFile(path)
	if err != nil {
		return spec, err
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

func health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := runtimeagent.ExecRunner{}.Run(ctx, "docker", "info")
	return err
}

func serve(addr string) error {
	if err := validateAgentListenAddress(addr); err != nil {
		return fmt.Errorf("refuse unsafe aifar-agent listen address: %w", err)
	}
	ctx := context.Background()
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{Log: os.Stdout})
	if err := manager.Load(ctx); err != nil {
		return err
	}
	nacosOptions := runtimeagent.NacosProxySyncOptions{
		StateDir: runtimeagent.DefaultStateDir,
		Action:   runtimeagent.NacosProxyRegister,
		Log:      os.Stdout,
	}
	if err := runtimeagent.SyncNacosProxyRegistrations(ctx, nacosOptions); err != nil {
		log.Printf("sync AIFAR Nacos proxies on startup failed: %v", err)
	}
	go runtimeagent.StartNacosProxyHeartbeat(ctx, nacosOptions)
	go manager.StartRuntimeResync(ctx, 15*time.Second, nacosOptions)
	go manager.StartDockerEventSync(ctx, 2*time.Second)
	server := &http.Server{Addr: addr, Handler: newAgentHandler(manager, health), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("aifar-agent listening on %s", addr)
	return server.ListenAndServe()
}

func syncNacosProxiesCommand(name string, args []string) error {
	cmd := flag.NewFlagSet(name, flag.ExitOnError)
	stateDir := cmd.String("state-dir", runtimeagent.DefaultStateDir, "agent runtime state directory")
	specPath := cmd.String("spec", "", "single runtime spec json")
	agentIP := cmd.String("agent-ip", "", "agent IP address registered in Nacos")
	_ = cmd.Parse(args)
	action := runtimeagent.NacosProxyRegister
	if strings.HasPrefix(name, "deregister-") {
		action = runtimeagent.NacosProxyDeregister
	}
	options := runtimeagent.NacosProxySyncOptions{
		StateDir: *stateDir,
		Action:   action,
		AgentIP:  *agentIP,
		Log:      os.Stdout,
	}
	if strings.TrimSpace(*specPath) != "" {
		spec, err := readSpec(*specPath)
		if err != nil {
			return err
		}
		options.Specs = []runtimeagent.RuntimeSpec{spec}
	}
	if err := runtimeagent.SyncNacosProxyRegistrations(context.Background(), options); err != nil {
		return err
	}
	fmt.Printf("{\"status\":\"%s\"}\n", action)
	return nil
}

func newAgentHandler(manager *runtimeagent.Manager, healthCheck func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		if err := healthCheck(r.Context()); err != nil {
			writeAgentError(w, http.StatusServiceUnavailable, "AGENT_UNAVAILABLE", "agent health check failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		status := manager.Status()
		status["features"] = mergeAgentFeatures(status["features"], perServiceFeatures)
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("/runtime/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		var spec runtimeagent.RuntimeSpec
		if !decodeAgentJSON(w, r, &spec) {
			return
		}
		if err := manager.ApplyLegacyRuntimeSpec(r.Context(), spec); err != nil {
			if errors.Is(err, runtimeagent.ErrLegacyRuntimeSpecDisabled) {
				writeAgentError(w, http.StatusConflict, "LEGACY_RUNTIME_SPEC_DISABLED", "legacy runtime spec is disabled", nil)
				return
			}
			if errors.Is(err, runtimeagent.ErrInvalidLegacyRuntimeSpec) {
				writeAgentError(w, http.StatusBadRequest, "INVALID_LEGACY_RUNTIME_SPEC", "legacy runtime spec is invalid", nil)
				return
			}
			writeAgentError(w, http.StatusInternalServerError, "LEGACY_RUNTIME_RECONCILE_FAILED", "legacy runtime reconcile failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "reconciled"})
	})
	mux.HandleFunc("/runtime/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		var spec runtimeagent.LegacyRuntimeSpec
		if !decodeAgentJSON(w, r, &spec) {
			return
		}
		acceptance, err := manager.BootstrapLegacyRuntime(r.Context(), spec)
		if err != nil {
			if errors.Is(err, runtimeagent.ErrLegacyRuntimeSpecDisabled) {
				writeAgentError(w, http.StatusConflict, "LEGACY_RUNTIME_SPEC_DISABLED", "legacy runtime spec is disabled", nil)
				return
			}
			if errors.Is(err, runtimeagent.ErrInvalidLegacyRuntimeSpec) {
				writeAgentError(w, http.StatusBadRequest, "INVALID_LEGACY_RUNTIME_SPEC", "legacy runtime spec is invalid", nil)
				return
			}
			writeAgentError(w, http.StatusInternalServerError, "BOOTSTRAP_RUNTIME_FAILED", "runtime bootstrap failed", nil)
			return
		}
		writeJSON(w, http.StatusAccepted, acceptance)
	})
	mux.HandleFunc(verifiedRuntimeBootstrapPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		var spec runtimeagent.LegacyRuntimeSpec
		if !decodeAgentJSONWithLimit(w, r, &spec, runtimeagent.LegacyBootstrapMaxBytes) {
			return
		}
		acceptance, err := manager.BootstrapLegacyRuntime(r.Context(), spec)
		if err != nil {
			if errors.Is(err, runtimeagent.ErrLegacyRuntimeSpecDisabled) {
				writeAgentError(w, http.StatusConflict, "LEGACY_RUNTIME_SPEC_DISABLED", "legacy runtime spec is disabled", nil)
				return
			}
			if errors.Is(err, runtimeagent.ErrInvalidLegacyRuntimeSpec) {
				writeAgentError(w, http.StatusBadRequest, "INVALID_LEGACY_RUNTIME_SPEC", "legacy runtime spec is invalid", nil)
				return
			}
			writeAgentError(w, http.StatusInternalServerError, "BOOTSTRAP_RUNTIME_FAILED", "runtime bootstrap failed", nil)
			return
		}
		writeJSON(w, http.StatusAccepted, acceptance)
	})
	mux.HandleFunc("/runtime/restart-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		var spec runtimeagent.RuntimeSpec
		if !decodeAgentJSON(w, r, &spec) {
			return
		}
		if err := manager.RestartLegacyRuntimeSpec(r.Context(), spec); err != nil {
			if errors.Is(err, runtimeagent.ErrLegacyRuntimeSpecDisabled) {
				writeAgentError(w, http.StatusConflict, "LEGACY_RUNTIME_SPEC_DISABLED", "legacy runtime spec is disabled", nil)
				return
			}
			if errors.Is(err, runtimeagent.ErrInvalidLegacyRuntimeSpec) {
				writeAgentError(w, http.StatusBadRequest, "INVALID_LEGACY_RUNTIME_SPEC", "legacy runtime spec is invalid", nil)
				return
			}
			writeAgentError(w, http.StatusInternalServerError, "RUNTIME_RESTART_FAILED", "runtime restart failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	})
	mux.HandleFunc("/runtime/instances/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			instance, ok := parseInstanceSnapshotPath(r.URL.EscapedPath())
			if !ok {
				writeAgentError(w, http.StatusBadRequest, "INVALID_INSTANCE_PATH", "runtime instance path is invalid", nil)
				return
			}
			if !ensureEmptyAgentBody(w, r) {
				return
			}
			snapshot, err := manager.RuntimeInstanceSnapshot(instance)
			if err != nil {
				writeAgentError(w, http.StatusConflict, "INSTANCE_SNAPSHOT_UNAVAILABLE", "runtime instance snapshot is unavailable", nil)
				return
			}
			writeJSON(w, http.StatusOK, snapshot)
			return
		}
		if r.Method == http.MethodPost {
			instance, ok := parseInstanceArchivePath(r.URL.EscapedPath())
			if !ok {
				writeAgentError(w, http.StatusBadRequest, "INVALID_INSTANCE_PATH", "runtime instance path is invalid", nil)
				return
			}
			var request struct {
				ExpectedSHA256 string `json:"expectedSHA256"`
			}
			if !decodeAgentJSON(w, r, &request) {
				return
			}
			if err := manager.ArchiveLegacyRuntimeSpec(instance, request.ExpectedSHA256); err != nil {
				writeAgentError(w, http.StatusConflict, "LEGACY_RUNTIME_ARCHIVE_FAILED", "legacy runtime archive failed", nil)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"archived": true})
			return
		}
		if r.Method != http.MethodDelete {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		instance, ok := parseRemoveInstancePath(r.URL.EscapedPath())
		if !ok {
			writeAgentError(w, http.StatusBadRequest, "INVALID_INSTANCE_PATH", "runtime instance path is invalid", nil)
			return
		}
		if err := manager.Remove(r.Context(), instance); err != nil {
			writeAgentError(w, http.StatusInternalServerError, "REMOVE_INSTANCE_FAILED", "runtime instance removal failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "route was not found", nil)
	})
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.EscapedPath(), "/runtime/instances/") && strings.Contains(r.URL.EscapedPath(), "/deployments/") {
			handleDeploymentRequest(manager, w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return recoverAgentHandler(router)
}

func recoverAgentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("aifar-agent handler panic path=%s\n%s", r.URL.Path, debug.Stack())
				writeAgentError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "aifar-agent handler panic recovered", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func postRuntimeSpec(ctx context.Context, addr string, spec runtimeagent.RuntimeSpec) error {
	return postRuntimeRequest(ctx, addr, "/runtime/reconcile", "reconcile", spec)
}

func postRuntimeRestart(ctx context.Context, addr string, spec runtimeagent.RuntimeSpec) error {
	return postRuntimeRequestOnce(ctx, addr, "/runtime/restart-all", "restart", spec)
}

func postRuntimeRequestOnce(ctx context.Context, addr, path, operation string, spec runtimeagent.RuntimeSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("aifar-agent service is not reachable on %s: %w", addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return sanitizedAgentResponseError(operation, resp.Status, body)
	}
	return nil
}

func postRuntimeRequest(ctx context.Context, addr, path, operation string, spec runtimeagent.RuntimeSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	url := "http://" + addr + path
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("aifar-agent service is not reachable on %s: %w", addr, err)
			if !isTransientAgentRequestError(err) || attempt == 5 || !sleepAgentRetry(ctx, attempt) {
				return lastErr
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			lastErr = sanitizedAgentResponseError(operation, resp.Status, body)
			if !isTransientAgentStatus(resp.StatusCode) || attempt == 5 || !sleepAgentRetry(ctx, attempt) {
				return lastErr
			}
			continue
		}
		return nil
	}
	return lastErr
}

func isTransientAgentRequestError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "eof") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "connection refused") ||
		strings.Contains(text, "server closed idle connection")
}

func isTransientAgentStatus(status int) bool {
	return status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func sleepAgentRetry(ctx context.Context, attempt int) bool {
	delay := time.Duration(attempt) * time.Second
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func deleteRuntimeInstance(ctx context.Context, addr, instance string) error {
	instance = strings.TrimSpace(instance)
	if !validAgentInstanceID(instance) {
		return errors.New("runtime instance identity is invalid")
	}
	url := "http://" + addr + "/runtime/instances/" + url.PathEscape(instance)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("aifar-agent service is not reachable on %s: %w", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return sanitizedAgentResponseError("remove instance", resp.Status, body)
	}
	return nil
}

func getAgentStatus(ctx context.Context, addr string) ([]byte, error) {
	url := "http://" + addr + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aifar-agent service is not reachable on %s: %w", addr, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, sanitizedAgentResponseError("status", resp.Status, data)
	}
	return data, nil
}

type agentErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

func handleDeploymentRequest(manager *runtimeagent.Manager, w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(r.URL.EscapedPath(), "/")
	if (len(pathParts) != 6 && len(pathParts) != 7) || (len(pathParts) == 7 && pathParts[6] != "reconcile") {
		writeAgentError(w, http.StatusNotFound, "NOT_FOUND", "route was not found", nil)
		return
	}
	instanceID, serviceName, reconcile, ok := parseDeploymentPath(r.URL.EscapedPath())
	if !ok {
		writeAgentError(w, http.StatusBadRequest, "INVALID_DEPLOYMENT_PATH", "deployment path is invalid", nil)
		return
	}
	if reconcile {
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		if !ensureEmptyAgentBody(w, r) {
			return
		}
		if _, exists := manager.DeploymentState(instanceID, serviceName); !exists {
			writeAgentError(w, http.StatusNotFound, "DEPLOYMENT_NOT_FOUND", "deployment was not found", nil)
			return
		}
		manager.ReconcileDeployment(instanceID, serviceName)
		writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
		return
	}

	switch r.Method {
	case http.MethodGet:
		state, exists := manager.DeploymentState(instanceID, serviceName)
		if !exists {
			writeAgentError(w, http.StatusNotFound, "DEPLOYMENT_NOT_FOUND", "deployment was not found", nil)
			return
		}
		writeJSON(w, http.StatusOK, state)
	case http.MethodPut:
		var manifest runtimeagent.DeploymentManifest
		if !decodeAgentJSON(w, r, &manifest) {
			return
		}
		if manifest.APIVersion != runtimeagent.ManifestAPIVersion || manifest.Kind != runtimeagent.DeploymentManifestKind {
			writeAgentError(w, http.StatusBadRequest, "INVALID_DEPLOYMENT_MANIFEST", "deployment manifest schema is invalid", nil)
			return
		}
		if strings.TrimSpace(manifest.Metadata.InstanceID) != instanceID ||
			strings.ToLower(strings.TrimSpace(manifest.Metadata.Name)) != serviceName ||
			strings.ToLower(strings.TrimSpace(manifest.Spec.ServiceName)) != serviceName ||
			strings.ToLower(strings.TrimSpace(manifest.Service.Name)) != serviceName {
			writeAgentError(w, http.StatusBadRequest, "DEPLOYMENT_IDENTITY_MISMATCH", "deployment identity does not match request path", nil)
			return
		}
		acceptance, err := manager.AcceptDeployment(r.Context(), manifest)
		if err != nil {
			err = manager.ClassifyDeploymentAcceptanceError(manifest, err)
			switch {
			case errors.Is(err, runtimeagent.ErrStaleDeploymentGeneration):
				writeAgentError(w, http.StatusConflict, "STALE_DEPLOYMENT_GENERATION", "deployment generation is stale", map[string]any{"currentGeneration": acceptance.Generation, "currentSpecHash": acceptance.SpecHash})
			case errors.Is(err, runtimeagent.ErrDeploymentGenerationConflict):
				writeAgentError(w, http.StatusConflict, "DEPLOYMENT_GENERATION_CONFLICT", "deployment generation already has a different specification", map[string]any{"currentGeneration": acceptance.Generation, "currentSpecHash": acceptance.SpecHash})
			case errors.Is(err, runtimeagent.ErrInvalidDeploymentManifest):
				writeAgentError(w, http.StatusBadRequest, "INVALID_DEPLOYMENT_MANIFEST", "deployment manifest is invalid", nil)
			default:
				writeAgentError(w, http.StatusInternalServerError, "AGENT_STATE_PERSISTENCE_FAILED", "agent state persistence failed", nil)
			}
			return
		}
		writeJSON(w, http.StatusAccepted, acceptance)
	default:
		writeAgentError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}

func parseDeploymentPath(escapedPath string) (string, string, bool, bool) {
	parts := strings.Split(escapedPath, "/")
	if len(parts) != 6 && len(parts) != 7 {
		return "", "", false, false
	}
	if parts[0] != "" || parts[1] != "runtime" || parts[2] != "instances" || parts[4] != "deployments" {
		return "", "", false, false
	}
	if len(parts) == 7 && parts[6] != "reconcile" {
		return "", "", false, false
	}
	instanceID, err := url.PathUnescape(parts[3])
	if err != nil || instanceID != parts[3] || !agentInstancePattern.MatchString(instanceID) || instanceID == "." || instanceID == ".." {
		return "", "", false, false
	}
	serviceName, err := url.PathUnescape(parts[5])
	if err != nil || serviceName != parts[5] || !agentServicePattern.MatchString(serviceName) {
		return "", "", false, false
	}
	return instanceID, serviceName, len(parts) == 7, true
}

func parseRemoveInstancePath(escapedPath string) (string, bool) {
	parts := strings.Split(escapedPath, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "runtime" || parts[2] != "instances" {
		return "", false
	}
	instanceID, err := url.PathUnescape(parts[3])
	if err != nil || instanceID != parts[3] || !validAgentInstanceID(instanceID) {
		return "", false
	}
	return instanceID, true
}

func ensureEmptyAgentBody(w http.ResponseWriter, r *http.Request) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) != 0 {
		writeAgentError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "request body must be empty", nil)
		return false
	}
	return true
}

func decodeAgentJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	return decodeAgentJSONWithLimit(w, r, target, maxAgentRequestBodyBytes)
}

func decodeAgentJSONWithLimit(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAgentError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json", nil)
		return false
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAgentError(w, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "request body exceeds the size limit", nil)
		} else {
			writeAgentError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "request body is invalid", nil)
		}
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeAgentError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "request body must contain one JSON value", nil)
		return false
	}
	return true
}

func writeAgentError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, agentErrorResponse{Code: code, Message: message, Details: details})
}

func runServiceAgentCommand(name string, args []string, out io.Writer) error {
	return runServiceAgentCommandWithInput(name, args, bytes.NewReader(nil), out)
}

func runServiceAgentCommandWithInput(name string, args []string, in io.Reader, out io.Writer) error {
	command := flag.NewFlagSet(name, flag.ContinueOnError)
	command.SetOutput(io.Discard)
	addr := command.String("addr", "127.0.0.1:18081", "agent API address")
	instanceID := command.String("instance", "", "runtime instance id")
	serviceName := command.String("service", "", "runtime service name")
	manifestPath := command.String("manifest", "", "deployment manifest json")
	specPath := command.String("spec", "", "legacy runtime spec json")
	expectedSHA256 := command.String("sha256", "", "expected legacy runtime spec SHA-256")
	if err := command.Parse(args); err != nil {
		return errors.New("invalid command flags")
	}
	if command.NArg() != 0 {
		return errors.New("positional command arguments are not allowed")
	}
	if err := validateLoopbackAgentAddress(*addr); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch name {
	case "apply-deployment":
		if strings.TrimSpace(*manifestPath) == "" || *instanceID != "" || *serviceName != "" || *specPath != "" || *expectedSHA256 != "" {
			return errors.New("--manifest is required")
		}
		manifest, err := readDeploymentManifest(*manifestPath)
		if err != nil {
			return errors.New("deployment manifest file is invalid")
		}
		instance := strings.TrimSpace(manifest.Metadata.InstanceID)
		service := strings.ToLower(strings.TrimSpace(manifest.Metadata.Name))
		if !validAgentIdentity(instance, service) {
			return errors.New("deployment manifest identity is invalid")
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			return errors.New("deployment manifest file is invalid")
		}
		return doAgentTypedRequest(ctx, *addr, http.MethodPut, deploymentAgentPath(instance, service, false), data, out)
	case "get-deployment", "reconcile-deployment":
		if *manifestPath != "" || *specPath != "" || *expectedSHA256 != "" {
			return errors.New("unsupported flags for deployment command")
		}
		instance := strings.TrimSpace(*instanceID)
		service := strings.ToLower(strings.TrimSpace(*serviceName))
		if !validAgentIdentity(instance, service) {
			return errors.New("--instance and --service are required and must be valid")
		}
		method := http.MethodGet
		reconcile := false
		if name == "reconcile-deployment" {
			method = http.MethodPost
			reconcile = true
		}
		return doAgentTypedRequest(ctx, *addr, method, deploymentAgentPath(instance, service, reconcile), nil, out)
	case "get-instance-snapshot":
		if *manifestPath != "" || *specPath != "" || *serviceName != "" || *expectedSHA256 != "" {
			return errors.New("unsupported flags for instance snapshot command")
		}
		instance := strings.TrimSpace(*instanceID)
		if !validAgentInstanceID(instance) {
			return errors.New("--instance is required and must be valid")
		}
		return doAgentTypedRequest(ctx, *addr, http.MethodGet, instanceSnapshotAgentPath(instance), nil, out)
	case "archive-legacy-runtime":
		if *manifestPath != "" || *specPath != "" || *serviceName != "" {
			return errors.New("unsupported flags for legacy runtime archive command")
		}
		instance := strings.TrimSpace(*instanceID)
		digest := strings.ToLower(strings.TrimSpace(*expectedSHA256))
		if !validAgentInstanceID(instance) || !agentSHA256Pattern.MatchString(digest) {
			return errors.New("--instance and --sha256 are required and must be valid")
		}
		data, _ := json.Marshal(map[string]string{"expectedSHA256": digest})
		return doAgentTypedRequest(ctx, *addr, http.MethodPost, instanceArchiveAgentPath(instance), data, out)
	case "bootstrap-runtime":
		if strings.TrimSpace(*specPath) == "" || *manifestPath != "" || *instanceID != "" || *serviceName != "" || *expectedSHA256 != "" {
			return errors.New("--spec is required")
		}
		spec, err := readLegacyRuntimeSpec(*specPath)
		if err != nil {
			return errors.New("legacy runtime spec file is invalid")
		}
		data, err := json.Marshal(spec)
		if err != nil {
			return errors.New("legacy runtime spec file is invalid")
		}
		return doAgentTypedRequest(ctx, *addr, http.MethodPost, "/runtime/bootstrap", data, out)
	case "bootstrap-runtime-stdin":
		if *manifestPath != "" || *specPath != "" || *serviceName != "" {
			return errors.New("unsupported flags for streamed runtime bootstrap")
		}
		instance := strings.TrimSpace(*instanceID)
		digest := strings.ToLower(strings.TrimSpace(*expectedSHA256))
		if !validAgentInstanceID(instance) || !agentSHA256Pattern.MatchString(digest) {
			return errors.New("--instance and --sha256 are required and must be valid")
		}
		if in == nil {
			return errors.New("runtime bootstrap input is required")
		}
		data, err := io.ReadAll(io.LimitReader(in, runtimeagent.LegacyBootstrapMaxBytes+1))
		if err != nil || len(data) > runtimeagent.LegacyBootstrapMaxBytes {
			return errors.New("runtime bootstrap input is invalid")
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != digest {
			return errors.New("runtime bootstrap input hash differs")
		}
		var spec runtimeagent.LegacyRuntimeSpec
		if err := decodeBoundedStrictJSON(data, &spec); err != nil || strings.TrimSpace(spec.InstanceID) != instance {
			return errors.New("runtime bootstrap input is invalid")
		}
		return doAgentTypedRequest(ctx, *addr, http.MethodPost, verifiedRuntimeBootstrapPath, data, out)
	default:
		return errors.New("unsupported service agent command")
	}
}

func readDeploymentManifest(path string) (runtimeagent.DeploymentManifest, error) {
	var manifest runtimeagent.DeploymentManifest
	err := readBoundedStrictJSONFile(path, &manifest)
	return manifest, err
}

func readLegacyRuntimeSpec(path string) (runtimeagent.LegacyRuntimeSpec, error) {
	var spec runtimeagent.LegacyRuntimeSpec
	err := readBoundedStrictJSONFile(path, &spec)
	return spec, err
}

func readBoundedStrictJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	limited := io.LimitReader(file, maxAgentRequestBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(data)) > maxAgentRequestBodyBytes {
		return errors.New("JSON file exceeds size limit")
	}
	return decodeBoundedStrictJSON(data, target)
}

func decodeBoundedStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON file must contain one value")
	}
	return nil
}

func validateLoopbackAgentAddress(addr string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return errors.New("agent address must be a loopback host and port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("agent address port is invalid")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("agent address must use a loopback host")
	}
	return nil
}

func validateAgentListenAddress(addr string) error {
	return validateLoopbackAgentAddress(addr)
}

func validAgentIdentity(instanceID, serviceName string) bool {
	return validAgentInstanceID(instanceID) && agentServicePattern.MatchString(serviceName)
}

func validAgentInstanceID(instanceID string) bool {
	return agentInstancePattern.MatchString(instanceID) && instanceID != "." && instanceID != ".."
}

func deploymentAgentPath(instanceID, serviceName string, reconcile bool) string {
	path := "/runtime/instances/" + url.PathEscape(instanceID) + "/deployments/" + url.PathEscape(serviceName)
	if reconcile {
		path += "/reconcile"
	}
	return path
}

func instanceSnapshotAgentPath(instanceID string) string {
	return "/runtime/instances/" + url.PathEscape(instanceID) + "/snapshot"
}

func instanceArchiveAgentPath(instanceID string) string {
	return "/runtime/instances/" + url.PathEscape(instanceID) + "/archive-legacy-runtime"
}

func parseInstanceSnapshotPath(escapedPath string) (string, bool) {
	parts := strings.Split(escapedPath, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "runtime" || parts[2] != "instances" || parts[4] != "snapshot" {
		return "", false
	}
	instanceID, err := url.PathUnescape(parts[3])
	if err != nil || instanceID != parts[3] || !validAgentInstanceID(instanceID) {
		return "", false
	}
	return instanceID, true
}

func parseInstanceArchivePath(escapedPath string) (string, bool) {
	parts := strings.Split(escapedPath, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "runtime" || parts[2] != "instances" || parts[4] != "archive-legacy-runtime" {
		return "", false
	}
	instanceID, err := url.PathUnescape(parts[3])
	if err != nil || instanceID != parts[3] || !validAgentInstanceID(instanceID) {
		return "", false
	}
	return instanceID, true
}

func doAgentTypedRequest(ctx context.Context, addr, method, path string, body []byte, out io.Writer) error {
	request, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot create aifar-agent request")
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return errors.New("aifar-agent service is not reachable")
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxAgentRequestBodyBytes+1))
	if readErr != nil || int64(len(data)) > maxAgentRequestBodyBytes {
		return errors.New("aifar-agent response is invalid")
	}
	if response.StatusCode >= 300 {
		var typed agentErrorResponse
		code := "UNKNOWN_ERROR"
		if json.Unmarshal(data, &typed) == nil && agentErrorCodePattern.MatchString(typed.Code) {
			code = typed.Code
		}
		return fmt.Errorf("aifar-agent request failed: %s %s", response.Status, code)
	}
	if out != nil {
		_, _ = out.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			_, _ = io.WriteString(out, "\n")
		}
	}
	return nil
}

func sanitizedAgentResponseError(operation, status string, data []byte) error {
	var typed agentErrorResponse
	code := "UNKNOWN_ERROR"
	if json.Unmarshal(data, &typed) == nil && agentErrorCodePattern.MatchString(typed.Code) {
		code = typed.Code
	}
	return fmt.Errorf("aifar-agent %s failed: %s %s", operation, status, code)
}

func mergeAgentFeatures(existing any, additions []string) []string {
	seen := map[string]bool{}
	features := make([]string, 0)
	appendFeature := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		features = append(features, value)
	}
	switch values := existing.(type) {
	case []string:
		for _, value := range values {
			appendFeature(value)
		}
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				appendFeature(text)
			}
		}
	}
	for _, value := range additions {
		appendFeature(value)
	}
	return features
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func agentStatus() map[string]any {
	return map[string]any{
		"status":  "running",
		"version": runtimeagent.DefaultAgentVersion,
		"features": mergeAgentFeatures([]string{
			"health",
			"host-proxy",
			"nacos-proxy-deregister",
			"nacos-proxy-register",
			"reconcile-ingress",
			"reconcile-runtime",
			"restart-runtime",
			"remove-instance",
			"status",
		}, perServiceFeatures),
	}
}

func writeAgentStatus(out *os.File) {
	_ = json.NewEncoder(out).Encode(agentStatus())
}
