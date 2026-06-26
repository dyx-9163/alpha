package adapter

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
)

type DockerSummary struct {
	Containers int    `json:"containers"`
	Images     int    `json:"images"`
	Networks   int    `json:"networks"`
	Volumes    int    `json:"volumes"`
	Running    int    `json:"running"`
	Driver     string `json:"driver,omitempty"`
	Version    string `json:"version,omitempty"`
	RootDir    string `json:"rootDir,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
}

type DockerContainer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	State     string `json:"state"`
	Status    string `json:"status"`
	Ports     string `json:"ports"`
	Networks  string `json:"networks"`
	CreatedAt string `json:"createdAt"`
}

type DockerImage struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Size       string `json:"size"`
	CreatedAt  string `json:"createdAt"`
	Digest     string `json:"digest"`
}

type DockerNetwork struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

type DockerVolume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Mountpoint string `json:"mountpoint"`
	Size       string `json:"size"`
}

type DockerDiskUsage struct {
	Type        string `json:"type"`
	Total       string `json:"total"`
	Active      string `json:"active"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

func DockerPing(ctx context.Context, host string) error {
	return dockerCommand(ctx, host, "version", "--format", "{{json .Server}}").Run()
}

func DockerSummaryForHost(ctx context.Context, host string) (DockerSummary, error) {
	out, err := dockerCommand(ctx, host, "info", "--format", "{{json .}}").Output()
	if err != nil {
		return DockerSummary{}, err
	}
	var info struct {
		Containers        any    `json:"Containers"`
		Images            any    `json:"Images"`
		ContainersRunning any    `json:"ContainersRunning"`
		Driver            string `json:"Driver"`
		ServerVersion     string `json:"ServerVersion"`
		DockerRootDir     string `json:"DockerRootDir"`
	}
	_ = json.Unmarshal(out, &info)
	networks, _ := DockerNetworks(ctx, host)
	volumes, _ := DockerVolumes(ctx, host)
	return DockerSummary{
		Containers: toInt(info.Containers),
		Images:     toInt(info.Images),
		Running:    toInt(info.ContainersRunning),
		Networks:   len(networks),
		Volumes:    len(volumes),
		Driver:     info.Driver,
		Version:    info.ServerVersion,
		RootDir:    info.DockerRootDir,
		Endpoint:   dockerEndpoint(host),
	}, nil
}

func DockerContainers(ctx context.Context, host string) ([]DockerContainer, error) {
	out, err := dockerCommand(ctx, host, "ps", "-a", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID        string `json:"ID"`
		Names     string `json:"Names"`
		Image     string `json:"Image"`
		State     string `json:"State"`
		Status    string `json:"Status"`
		Ports     string `json:"Ports"`
		Networks  string `json:"Networks"`
		CreatedAt string `json:"CreatedAt"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerContainer, 0, len(rows))
	for _, row := range rows {
		items = append(items, DockerContainer{
			ID: row.ID, Name: row.Names, Image: row.Image, State: row.State,
			Status: row.Status, Ports: row.Ports, Networks: row.Networks, CreatedAt: row.CreatedAt,
		})
	}
	return items, nil
}

func DockerImages(ctx context.Context, host string) ([]DockerImage, error) {
	out, err := dockerCommand(ctx, host, "images", "--digests", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID         string `json:"ID"`
		Repository string `json:"Repository"`
		Tag        string `json:"Tag"`
		Size       string `json:"Size"`
		CreatedAt  string `json:"CreatedAt"`
		Digest     string `json:"Digest"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerImage, 0, len(rows))
	for _, row := range rows {
		items = append(items, DockerImage{
			ID: row.ID, Repository: row.Repository, Tag: row.Tag, Size: row.Size, CreatedAt: row.CreatedAt, Digest: row.Digest,
		})
	}
	return items, nil
}

func DockerNetworks(ctx context.Context, host string) ([]DockerNetwork, error) {
	out, err := dockerCommand(ctx, host, "network", "ls", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID     string `json:"ID"`
		Name   string `json:"Name"`
		Driver string `json:"Driver"`
		Scope  string `json:"Scope"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerNetwork, 0, len(rows))
	for _, row := range rows {
		items = append(items, DockerNetwork{ID: row.ID, Name: row.Name, Driver: row.Driver, Scope: row.Scope})
	}
	return items, nil
}

func DockerVolumes(ctx context.Context, host string) ([]DockerVolume, error) {
	out, err := dockerCommand(ctx, host, "volume", "ls", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Name       string `json:"Name"`
		Driver     string `json:"Driver"`
		Scope      string `json:"Scope"`
		Mountpoint string `json:"Mountpoint"`
		Size       string `json:"Size"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerVolume, 0, len(rows))
	for _, row := range rows {
		items = append(items, DockerVolume{Name: row.Name, Driver: row.Driver, Scope: row.Scope, Mountpoint: row.Mountpoint, Size: row.Size})
	}
	return items, nil
}

func DockerSystemDF(ctx context.Context, host string) ([]DockerDiskUsage, error) {
	out, err := dockerCommand(ctx, host, "system", "df", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Type        string `json:"Type"`
		TotalCount  string `json:"TotalCount"`
		Active      string `json:"Active"`
		Size        string `json:"Size"`
		Reclaimable string `json:"Reclaimable"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerDiskUsage, 0, len(rows))
	for _, row := range rows {
		items = append(items, DockerDiskUsage{
			Type: row.Type, Total: row.TotalCount, Active: row.Active, Size: row.Size, Reclaimable: row.Reclaimable,
		})
	}
	return items, nil
}

func DockerContainerLogs(ctx context.Context, host, id string, tail int) ([]string, error) {
	if tail <= 0 {
		tail = 200
	}
	out, err := dockerCommand(ctx, host, "logs", "--tail", strconv.Itoa(tail), id).CombinedOutput()
	lines := splitLines(string(out))
	if err != nil {
		return lines, err
	}
	return lines, nil
}

func DockerContainerAction(ctx context.Context, host, id, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return exec.ErrNotFound
	}
	return dockerCommand(ctx, host, action, id).Run()
}

func dockerCommand(ctx context.Context, host string, args ...string) *exec.Cmd {
	if host != "" {
		args = append([]string{"-H", host}, args...)
	}
	return exec.CommandContext(ctx, "docker", args...)
}

func dockerEndpoint(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "local"
	}
	return host
}

func parseDockerJSONLines[T any](raw []byte, out *[]T) error {
	*out = nil
	for _, line := range splitLines(string(raw)) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return err
		}
		*out = append(*out, item)
	}
	return nil
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func toInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}
