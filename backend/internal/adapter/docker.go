package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/store"
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
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	Ports     string            `json:"ports"`
	Networks  string            `json:"networks"`
	CreatedAt string            `json:"createdAt"`
	Labels    map[string]string `json:"labels,omitempty"`
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

type DockerContainerStat struct {
	ID            string  `json:"id,omitempty"`
	Name          string  `json:"name,omitempty"`
	CPUPerc       float64 `json:"cpuPercent"`
	MemPerc       float64 `json:"memoryPercent"`
	MemUsage      string  `json:"memoryUsage,omitempty"`
	RawCPUPerc    string  `json:"rawCpuPercent,omitempty"`
	RawMemPercent string  `json:"rawMemoryPercent,omitempty"`
}

type DockerDiskUsage struct {
	Type        string `json:"type"`
	Total       string `json:"total"`
	Active      string `json:"active"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

func DockerPing(ctx context.Context, host string) error {
	if dockerAPIHost(host) {
		return dockerAPIPing(ctx, host)
	}
	return dockerCommand(ctx, host, "version", "--format", "{{json .Server}}").Run()
}

func DockerPingForServer(ctx context.Context, server store.Server) error {
	if dockerAPIHost(server.DockerHost) {
		return DockerPing(ctx, server.DockerHost)
	}
	_, err := dockerSSHOutput(ctx, server, "version", "--format", "{{json .Server}}")
	return err
}

func DockerSummaryForHost(ctx context.Context, host string) (DockerSummary, error) {
	if dockerAPIHost(host) {
		return dockerAPISummary(ctx, host)
	}
	out, err := dockerCommand(ctx, host, "info", "--format", "{{json .}}").Output()
	if err != nil {
		return DockerSummary{}, err
	}
	return dockerSummaryFromOutput(ctx, out, dockerEndpoint(host), func(ctx context.Context) ([]DockerImage, error) {
		return DockerImages(ctx, host)
	}, func(ctx context.Context) ([]DockerNetwork, error) {
		return DockerNetworks(ctx, host)
	}, func(ctx context.Context) ([]DockerVolume, error) {
		return DockerVolumes(ctx, host)
	})
}

func DockerSummaryForServer(ctx context.Context, server store.Server) (DockerSummary, error) {
	if dockerAPIHost(server.DockerHost) {
		return DockerSummaryForHost(ctx, server.DockerHost)
	}
	out, err := dockerSSHOutput(ctx, server, "info", "--format", "{{json .}}")
	if err != nil {
		return DockerSummary{}, err
	}
	return dockerSummaryFromOutput(ctx, out, serverDockerEndpoint(server), func(ctx context.Context) ([]DockerImage, error) {
		return DockerImagesForServer(ctx, server)
	}, func(ctx context.Context) ([]DockerNetwork, error) {
		return DockerNetworksForServer(ctx, server)
	}, func(ctx context.Context) ([]DockerVolume, error) {
		return DockerVolumesForServer(ctx, server)
	})
}

func dockerSummaryFromOutput(ctx context.Context, out []byte, endpoint string, imagesFn func(context.Context) ([]DockerImage, error), networksFn func(context.Context) ([]DockerNetwork, error), volumesFn func(context.Context) ([]DockerVolume, error)) (DockerSummary, error) {
	var info struct {
		Containers        any    `json:"Containers"`
		Images            any    `json:"Images"`
		ContainersRunning any    `json:"ContainersRunning"`
		Driver            string `json:"Driver"`
		ServerVersion     string `json:"ServerVersion"`
		DockerRootDir     string `json:"DockerRootDir"`
	}
	_ = json.Unmarshal(out, &info)
	imageCount := toInt(info.Images)
	if images, err := imagesFn(ctx); err == nil {
		imageCount = len(images)
	}
	networks, _ := networksFn(ctx)
	volumes, _ := volumesFn(ctx)
	return DockerSummary{
		Containers: toInt(info.Containers),
		Images:     imageCount,
		Running:    toInt(info.ContainersRunning),
		Networks:   len(networks),
		Volumes:    len(volumes),
		Driver:     info.Driver,
		Version:    info.ServerVersion,
		RootDir:    info.DockerRootDir,
		Endpoint:   endpoint,
	}, nil
}

func DockerContainers(ctx context.Context, host string) ([]DockerContainer, error) {
	if dockerAPIHost(host) {
		return dockerAPIContainers(ctx, host)
	}
	out, err := dockerCommand(ctx, host, "ps", "-a", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	return parseDockerContainers(out)
}

func DockerContainersForServer(ctx context.Context, server store.Server) ([]DockerContainer, error) {
	if dockerAPIHost(server.DockerHost) {
		return DockerContainers(ctx, server.DockerHost)
	}
	out, err := dockerSSHOutput(ctx, server, "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseDockerContainers(out)
}

func parseDockerContainers(out []byte) ([]DockerContainer, error) {
	var rows []struct {
		ID        string `json:"ID"`
		Names     string `json:"Names"`
		Image     string `json:"Image"`
		State     string `json:"State"`
		Status    string `json:"Status"`
		Ports     string `json:"Ports"`
		Networks  string `json:"Networks"`
		CreatedAt string `json:"CreatedAt"`
		Labels    string `json:"Labels"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerContainer, 0, len(rows))
	for _, row := range rows {
		items = append(items, DockerContainer{
			ID: row.ID, Name: row.Names, Image: row.Image, State: row.State,
			Status: row.Status, Ports: row.Ports, Networks: row.Networks, CreatedAt: row.CreatedAt,
			Labels: parseDockerLabelList(row.Labels),
		})
	}
	return items, nil
}

func parseDockerLabelList(value string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, val, ok := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if ok {
			out[key] = strings.TrimSpace(val)
		} else {
			out[key] = ""
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func DockerImages(ctx context.Context, host string) ([]DockerImage, error) {
	if dockerAPIHost(host) {
		return dockerAPIImages(ctx, host)
	}
	out, err := dockerCommand(ctx, host, "images", "--digests", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	return parseDockerImages(out)
}

func DockerImagesForServer(ctx context.Context, server store.Server) ([]DockerImage, error) {
	if dockerAPIHost(server.DockerHost) {
		return DockerImages(ctx, server.DockerHost)
	}
	out, err := dockerSSHOutput(ctx, server, "images", "--digests", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseDockerImages(out)
}

func DockerImageRemove(ctx context.Context, host, id string) error {
	if dockerAPIHost(host) {
		return dockerAPIImageRemove(ctx, host, id)
	}
	return dockerCommand(ctx, host, "image", "rm", id).Run()
}

func DockerImageRemoveForServer(ctx context.Context, server store.Server, id string) error {
	if dockerAPIHost(server.DockerHost) {
		return DockerImageRemove(ctx, server.DockerHost, id)
	}
	_, err := dockerSSHOutput(ctx, server, "image", "rm", id)
	return err
}

func parseDockerImages(out []byte) ([]DockerImage, error) {
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
	if dockerAPIHost(host) {
		return dockerAPINetworks(ctx, host)
	}
	out, err := dockerCommand(ctx, host, "network", "ls", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	return parseDockerNetworks(out)
}

func DockerNetworksForServer(ctx context.Context, server store.Server) ([]DockerNetwork, error) {
	if dockerAPIHost(server.DockerHost) {
		return DockerNetworks(ctx, server.DockerHost)
	}
	out, err := dockerSSHOutput(ctx, server, "network", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseDockerNetworks(out)
}

func parseDockerNetworks(out []byte) ([]DockerNetwork, error) {
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
	if dockerAPIHost(host) {
		return dockerAPIVolumes(ctx, host)
	}
	out, err := dockerCommand(ctx, host, "volume", "ls", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	return parseDockerVolumes(out)
}

func DockerVolumesForServer(ctx context.Context, server store.Server) ([]DockerVolume, error) {
	if dockerAPIHost(server.DockerHost) {
		return DockerVolumes(ctx, server.DockerHost)
	}
	out, err := dockerSSHOutput(ctx, server, "volume", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseDockerVolumes(out)
}

func parseDockerVolumes(out []byte) ([]DockerVolume, error) {
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
	if dockerAPIHost(host) {
		return dockerAPISystemDF(ctx, host)
	}
	out, err := dockerCommand(ctx, host, "system", "df", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
	}
	return parseDockerSystemDF(out)
}

func DockerSystemDFForServer(ctx context.Context, server store.Server) ([]DockerDiskUsage, error) {
	if dockerAPIHost(server.DockerHost) {
		return DockerSystemDF(ctx, server.DockerHost)
	}
	out, err := dockerSSHOutput(ctx, server, "system", "df", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseDockerSystemDF(out)
}

func parseDockerSystemDF(out []byte) ([]DockerDiskUsage, error) {
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
	if dockerAPIHost(host) {
		return dockerAPIContainerLogs(ctx, host, id, tail)
	}
	out, err := dockerCommand(ctx, host, "logs", "--tail", strconv.Itoa(tail), id).CombinedOutput()
	lines := splitLines(string(out))
	if err != nil {
		return lines, err
	}
	return lines, nil
}

func DockerContainerLogsForServer(ctx context.Context, server store.Server, id string, tail int) ([]string, error) {
	if tail <= 0 {
		tail = 200
	}
	if dockerAPIHost(server.DockerHost) {
		return DockerContainerLogs(ctx, server.DockerHost, id, tail)
	}
	out, err := dockerSSHCombinedOutput(ctx, server, "logs", "--tail", strconv.Itoa(tail), id)
	lines := splitLines(string(out))
	if err != nil {
		return lines, err
	}
	return lines, nil
}

func DockerContainerStats(ctx context.Context, host string, ids []string) ([]DockerContainerStat, error) {
	ids = normalizeDockerArgs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, ids...)
	out, err := dockerCommand(ctx, host, args...).Output()
	if err != nil {
		return nil, err
	}
	return parseDockerContainerStats(out)
}

func DockerContainerStatsForServer(ctx context.Context, server store.Server, ids []string) ([]DockerContainerStat, error) {
	ids = normalizeDockerArgs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	if dockerAPIHost(server.DockerHost) {
		return DockerContainerStats(ctx, server.DockerHost, ids)
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, ids...)
	out, err := dockerSSHOutput(ctx, server, args...)
	if err != nil {
		return nil, err
	}
	return parseDockerContainerStats(out)
}

func parseDockerContainerStats(out []byte) ([]DockerContainerStat, error) {
	var rows []struct {
		Container string `json:"Container"`
		ID        string `json:"ID"`
		Name      string `json:"Name"`
		CPUPerc   string `json:"CPUPerc"`
		MemPerc   string `json:"MemPerc"`
		MemUsage  string `json:"MemUsage"`
	}
	if err := parseDockerJSONLines(out, &rows); err != nil {
		return nil, err
	}
	items := make([]DockerContainerStat, 0, len(rows))
	for _, row := range rows {
		id := firstNonEmptyString(row.ID, row.Container)
		items = append(items, DockerContainerStat{
			ID:            id,
			Name:          row.Name,
			CPUPerc:       parsePercent(row.CPUPerc),
			MemPerc:       parsePercent(row.MemPerc),
			MemUsage:      row.MemUsage,
			RawCPUPerc:    row.CPUPerc,
			RawMemPercent: row.MemPerc,
		})
	}
	return items, nil
}

func DockerContainerAction(ctx context.Context, host, id, action string) error {
	command, ok := dockerContainerCommand(action)
	if !ok {
		return exec.ErrNotFound
	}
	if dockerAPIHost(host) {
		return dockerAPIContainerAction(ctx, host, id, action)
	}
	return dockerCommand(ctx, host, command, id).Run()
}

func DockerContainerActionForServer(ctx context.Context, server store.Server, id, action string) error {
	command, ok := dockerContainerCommand(action)
	if !ok {
		return exec.ErrNotFound
	}
	if dockerAPIHost(server.DockerHost) {
		return DockerContainerAction(ctx, server.DockerHost, id, action)
	}
	_, err := dockerSSHOutput(ctx, server, command, id)
	return err
}

func dockerContainerCommand(action string) (string, bool) {
	switch action {
	case "start", "stop", "restart":
		return action, true
	case "remove", "rm":
		return "rm", true
	default:
		return "", false
	}
}

func dockerCommand(ctx context.Context, host string, args ...string) *exec.Cmd {
	if host != "" {
		args = append([]string{"-H", host}, args...)
	}
	return exec.CommandContext(ctx, "docker", args...)
}

func dockerSSHOutput(ctx context.Context, server store.Server, args ...string) ([]byte, error) {
	result, err := RunSSH(ctx, server, dockerShellCommand(args...))
	if err != nil {
		stderr := strings.TrimSpace(result.Stderr)
		if stderr != "" {
			return []byte(result.Stdout), fmt.Errorf("%w: %s", err, stderr)
		}
		return []byte(result.Stdout), err
	}
	return []byte(result.Stdout), nil
}

func dockerSSHCombinedOutput(ctx context.Context, server store.Server, args ...string) ([]byte, error) {
	result, err := RunSSH(ctx, server, dockerShellCommand(args...))
	out := result.Stdout
	if strings.TrimSpace(result.Stderr) != "" {
		if strings.TrimSpace(out) != "" {
			out += "\n"
		}
		out += result.Stderr
	}
	return []byte(out), err
}

func dockerShellCommand(args ...string) string {
	parts := []string{"docker"}
	for _, arg := range args {
		parts = append(parts, dockerShellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func dockerShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func normalizeDockerArgs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func parsePercent(value string) float64 {
	value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	if value == "" {
		return 0
	}
	n, _ := strconv.ParseFloat(value, 64)
	return n
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func dockerEndpoint(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "local"
	}
	return host
}

func serverDockerEndpoint(server store.Server) string {
	if strings.TrimSpace(server.DockerHost) != "" {
		return strings.TrimSpace(server.DockerHost)
	}
	return "ssh://" + server.Username + "@" + server.Host
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
