package aifar

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
)

const (
	runtimeDiagnosticServiceRecord = "AIFAR_DIAG_SERVICE"
	runtimeDiagnosticTotalRecord   = "AIFAR_DIAG_TOTAL"
	runtimeDiagnosticWarningRecord = "AIFAR_DIAG_WARNING"
)

var runtimeDiagnosticNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func parseRuntimeDiagnosticEstimate(raw string, expectedSelections ...[]string) (registry.RuntimeDiagnosticEstimateResult, error) {
	var result registry.RuntimeDiagnosticEstimateResult
	seenServices := map[string]bool{}
	expected := map[string]bool{}
	if len(expectedSelections) > 0 {
		for _, service := range expectedSelections[0] {
			expected[service] = true
		}
	}
	seenTotal := false
	trimmed := strings.TrimSuffix(raw, "\n")
	if trimmed == "" {
		return result, fmt.Errorf("runtime diagnostic estimate output is empty")
	}
	for lineNumber, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSuffix(line, "\r")
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case runtimeDiagnosticServiceRecord:
			if len(fields) != 4 {
				return result, fmt.Errorf("invalid service record at line %d", lineNumber+1)
			}
			service := fields[1]
			if !runtimeDiagnosticNamePattern.MatchString(service) {
				return result, fmt.Errorf("invalid service name at line %d", lineNumber+1)
			}
			if len(expected) > 0 && !expected[service] {
				return result, fmt.Errorf("unexpected service %q", service)
			}
			if seenServices[service] {
				return result, fmt.Errorf("duplicate service %q", service)
			}
			fileBytes, err := parseRuntimeDiagnosticBytes(fields[2])
			if err != nil {
				return result, fmt.Errorf("invalid service file bytes at line %d: %w", lineNumber+1, err)
			}
			containerBytes, err := parseRuntimeDiagnosticBytes(fields[3])
			if err != nil {
				return result, fmt.Errorf("invalid service container bytes at line %d: %w", lineNumber+1, err)
			}
			seenServices[service] = true
			result.Services = append(result.Services, registry.RuntimeDiagnosticServiceEstimate{
				Service: service, FileBytes: fileBytes, ContainerBytes: containerBytes,
			})
		case runtimeDiagnosticTotalRecord:
			if len(fields) != 6 {
				return result, fmt.Errorf("invalid total record at line %d", lineNumber+1)
			}
			if seenTotal {
				return result, fmt.Errorf("duplicate total record")
			}
			values := make([]int64, 5)
			for idx := range values {
				value, err := parseRuntimeDiagnosticBytes(fields[idx+1])
				if err != nil {
					return result, fmt.Errorf("invalid total value at line %d: %w", lineNumber+1, err)
				}
				values[idx] = value
			}
			result.FileBytes = values[0]
			result.ContainerBytes = values[1]
			result.TotalBytes = values[2]
			result.AvailableBytes = values[3]
			result.RequiredBytes = values[4]
			seenTotal = true
		case runtimeDiagnosticWarningRecord:
			if len(fields) != 3 || !runtimeDiagnosticNamePattern.MatchString(fields[1]) {
				return result, fmt.Errorf("invalid warning record at line %d", lineNumber+1)
			}
			if fields[2] != "-" {
				if !runtimeDiagnosticNamePattern.MatchString(fields[2]) || (len(expected) > 0 && !expected[fields[2]]) {
					return result, fmt.Errorf("invalid warning service at line %d", lineNumber+1)
				}
			}
			result.Warnings = append(result.Warnings, fields[1]+":"+fields[2])
		default:
			return result, fmt.Errorf("unknown estimate record at line %d", lineNumber+1)
		}
	}
	if !seenTotal {
		return result, fmt.Errorf("runtime diagnostic estimate total is missing")
	}
	var fileSum, containerSum int64
	for _, service := range result.Services {
		fileSum += service.FileBytes
		containerSum += service.ContainerBytes
	}
	if fileSum != result.FileBytes || containerSum != result.ContainerBytes || result.TotalBytes != result.FileBytes+result.ContainerBytes {
		return result, fmt.Errorf("runtime diagnostic estimate totals are inconsistent")
	}
	requiredBytes, err := runtimeDiagnosticRequiredBytes(result.TotalBytes)
	if err != nil {
		return result, err
	}
	if result.RequiredBytes != requiredBytes {
		return result, fmt.Errorf("runtime diagnostic required bytes are inconsistent")
	}
	result.RequiredBytes = requiredBytes
	for service := range expected {
		if !seenServices[service] {
			return result, fmt.Errorf("runtime diagnostic estimate is missing service %q", service)
		}
	}
	return result, nil
}

func runtimeDiagnosticRequiredBytes(totalBytes int64) (int64, error) {
	if totalBytes < 0 {
		return 0, fmt.Errorf("runtime diagnostic total bytes must be non-negative")
	}
	bufferBytes := totalBytes / 5
	if bufferBytes < 512*1024*1024 {
		bufferBytes = 512 * 1024 * 1024
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	const baseBytes = int64(1024 * 1024 * 1024)
	if totalBytes > maxInt64-baseBytes {
		return 0, fmt.Errorf("runtime diagnostic required bytes overflow")
	}
	requiredBytes := totalBytes + baseBytes
	if requiredBytes > maxInt64-bufferBytes {
		return 0, fmt.Errorf("runtime diagnostic required bytes overflow")
	}
	return requiredBytes + bufferBytes, nil
}

func parseRuntimeDiagnosticBytes(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid non-negative integer %q", value)
	}
	return parsed, nil
}
