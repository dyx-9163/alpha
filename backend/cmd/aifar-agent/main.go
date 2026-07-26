package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"aifar-deployment/backend/internal/runtimeagent"
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
	case "reconcile-runtime", "reconcile-ingress", "reconcile":
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
	case "restart-runtime":
		cmd := flag.NewFlagSet("restart-runtime", flag.ExitOnError)
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
	fmt.Fprintln(os.Stderr, "usage: aifar-agent health | status | reconcile-runtime --spec <file> | restart-runtime --spec <file> | reconcile-ingress --spec <file> | remove-instance [--instance admin] | register-nacos [--state-dir dir] | deregister-nacos [--state-dir dir] | serve [--addr 127.0.0.1:18081]")
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
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := healthCheck(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, manager.Status())
	})
	mux.HandleFunc("/runtime/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var spec runtimeagent.RuntimeSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := (runtimeagent.Reconciler{Manager: manager, Log: os.Stdout}).ReconcileRuntime(r.Context(), spec); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "reconciled"})
	})
	mux.HandleFunc("/runtime/restart-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var spec runtimeagent.RuntimeSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := manager.RestartAll(r.Context(), spec); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	})
	mux.HandleFunc("/runtime/instances/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		instance := strings.Trim(strings.TrimPrefix(r.URL.Path, "/runtime/instances/"), "/")
		if instance == "" {
			http.Error(w, "instance is required", http.StatusBadRequest)
			return
		}
		if err := manager.Remove(r.Context(), instance); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	})
	return recoverAgentHandler(mux)
}

func recoverAgentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("aifar-agent handler panic path=%s: %v\n%s", r.URL.Path, recovered, debug.Stack())
				http.Error(w, fmt.Sprintf("aifar-agent handler panic: %v", recovered), http.StatusInternalServerError)
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
		return fmt.Errorf("aifar-agent %s failed: %s: %s", operation, resp.Status, strings.TrimSpace(string(body)))
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
			lastErr = fmt.Errorf("aifar-agent %s failed: %s: %s", operation, resp.Status, strings.TrimSpace(string(body)))
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
	url := "http://" + addr + "/runtime/instances/" + instance
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
		return fmt.Errorf("aifar-agent remove instance failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
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
		return nil, fmt.Errorf("aifar-agent status failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
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
		"features": []string{
			"health",
			"host-proxy",
			"nacos-proxy-deregister",
			"nacos-proxy-register",
			"reconcile-ingress",
			"reconcile-runtime",
			"restart-runtime",
			"remove-instance",
			"status",
		},
	}
}

func writeAgentStatus(out *os.File) {
	_ = json.NewEncoder(out).Encode(agentStatus())
}
