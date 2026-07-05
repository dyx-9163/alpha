package adapter

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var dockerHTTPClient = &http.Client{Timeout: 20 * time.Second}

func dockerAPIHost(host string) bool {
	_, ok := dockerAPIBase(host)
	return ok
}

func dockerAPIBase(host string) (string, bool) {
	host = strings.TrimSpace(host)
	if host == "" || strings.HasPrefix(host, "ssh://") || strings.HasPrefix(host, "unix://") {
		return "", false
	}
	if strings.HasPrefix(host, "tcp://") {
		return "http://" + strings.TrimPrefix(host, "tcp://"), true
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/"), true
	}
	return "", false
}

func dockerAPIURL(host, apiPath string, query url.Values) (string, error) {
	base, ok := dockerAPIBase(host)
	if !ok {
		return "", fmt.Errorf("unsupported Docker API host: %s", host)
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	rawPath := strings.TrimRight(u.EscapedPath(), "/") + "/" + strings.TrimLeft(apiPath, "/")
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", err
	}
	u.Path = decodedPath
	if rawPath != decodedPath {
		u.RawPath = rawPath
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func dockerAPIJSON(ctx context.Context, method, host, apiPath string, query url.Values, out any) error {
	target, err := dockerAPIURL(host, apiPath, query)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return err
	}
	resp, err := dockerHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("docker API %s %s failed: %s", method, apiPath, strings.TrimSpace(string(body)))
	}
	if readErr != nil {
		return readErr
	}
	if out == nil || len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func dockerAPIRaw(ctx context.Context, method, host, apiPath string, query url.Values) ([]byte, error) {
	target, err := dockerAPIURL(host, apiPath, query)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := dockerHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("docker API %s %s failed: %s", method, apiPath, strings.TrimSpace(string(body)))
	}
	return body, readErr
}

func dockerAPIPing(ctx context.Context, host string) error {
	_, err := dockerAPIRaw(ctx, http.MethodGet, host, "/_ping", nil)
	return err
}

func dockerAPISummary(ctx context.Context, host string) (DockerSummary, error) {
	var info struct {
		Containers        int    `json:"Containers"`
		Images            int    `json:"Images"`
		ContainersRunning int    `json:"ContainersRunning"`
		Driver            string `json:"Driver"`
		ServerVersion     string `json:"ServerVersion"`
		DockerRootDir     string `json:"DockerRootDir"`
	}
	if err := dockerAPIJSON(ctx, http.MethodGet, host, "/info", nil, &info); err != nil {
		return DockerSummary{}, err
	}
	imageCount := info.Images
	if images, err := dockerAPIImages(ctx, host); err == nil {
		imageCount = len(images)
	}
	networks, _ := dockerAPINetworks(ctx, host)
	volumes, _ := dockerAPIVolumes(ctx, host)
	return DockerSummary{
		Containers: info.Containers,
		Images:     imageCount,
		Running:    info.ContainersRunning,
		Networks:   len(networks),
		Volumes:    len(volumes),
		Driver:     info.Driver,
		Version:    info.ServerVersion,
		RootDir:    info.DockerRootDir,
		Endpoint:   host,
	}, nil
}

func dockerAPIContainers(ctx context.Context, host string) ([]DockerContainer, error) {
	var rows []struct {
		ID              string `json:"Id"`
		Names           []string
		Image           string
		State           string
		Status          string
		Created         int64
		Ports           []dockerAPIPort
		Labels          map[string]string
		NetworkSettings struct {
			Networks map[string]any
		}
	}
	query := url.Values{"all": []string{"1"}}
	if err := dockerAPIJSON(ctx, http.MethodGet, host, "/containers/json", query, &rows); err != nil {
		return nil, err
	}
	out := make([]DockerContainer, 0, len(rows))
	for _, row := range rows {
		name := row.ID
		if len(row.Names) > 0 {
			name = strings.TrimPrefix(row.Names[0], "/")
		}
		out = append(out, DockerContainer{
			ID:        row.ID,
			Name:      name,
			Image:     row.Image,
			State:     row.State,
			Status:    row.Status,
			Ports:     formatDockerAPIPorts(row.Ports),
			Networks:  sortedKeys(row.NetworkSettings.Networks),
			CreatedAt: formatUnix(row.Created),
			Labels:    cloneStringMap(row.Labels),
		})
	}
	return out, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type dockerAPIPort struct {
	IP          string
	PrivatePort int
	PublicPort  int
	Type        string
}

func formatDockerAPIPorts(ports []dockerAPIPort) string {
	if len(ports) == 0 {
		return ""
	}
	out := make([]string, 0, len(ports))
	for _, port := range ports {
		proto := port.Type
		if proto == "" {
			proto = "tcp"
		}
		private := fmt.Sprintf("%d/%s", port.PrivatePort, proto)
		if port.PublicPort > 0 {
			ip := port.IP
			if ip == "" {
				ip = "0.0.0.0"
			}
			out = append(out, fmt.Sprintf("%s:%d->%s", ip, port.PublicPort, private))
		} else {
			out = append(out, private)
		}
	}
	return strings.Join(out, ", ")
}

func dockerAPIImages(ctx context.Context, host string) ([]DockerImage, error) {
	var rows []struct {
		ID          string `json:"Id"`
		RepoTags    []string
		RepoDigests []string
		Size        int64
		Created     int64
	}
	query := url.Values{"digests": []string{"1"}}
	if err := dockerAPIJSON(ctx, http.MethodGet, host, "/images/json", query, &rows); err != nil {
		return nil, err
	}
	out := make([]DockerImage, 0, len(rows))
	for _, row := range rows {
		repo, tag := splitImageTag(firstNonEmpty(row.RepoTags, "<none>:<none>"))
		out = append(out, DockerImage{
			ID:         strings.TrimPrefix(row.ID, "sha256:"),
			Repository: repo,
			Tag:        tag,
			Size:       formatBytes(row.Size),
			CreatedAt:  formatUnix(row.Created),
			Digest:     firstNonEmpty(row.RepoDigests, ""),
		})
	}
	return out, nil
}

func dockerAPINetworks(ctx context.Context, host string) ([]DockerNetwork, error) {
	var rows []struct {
		ID     string `json:"Id"`
		Name   string
		Driver string
		Scope  string
	}
	if err := dockerAPIJSON(ctx, http.MethodGet, host, "/networks", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]DockerNetwork, 0, len(rows))
	for _, row := range rows {
		out = append(out, DockerNetwork{ID: row.ID, Name: row.Name, Driver: row.Driver, Scope: row.Scope})
	}
	return out, nil
}

func dockerAPIVolumes(ctx context.Context, host string) ([]DockerVolume, error) {
	var response struct {
		Volumes []struct {
			Name       string
			Driver     string
			Scope      string
			Mountpoint string
			UsageData  struct {
				Size int64
			}
		}
	}
	if err := dockerAPIJSON(ctx, http.MethodGet, host, "/volumes", nil, &response); err != nil {
		return nil, err
	}
	out := make([]DockerVolume, 0, len(response.Volumes))
	for _, row := range response.Volumes {
		out = append(out, DockerVolume{
			Name:       row.Name,
			Driver:     row.Driver,
			Scope:      row.Scope,
			Mountpoint: row.Mountpoint,
			Size:       formatBytes(row.UsageData.Size),
		})
	}
	return out, nil
}

func dockerAPISystemDF(ctx context.Context, host string) ([]DockerDiskUsage, error) {
	var response struct {
		Images []struct {
			Size int64
		}
		Containers []struct {
			SizeRootFs int64
		}
		Volumes []struct {
			UsageData struct {
				Size int64
			}
		}
		BuildCache []struct {
			Size int64
		}
	}
	if err := dockerAPIJSON(ctx, http.MethodGet, host, "/system/df", nil, &response); err != nil {
		return nil, err
	}
	return []DockerDiskUsage{
		{Type: "Images", Total: strconv.Itoa(len(response.Images)), Size: formatBytes(sumAPIImageSize(response.Images)), Reclaimable: "-"},
		{Type: "Containers", Total: strconv.Itoa(len(response.Containers)), Size: formatBytes(sumAPIContainerSize(response.Containers)), Reclaimable: "-"},
		{Type: "Local Volumes", Total: strconv.Itoa(len(response.Volumes)), Size: formatBytes(sumAPIVolumeSize(response.Volumes)), Reclaimable: "-"},
		{Type: "Build Cache", Total: strconv.Itoa(len(response.BuildCache)), Size: formatBytes(sumAPIBuildCacheSize(response.BuildCache)), Reclaimable: "-"},
	}, nil
}

func dockerAPIContainerLogs(ctx context.Context, host, id string, tail int) ([]string, error) {
	query := url.Values{
		"stdout": []string{"1"},
		"stderr": []string{"1"},
		"tail":   []string{strconv.Itoa(tail)},
	}
	body, err := dockerAPIRaw(ctx, http.MethodGet, host, "/containers/"+url.PathEscape(id)+"/logs", query)
	lines := decodeDockerLogBytes(body)
	if err != nil {
		return lines, err
	}
	return lines, nil
}

func dockerAPIContainerAction(ctx context.Context, host, id, action string) error {
	if action == "remove" || action == "rm" {
		return dockerAPIJSON(ctx, http.MethodDelete, host, "/containers/"+url.PathEscape(id), nil, nil)
	}
	return dockerAPIJSON(ctx, http.MethodPost, host, "/containers/"+url.PathEscape(id)+"/"+action, nil, nil)
}

func dockerAPIImageRemove(ctx context.Context, host, id string) error {
	return dockerAPIJSON(ctx, http.MethodDelete, host, "/images/"+url.PathEscape(id), nil, nil)
}

func decodeDockerLogBytes(raw []byte) []string {
	var payload []byte
	for len(raw) >= 8 && raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
		size := int(binary.BigEndian.Uint32(raw[4:8]))
		if size < 0 || len(raw) < 8+size {
			break
		}
		payload = append(payload, raw[8:8+size]...)
		raw = raw[8+size:]
	}
	if len(payload) > 0 {
		return splitLines(string(payload))
	}
	return splitLines(string(raw))
}

func sortedKeys(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func splitImageTag(value string) (string, string) {
	index := strings.LastIndex(value, ":")
	if index <= 0 || strings.Contains(value[index+1:], "/") {
		return value, ""
	}
	return value[:index], value[index+1:]
}

func firstNonEmpty(values []string, fallback string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return fallback
}

func formatUnix(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).Format(time.RFC3339)
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func sumAPIImageSize(values []struct{ Size int64 }) int64 {
	var total int64
	for _, value := range values {
		total += value.Size
	}
	return total
}

func sumAPIContainerSize(values []struct{ SizeRootFs int64 }) int64 {
	var total int64
	for _, value := range values {
		total += value.SizeRootFs
	}
	return total
}

func sumAPIVolumeSize(values []struct {
	UsageData struct {
		Size int64
	}
}) int64 {
	var total int64
	for _, value := range values {
		total += value.UsageData.Size
	}
	return total
}

func sumAPIBuildCacheSize(values []struct{ Size int64 }) int64 {
	var total int64
	for _, value := range values {
		total += value.Size
	}
	return total
}
