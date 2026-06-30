package minio

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"aifar-deployment/backend/internal/store"
)

func instanceAPIPort(instance store.AppInstance) int {
	var metadata struct {
		APIPort int `json:"apiPort"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return normalizePort(metadata.APIPort, 9000)
}

func minioUninstallOptions(req DeleteRequest) UninstallOptions {
	return UninstallOptions{
		RemoveMountedDisks: boolParam(req.Parameters, "removeMountedDisks", "unmountDisks", "removeDiskMounts"),
		MountRoots:         minioMountedDiskRoots(req.Instance),
	}
}

func minioMountedDiskRoots(instance store.AppInstance) []string {
	metadata := appMetadata(instance)
	if metadataString(metadata, "storageMode") != StorageModeUnmountedDisk {
		return nil
	}
	if dataDirs := stringSliceMetadata(metadata, "dataDirs"); len(dataDirs) > 0 {
		roots := make([]string, 0, len(dataDirs))
		for _, dir := range dataDirs {
			dir = "/" + strings.Trim(strings.TrimSpace(dir), "/")
			if path.Base(dir) == "minio" {
				roots = append(roots, path.Dir(dir))
			}
		}
		return roots
	}
	dataRoot := metadataString(metadata, "dataRoot")
	devices := stringSliceMetadata(metadata, "diskDevices")
	if dataRoot == "" || len(devices) == 0 {
		return nil
	}
	roots := make([]string, 0, len(devices))
	for idx := range devices {
		roots = append(roots, path.Join("/"+strings.Trim(dataRoot, "/"), fmt.Sprintf("disk%d", idx+1)))
	}
	return roots
}

func stringSliceMetadata(metadata map[string]any, key string) []string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return cleanStrings(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return cleanStrings(out)
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" || text == "<nil>" {
			return nil
		}
		return cleanStrings(strings.Split(text, ","))
	}
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "<nil>" {
			out = append(out, value)
		}
	}
	return out
}

func boolParam(params map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := params[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			text := strings.TrimSpace(typed)
			return strings.EqualFold(text, "true") || text == "1" || strings.EqualFold(text, "yes")
		default:
			return fmt.Sprint(value) == "1"
		}
	}
	return false
}

func appMetadata(instance store.AppInstance) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}
