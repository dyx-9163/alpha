package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	case "reconcile-ingress":
		cmd := flag.NewFlagSet("reconcile-ingress", flag.ExitOnError)
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
	fmt.Fprintln(os.Stderr, "usage: aifar-agent health | status | reconcile-ingress --spec <file> | remove-instance [--instance admin] | serve [--addr 127.0.0.1:18081]")
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
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{Log: os.Stdout})
	if err := manager.Load(context.Background()); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := health(r.Context()); err != nil {
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
		if err := health(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
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
		if err := (runtimeagent.Reconciler{Manager: manager, Log: os.Stdout}).ReconcileIngress(r.Context(), spec); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "reconciled"})
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
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("aifar-agent listening on %s", addr)
	return server.ListenAndServe()
}

func postRuntimeSpec(ctx context.Context, addr string, spec runtimeagent.RuntimeSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	url := "http://" + addr + "/runtime/reconcile"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("aifar-agent service is not reachable on %s: %w", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("aifar-agent reconcile failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
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
			"reconcile-ingress",
			"remove-instance",
			"status",
		},
	}
}

func writeAgentStatus(out *os.File) {
	_ = json.NewEncoder(out).Encode(agentStatus())
}
