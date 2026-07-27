package aifar

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
)

const (
	runtimeDiagnosticServiceRecord   = "AIFAR_DIAG_SERVICE"
	runtimeDiagnosticTotalRecord     = "AIFAR_DIAG_TOTAL"
	runtimeDiagnosticWarningRecord   = "AIFAR_DIAG_WARNING"
	runtimeDiagnosticResultRecord    = "AIFAR_DIAG_RESULT"
	runtimeDiagnosticServiceRecordV2 = "AIFAR_DIAG_SERVICE_V2"
	runtimeDiagnosticTotalRecordV2   = "AIFAR_DIAG_TOTAL_V2"
	runtimeDiagnosticStreamRecordV1  = "AIFAR_DIAG_STREAM_V1"
)

var runtimeDiagnosticNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var runtimeDiagnosticTimezonePattern = regexp.MustCompile(`^[A-Za-z0-9_+.-]+(?:/[A-Za-z0-9_+.-]+)*$`)
var runtimeDiagnosticLogFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.log(?:\.[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

var (
	runtimeDiagnosticExportIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	runtimeDiagnosticArchivePattern  = regexp.MustCompile(`^aifar-diagnostics-[A-Za-z0-9._-]+-[0-9]{8}T[0-9]{6}Z\.tar\.gz$`)
	runtimeDiagnosticSHA256Pattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type runtimeDiagnosticExportResult struct {
	RemoteRelativePath string
	ArchiveName        string
	ArchiveBytes       int64
	UncompressedBytes  int64
	SHA256             string
	WarningCount       int
}

type runtimeDiagnosticStreamHeader struct {
	ArchiveName       string
	UncompressedBytes int64
	WarningCount      int
	ServerTimezone    string
}

func parseRuntimeDiagnosticStreamHeader(line string) (runtimeDiagnosticStreamHeader, error) {
	var result runtimeDiagnosticStreamHeader
	if !strings.HasSuffix(line, "\n") || strings.Contains(line[:len(line)-1], "\n") || strings.Contains(line, "\r") {
		return result, fmt.Errorf("runtime diagnostic stream header must be one LF-terminated record")
	}
	fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
	if len(fields) != 5 || fields[0] != runtimeDiagnosticStreamRecordV1 {
		return result, fmt.Errorf("runtime diagnostic stream header is invalid")
	}
	if !runtimeDiagnosticArchivePattern.MatchString(fields[1]) {
		return result, fmt.Errorf("runtime diagnostic stream archive name is invalid")
	}
	uncompressedBytes, err := parseRuntimeDiagnosticBytes(fields[2])
	if err != nil || uncompressedBytes > 524288000 {
		return result, fmt.Errorf("runtime diagnostic stream uncompressed bytes are invalid")
	}
	warningCount, err := parseRuntimeDiagnosticBytes(fields[3])
	if err != nil || warningCount > runtimeDiagnosticMaxTotalScan {
		return result, fmt.Errorf("runtime diagnostic stream warning count is invalid")
	}
	if !validRuntimeDiagnosticTimezone(fields[4]) {
		return result, fmt.Errorf("runtime diagnostic stream timezone is invalid")
	}
	return runtimeDiagnosticStreamHeader{
		ArchiveName: fields[1], UncompressedBytes: uncompressedBytes,
		WarningCount: int(warningCount), ServerTimezone: fields[4],
	}, nil
}

func validRuntimeDiagnosticTimezone(value string) bool {
	if !runtimeDiagnosticTimezonePattern.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func runtimeDiagnosticLogFileAllowed(name string) bool {
	if !runtimeDiagnosticLogFilePattern.MatchString(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\\`) {
		return false
	}
	lower := strings.ToLower(name)
	for _, forbidden := range []string{"config", "database", "credential", "secret", "password", "token", "private", "keystore", "truststore"} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	return true
}

func parseRuntimeDiagnosticExportResult(raw, exportID string) (runtimeDiagnosticExportResult, error) {
	var result runtimeDiagnosticExportResult
	line := strings.TrimSuffix(raw, "\n")
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return result, fmt.Errorf("runtime diagnostic export result must be one LF-terminated record")
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 7 || fields[0] != runtimeDiagnosticResultRecord {
		return result, fmt.Errorf("runtime diagnostic export result record is invalid")
	}
	if !runtimeDiagnosticExportIDPattern.MatchString(exportID) {
		return result, fmt.Errorf("runtime diagnostic export id is invalid")
	}
	relativePath, archiveName, err := validateRuntimeDiagnosticRelativePath(exportID, fields[1], fields[2])
	if err != nil {
		return result, err
	}
	archiveBytes, err := parseRuntimeDiagnosticBytes(fields[3])
	if err != nil || archiveBytes > runtimeDiagnosticMaxArchive {
		return result, fmt.Errorf("runtime diagnostic archive bytes are invalid")
	}
	uncompressedBytes, err := parseRuntimeDiagnosticBytes(fields[4])
	if err != nil || uncompressedBytes > runtimeDiagnosticMaxUncompressed {
		return result, fmt.Errorf("runtime diagnostic uncompressed bytes are invalid")
	}
	if !runtimeDiagnosticSHA256Pattern.MatchString(fields[5]) {
		return result, fmt.Errorf("runtime diagnostic archive checksum is invalid")
	}
	warningCount64, err := parseRuntimeDiagnosticBytes(fields[6])
	if err != nil || warningCount64 > 100000 {
		return result, fmt.Errorf("runtime diagnostic warning count is invalid")
	}
	return runtimeDiagnosticExportResult{
		RemoteRelativePath: relativePath,
		ArchiveName:        archiveName,
		ArchiveBytes:       archiveBytes,
		UncompressedBytes:  uncompressedBytes,
		SHA256:             fields[5],
		WarningCount:       int(warningCount64),
	}, nil
}

func validateRuntimeDiagnosticRelativePath(exportID, relativePath, archiveName string) (string, string, error) {
	if !runtimeDiagnosticExportIDPattern.MatchString(exportID) || !runtimeDiagnosticArchivePattern.MatchString(archiveName) {
		return "", "", fmt.Errorf("runtime diagnostic archive identity is invalid")
	}
	if strings.ContainsAny(relativePath, "\\\r\n\t") || path.IsAbs(relativePath) || path.Clean(relativePath) != relativePath {
		return "", "", fmt.Errorf("runtime diagnostic archive path is invalid")
	}
	if relativePath != exportID+"/"+archiveName || path.Base(relativePath) != archiveName {
		return "", "", fmt.Errorf("runtime diagnostic archive path is outside its export root")
	}
	return relativePath, archiveName, nil
}

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
	protocolVersion := 0
	trimmed := strings.TrimSuffix(raw, "\n")
	if trimmed == "" {
		return result, fmt.Errorf("runtime diagnostic estimate output is empty")
	}
	for lineNumber, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSuffix(line, "\r")
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case runtimeDiagnosticServiceRecordV2:
			if protocolVersion == 1 || len(fields) != 4 {
				return result, fmt.Errorf("invalid V2 service record at line %d", lineNumber+1)
			}
			protocolVersion = 2
			service := fields[1]
			if !runtimeDiagnosticNamePattern.MatchString(service) || seenServices[service] || (len(expected) > 0 && !expected[service]) {
				return result, fmt.Errorf("invalid V2 service name at line %d", lineNumber+1)
			}
			candidateFiles64, err := parseRuntimeDiagnosticBytes(fields[2])
			if err != nil || candidateFiles64 > int64(^uint(0)>>1) {
				return result, fmt.Errorf("invalid V2 candidate file count at line %d", lineNumber+1)
			}
			candidateBytes, err := parseRuntimeDiagnosticBytes(fields[3])
			if err != nil {
				return result, fmt.Errorf("invalid V2 candidate bytes at line %d", lineNumber+1)
			}
			seenServices[service] = true
			result.Services = append(result.Services, registry.RuntimeDiagnosticServiceEstimate{
				Service: service, CandidateFiles: int(candidateFiles64), CandidateScanBytes: candidateBytes,
			})
		case runtimeDiagnosticTotalRecordV2:
			if protocolVersion == 1 || len(fields) != 5 || seenTotal {
				return result, fmt.Errorf("invalid V2 total record at line %d", lineNumber+1)
			}
			protocolVersion = 2
			candidateFiles64, err := parseRuntimeDiagnosticBytes(fields[1])
			if err != nil || candidateFiles64 > int64(^uint(0)>>1) {
				return result, fmt.Errorf("invalid V2 total file count at line %d", lineNumber+1)
			}
			candidateBytes, err := parseRuntimeDiagnosticBytes(fields[2])
			if err != nil {
				return result, fmt.Errorf("invalid V2 total bytes at line %d", lineNumber+1)
			}
			if !validRuntimeDiagnosticTimezone(fields[3]) {
				return result, fmt.Errorf("invalid V2 timezone at line %d", lineNumber+1)
			}
			blockReason := fields[4]
			if blockReason == "-" {
				blockReason = ""
			} else if !runtimeDiagnosticNamePattern.MatchString(blockReason) {
				return result, fmt.Errorf("invalid V2 block reason at line %d", lineNumber+1)
			}
			result.CandidateFiles = int(candidateFiles64)
			result.CandidateScanBytes = candidateBytes
			result.ServerTimezone = fields[3]
			result.BlockReason = blockReason
			seenTotal = true
		case runtimeDiagnosticServiceRecord:
			if protocolVersion == 2 {
				return result, fmt.Errorf("mixed runtime diagnostic estimate protocols")
			}
			protocolVersion = 1
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
			if protocolVersion == 2 {
				return result, fmt.Errorf("mixed runtime diagnostic estimate protocols")
			}
			protocolVersion = 1
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
	if protocolVersion == 2 {
		var candidateFiles int
		var candidateBytes int64
		for _, service := range result.Services {
			candidateFiles += service.CandidateFiles
			candidateBytes += service.CandidateScanBytes
		}
		if candidateFiles != result.CandidateFiles || candidateBytes != result.CandidateScanBytes {
			return result, fmt.Errorf("runtime diagnostic V2 estimate totals are inconsistent")
		}
		for service := range expected {
			if !seenServices[service] {
				return result, fmt.Errorf("runtime diagnostic estimate is missing service %q", service)
			}
		}
		return result, nil
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
