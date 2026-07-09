package appmeta

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Metadata map[string]any

type LastCheck struct {
	Status    string         `json:"status,omitempty"`
	Message   string         `json:"message,omitempty"`
	CheckedAt time.Time      `json:"checkedAt,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

func Parse(raw string) Metadata {
	metadata, _ := ParseStrict(raw)
	return metadata
}

func ParseStrict(raw string) (Metadata, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Metadata{}, nil
	}
	metadata := Metadata{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return Metadata{}, err
	}
	if metadata == nil {
		return Metadata{}, nil
	}
	return metadata, nil
}

func Marshal(metadata map[string]any) string {
	if len(metadata) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func Clone(metadata map[string]any) Metadata {
	if len(metadata) == 0 {
		return Metadata{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		out := Metadata{}
		for key, value := range metadata {
			out[key] = value
		}
		return out
	}
	out := Metadata{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Metadata{}
	}
	return out
}

func String(metadata map[string]any, key, fallback string) string {
	if value, ok := metadata[key]; ok {
		if text := Text(value); text != "" {
			return text
		}
	}
	return fallback
}

func Text(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		if typed == float32(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func Int(metadata map[string]any, key string, fallback int) int {
	if value, ok := metadata[key]; ok {
		if parsed, ok := AnyInt(value); ok {
			return parsed
		}
	}
	return fallback
}

func AnyInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case int32:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed), true
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func Bool(metadata map[string]any, key string, fallback bool) bool {
	value, ok := metadata[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	}
	return fallback
}

func StringSlice(metadata map[string]any, key string) []string {
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	return AnyStringSlice(value)
}

func AnyStringSlice(value any) []string {
	out := []string{}
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
	case []any:
		for _, item := range typed {
			if text := Text(item); text != "" {
				out = append(out, text)
			}
		}
	case string:
		for _, item := range strings.Split(typed, ",") {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func Map(metadata map[string]any, key string) Metadata {
	raw, ok := metadata[key]
	if !ok {
		return Metadata{}
	}
	return AnyMap(raw)
}

func AnyMap(value any) Metadata {
	switch typed := value.(type) {
	case map[string]any:
		return Clone(typed)
	case Metadata:
		return Clone(typed)
	default:
		return Metadata{}
	}
}

func WithLastCheck(metadata map[string]any, check LastCheck) Metadata {
	next := Clone(metadata)
	payload := map[string]any{}
	if check.Status != "" {
		payload["status"] = check.Status
	}
	if check.Message != "" {
		payload["message"] = check.Message
	}
	if !check.CheckedAt.IsZero() {
		payload["checkedAt"] = check.CheckedAt.UTC().Format(time.RFC3339)
	}
	if len(check.Details) > 0 {
		payload["details"] = check.Details
	}
	next["lastCheck"] = payload
	return next
}

func LastCheckFrom(metadata map[string]any) (LastCheck, bool) {
	raw := Map(metadata, "lastCheck")
	if len(raw) == 0 {
		return LastCheck{}, false
	}
	check := LastCheck{
		Status:  String(raw, "status", ""),
		Message: String(raw, "message", ""),
		Details: Map(raw, "details"),
	}
	if checkedAt := String(raw, "checkedAt", ""); checkedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, checkedAt); err == nil {
			check.CheckedAt = parsed
		}
	}
	return check, true
}

func MarkInstallFailed(metadata map[string]any, taskID string, err error) Metadata {
	next := Clone(metadata)
	next["installFailed"] = true
	if strings.TrimSpace(taskID) != "" {
		next["taskId"] = strings.TrimSpace(taskID)
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		next["installError"] = strings.TrimSpace(err.Error())
	}
	return next
}

func ClusterID(metadata map[string]any) string {
	for _, key := range []string{"clusterId", "replicationGroupId", "replicaGroupId"} {
		if value := String(metadata, key, ""); value != "" {
			return value
		}
	}
	return ""
}

func Endpoint(metadata map[string]any) string {
	for _, key := range []string{"endpoint", "gatewayEndpoint", "dockerHost"} {
		if value := String(metadata, key, ""); value != "" {
			return value
		}
	}
	return ""
}
