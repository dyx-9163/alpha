package runtimeagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Reconciler struct {
	Runner CommandRunner
	Log    io.Writer
}

func (r Reconciler) ReconcileIngress(ctx context.Context, spec RuntimeSpec) error {
	spec = NormalizeSpec(spec)
	if err := validateIngressSpec(spec); err != nil {
		return err
	}
	runner := r.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if err := os.MkdirAll(filepath.Dir(spec.Ingress.ConfigPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(spec.Ingress.ConfigPath, []byte(RenderIngressConfig(spec)), 0o644); err != nil {
		return err
	}
	_, _ = runner.Run(ctx, "docker", "rm", "-f", spec.Ingress.Container)
	args := []string{
		"run", "-d",
		"--name", spec.Ingress.Container,
		"--restart", "unless-stopped",
		"--network", spec.Network,
	}
	for _, label := range sortedLabels(spec.Ingress.Labels) {
		args = append(args, "--label", label)
	}
	args = append(args,
		"-p", fmt.Sprintf("%d:%d", spec.Ingress.GatewayPort, spec.Ingress.GatewayPort),
		"-p", fmt.Sprintf("%d:%d", spec.Ingress.WebPort, spec.Ingress.WebPort),
		"-v", spec.Ingress.ConfigPath+":/etc/nginx/nginx.conf:ro",
		spec.Ingress.Image,
	)
	if _, err := runner.Run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("start ingress container: %w", err)
	}
	if _, err := runner.Run(ctx, "docker", "exec", spec.Ingress.Container, "nginx", "-t"); err != nil {
		return fmt.Errorf("validate ingress nginx config: %w", err)
	}
	if err := verifyIngress(ctx, runner, spec); err != nil {
		return err
	}
	logf(r.Log, "AIFAR agent reconciled ingress %s on %s\n", spec.Ingress.Container, spec.Network)
	return nil
}

func validateIngressSpec(spec RuntimeSpec) error {
	if strings.TrimSpace(spec.Network) == "" {
		return errors.New("runtime network is required")
	}
	if strings.TrimSpace(spec.Ingress.Container) == "" {
		return errors.New("ingress container is required")
	}
	if strings.TrimSpace(spec.Ingress.Image) == "" {
		return errors.New("ingress image is required")
	}
	if strings.TrimSpace(spec.Ingress.ConfigPath) == "" {
		return errors.New("ingress config path is required")
	}
	if strings.TrimSpace(spec.Ingress.GatewayService) == "" || strings.TrimSpace(spec.Ingress.WebService) == "" {
		return errors.New("ingress upstream services are required")
	}
	if spec.Ingress.GatewayPort <= 0 || spec.Ingress.WebPort <= 0 {
		return errors.New("ingress ports must be positive")
	}
	if spec.Ingress.GatewayPort == spec.Ingress.WebPort {
		return errors.New("gateway and web ingress ports must be different")
	}
	return nil
}

func verifyIngress(ctx context.Context, runner CommandRunner, spec RuntimeSpec) error {
	running, err := runner.Run(ctx, "docker", "inspect", "-f", "{{.State.Running}}", spec.Ingress.Container)
	if err != nil {
		return fmt.Errorf("inspect ingress container: %w", err)
	}
	if strings.TrimSpace(running.Stdout) != "true" {
		return fmt.Errorf("ingress container is not running: %s", spec.Ingress.Container)
	}
	ports, err := runner.Run(ctx, "docker", "port", spec.Ingress.Container)
	if err != nil {
		return fmt.Errorf("inspect ingress ports: %w", err)
	}
	if !strings.Contains(ports.Stdout, fmt.Sprintf("%d/tcp", spec.Ingress.GatewayPort)) {
		return fmt.Errorf("ingress does not publish gateway port %d", spec.Ingress.GatewayPort)
	}
	if !strings.Contains(ports.Stdout, fmt.Sprintf("%d/tcp", spec.Ingress.WebPort)) {
		return fmt.Errorf("ingress does not publish web port %d", spec.Ingress.WebPort)
	}
	return nil
}

func RenderIngressConfig(spec RuntimeSpec) string {
	spec = NormalizeSpec(spec)
	return fmt.Sprintf(`events {}
http {
  map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
  }
  upstream aifar_gateway_service {
    server %s:%d;
  }
  upstream aifar_web_service {
    server %s:%d;
  }
  server {
    listen %d;
    location / {
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_pass http://aifar_gateway_service;
    }
  }
  server {
    listen %d;
    location /api/ {
      proxy_pass http://aifar_gateway_service;
    }
    location /im/ws/ {
      proxy_http_version 1.1;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection $connection_upgrade;
      proxy_pass http://aifar_gateway_service;
    }
    location / {
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_pass http://aifar_web_service;
    }
  }
}
`, spec.Ingress.GatewayService, spec.Ingress.GatewayPort, spec.Ingress.WebService, spec.Ingress.WebPort, spec.Ingress.GatewayPort, spec.Ingress.WebPort)
}

func sortedLabels(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out = append(out, key+"="+labels[key])
	}
	return out
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}
