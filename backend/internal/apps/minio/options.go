package minio

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const defaultMinioDataRoot = "/aifar/apps/minio/data"

func minioOptions(params map[string]any, defaultPassword string) InstallOptions {
	return InstallOptions{
		APIPort:      intParam(params, "apiPort", 9000),
		ConsolePort:  intParam(params, "consolePort", 9001),
		RootUser:     stringParam(params, "rootUser", "admin"),
		RootPassword: passwordParam(params, defaultPassword),
	}
}

type ReplicationOptions struct {
	Buckets          []string
	Priority         string
	MaxWorkers       int
	MaxLargeWorkers  int
	ReplicateDeletes bool
}

func minioReplicationOptions(params map[string]any) ReplicationOptions {
	return ReplicationOptions{
		Buckets:          minioReplicationBuckets(params),
		Priority:         minioReplicationPriority(params),
		MaxWorkers:       intParam(params, "replicationMaxWorkers", 8),
		MaxLargeWorkers:  intParam(params, "replicationMaxLargeWorkers", 1),
		ReplicateDeletes: boolParamDefault(params, "replicateDeletes", false),
	}
}

func minioReplicationBuckets(params map[string]any) []string {
	for _, key := range []string{"replicationBuckets", "bucketNames", "bucketName"} {
		if value, ok := params[key]; ok {
			if buckets := cleanBucketNames(value); len(buckets) > 0 {
				return buckets
			}
		}
	}
	return []string{"aifar"}
}

func minioReplicationPriority(params map[string]any) string {
	for _, key := range []string{"replicationPriority", "replicationPerformance"} {
		if value, ok := params[key]; ok {
			switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
			case "slow", "auto", "fast":
				return strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			}
		}
	}
	return "slow"
}

func cleanBucketNames(value any) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(text string) {
		for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' ' }) {
			name := strings.TrimSpace(part)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			add(item)
		}
	case []any:
		for _, item := range typed {
			add(fmt.Sprint(item))
		}
	default:
		add(fmt.Sprint(value))
	}
	return out
}

func validateMinioReplicationOptions(options ReplicationOptions) error {
	if len(options.Buckets) == 0 {
		return errors.New("MinIO bucket replication requires at least one bucket")
	}
	for _, bucket := range options.Buckets {
		if err := validateMinioBucketName(bucket); err != nil {
			return err
		}
	}
	switch options.Priority {
	case "slow", "auto", "fast":
	default:
		return fmt.Errorf("unsupported MinIO replication priority: %s", options.Priority)
	}
	if options.MaxWorkers < 1 || options.MaxWorkers > 512 {
		return errors.New("MinIO replication max workers must be between 1 and 512")
	}
	if options.MaxLargeWorkers < 1 || options.MaxLargeWorkers > 64 {
		return errors.New("MinIO replication large object workers must be between 1 and 64")
	}
	return nil
}

var minioBucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func validateMinioBucketName(bucket string) error {
	bucket = strings.TrimSpace(bucket)
	if !minioBucketNameRe.MatchString(bucket) || strings.Contains(bucket, "..") || strings.Contains(bucket, ".-") || strings.Contains(bucket, "-.") {
		return fmt.Errorf("invalid MinIO bucket name: %s", bucket)
	}
	return nil
}

func minioDataRoot(params map[string]any) string {
	for _, key := range []string{"dataRoot", "dataDiskRoot", "dataDir"} {
		if value, ok := params[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return defaultMinioDataRoot
}

func minioStorageMode(params map[string]any) string {
	for _, key := range []string{"storageMode", "dataStorageMode", "diskMode"} {
		if value, ok := params[key]; ok {
			switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
			case "unmounted", "unmounted-disk", "raw-disk", "disk", "device":
				return StorageModeUnmountedDisk
			case "local", "local-disk", "local-dir", "directory", "":
				return StorageModeLocalDisk
			}
		}
	}
	return StorageModeLocalDisk
}

func minioDiskDevice(params map[string]any) string {
	return minioDiskDeviceForServer(params, "")
}

func minioDiskDeviceForServer(params map[string]any, serverID string) string {
	return firstString(minioDiskDevicesForServer(params, serverID))
}

func minioDiskDevicesForServer(params map[string]any, serverID string) []string {
	for _, key := range []string{"diskDevice", "dataDevice", "blockDevice"} {
		if value, ok := params[key]; ok {
			return diskDevicesFromValue(value, serverID)
		}
	}
	return nil
}

func diskDevicesFromValue(value any, serverID string) []string {
	if value == nil {
		return nil
	}
	serverID = strings.TrimSpace(serverID)
	switch typed := value.(type) {
	case map[string]any:
		return diskDevicesFromAnyMap(typed, serverID)
	case map[string]string:
		if serverID != "" {
			return cleanDiskDevices([]any{typed[serverID]})
		}
		if len(typed) == 1 {
			for _, device := range typed {
				return cleanDiskDevices([]any{device})
			}
		}
	case map[any]any:
		converted := make(map[string]any, len(typed))
		for key, device := range typed {
			converted[strings.TrimSpace(fmt.Sprint(key))] = device
		}
		return diskDevicesFromAnyMap(converted, serverID)
	case []any:
		return cleanDiskDevices(typed)
	case []string:
		values := make([]any, 0, len(typed))
		for _, device := range typed {
			values = append(values, device)
		}
		return cleanDiskDevices(values)
	default:
		return cleanDiskDevices([]any{typed})
	}
	return nil
}

func diskDevicesFromAnyMap(values map[string]any, serverID string) []string {
	if serverID != "" {
		device, ok := values[serverID]
		if !ok || device == nil {
			return nil
		}
		return diskDevicesFromValue(device, "")
	}
	if len(values) == 1 {
		for _, device := range values {
			if device == nil {
				return nil
			}
			return diskDevicesFromValue(device, "")
		}
	}
	return nil
}

func cleanDiskDevices(values []any) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		device := strings.TrimSpace(fmt.Sprint(value))
		if device == "" || seen[device] {
			continue
		}
		seen[device] = true
		out = append(out, device)
	}
	return out
}

func minioDataDirRequest(params map[string]any, installRoot string, apiPort int, serverID string) DataDirRequest {
	return DataDirRequest{
		Mode:        minioStorageMode(params),
		DataRoot:    minioDataRoot(params),
		DiskDevice:  minioDiskDeviceForServer(params, serverID),
		DiskDevices: minioDiskDevicesForServer(params, serverID),
		InstallRoot: installRoot,
		APIPort:     apiPort,
	}
}

func validateMinioStorage(params map[string]any, targets ...string) error {
	mode := minioStorageMode(params)
	if mode != StorageModeLocalDisk && mode != StorageModeUnmountedDisk {
		return fmt.Errorf("unsupported MinIO storage mode: %s", mode)
	}
	if err := validateMinioDataRoot(minioDataRoot(params)); err != nil {
		return err
	}
	if mode == StorageModeUnmountedDisk {
		if len(targets) == 0 {
			targets = []string{""}
		}
		for _, target := range targets {
			devices := minioDiskDevicesForServer(params, target)
			if len(devices) == 0 {
				return errors.New("MinIO unmounted disk mode requires a disk device")
			}
			for _, device := range devices {
				if err := validateMinioDiskDevice(device); err != nil {
					return err
				}
			}
			if err := validateUniqueMinioDiskDevices(devices); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMinioDiskDevice(device string) error {
	device = strings.TrimSpace(device)
	if device == "" {
		return errors.New("MinIO unmounted disk mode requires a disk device")
	}
	if !strings.HasPrefix(device, "/dev/") {
		return errors.New("MinIO disk device must start with /dev/")
	}
	if strings.IndexFunc(device, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO disk device must not contain whitespace")
	}
	return nil
}

func validateUniqueMinioDiskDevices(devices []string) error {
	seen := map[string]bool{}
	for _, device := range devices {
		device = strings.TrimSpace(device)
		if seen[device] {
			return fmt.Errorf("MinIO disk device is selected more than once: %s", device)
		}
		seen[device] = true
	}
	return nil
}

func validateMinioDataRoot(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "/") {
		return errors.New("MinIO data disk root must be an absolute path")
	}
	if strings.Trim(value, "/") == "" {
		return errors.New("MinIO data disk root must not be /")
	}
	if strings.IndexFunc(value, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO data disk root must not contain whitespace")
	}
	return nil
}

func passwordParam(params map[string]any, fallback string) string {
	for _, key := range []string{"rootPassword", "password", "minioPassword"} {
		if value, ok := params[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return "Oversea.123"
	}
	return fallback
}

func stringParam(params map[string]any, key, fallback string) string {
	if value, ok := params[key]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return fallback
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return normalizePort(v, fallback)
	case int64:
		return normalizePort(int(v), fallback)
	case float64:
		return normalizePort(int(v), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePort(n, fallback)
	default:
		return fallback
	}
}

func boolParamDefault(params map[string]any, key string, fallback bool) bool {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		text := strings.ToLower(strings.TrimSpace(v))
		return text == "true" || text == "1" || text == "yes" || text == "on"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return fallback
	}
}

func normalizePort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
