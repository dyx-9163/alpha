package aifar

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	artifactBundleSchema       = "aifar-artifact-bundle-v1"
	artifactBundleManifestName = "manifest.json"
)

type artifactBundleManifest struct {
	Schema   string                          `json:"schema"`
	App      string                          `json:"app"`
	Kind     string                          `json:"kind"`
	Services []artifactBundleManifestService `json:"services"`
}

type artifactBundleManifestService struct {
	Service  string `json:"service"`
	Module   string `json:"module"`
	Artifact string `json:"artifact"`
	FileName string `json:"fileName"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type artifactBundleItem struct {
	ServiceName string
	FileName    string
	LocalPath   string
	SHA256      string
	Size        int64
}

func (s Service) ValidateArtifactBundleUpdate(req ArtifactBundleUpdateRequest) error {
	copy := updateCopyFor(req.Language)
	_, cleanup, err := s.artifactBundleItemsFromRequest(req, copy, false)
	if cleanup != nil {
		cleanup()
	}
	return err
}

func (s Service) UpdateArtifactBundle(ctx context.Context, req ArtifactBundleUpdateRequest, log Logger, targetLog targetLogger) error {
	copy := updateCopyFor(req.Language)
	items, cleanup, err := s.artifactBundleItemsFromRequest(req, copy, true)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New(copy.BundleEmpty)
	}

	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	recorder, _ := log.(stepRecorder)
	stepsPerService := len(updateSteps(copy))
	current := req.Instance
	log.Info(copy.BundleUpdating, len(items))
	for idx, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if idx > 0 {
			current, err = s.store.GetAppInstance(req.Instance.ID)
			if err != nil {
				return err
			}
		}
		serviceLog := log
		if recorder != nil {
			serviceLog = prefixedStepLogger{
				Logger:      log,
				recorder:    recorder,
				prefix:      item.ServiceName,
				titlePrefix: item.ServiceName,
				orderOffset: idx * stepsPerService,
			}
		}
		log.Info(copy.BundleServiceUpdating, idx+1, len(items), item.ServiceName)
		if err := s.UpdateArtifact(ctx, ArtifactUpdateRequest{
			Instance:          current,
			Server:            req.Server,
			Language:          req.Language,
			Actor:             req.Actor,
			ServiceName:       item.ServiceName,
			ArtifactLocalPath: item.LocalPath,
			ArtifactFileName:  item.FileName,
		}, serviceLog, targetLog); err != nil {
			return err
		}
		current, err = s.store.GetAppInstance(req.Instance.ID)
		if err != nil {
			return err
		}
	}
	log.Info(copy.BundleUpdated, target, len(items))
	return nil
}

func (s Service) artifactBundleItemsFromRequest(req ArtifactBundleUpdateRequest, copy UpdateCopy, extract bool) ([]artifactBundleItem, func(), error) {
	if req.Instance.App != AppName {
		return nil, nil, errors.New(copy.UnsupportedInstance)
	}
	if strings.TrimSpace(req.Instance.ServerID) == "" && strings.TrimSpace(req.Server.ID) == "" {
		return nil, nil, errors.New(copy.TargetRequired)
	}
	bundlePath := strings.TrimSpace(req.BundleLocalPath)
	if bundlePath == "" {
		return nil, nil, errors.New(copy.BundleRequired)
	}
	stat, err := os.Stat(bundlePath)
	if err != nil {
		return nil, nil, err
	}
	if stat.IsDir() || stat.Size() == 0 {
		return nil, nil, errors.New(copy.BundleRequired)
	}
	if !strings.EqualFold(filepath.Ext(req.BundleFileName), ".zip") && !strings.EqualFold(filepath.Ext(bundlePath), ".zip") {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, "update bundle must be a zip file")
	}

	reader, err := zip.OpenReader(bundlePath)
	if err != nil {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, err)
	}
	defer reader.Close()

	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		clean, err := cleanBundleZipPath(file.Name)
		if err != nil {
			return nil, nil, fmt.Errorf(copy.BundleInvalid, err)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		entries[clean] = file
	}
	manifestFile := entries[artifactBundleManifestName]
	if manifestFile == nil {
		return nil, nil, errors.New(copy.BundleManifestRequired)
	}
	manifest, err := readArtifactBundleManifest(manifestFile)
	if err != nil {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, err)
	}
	if manifest.Schema != artifactBundleSchema {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, "manifest schema is not supported")
	}
	if strings.TrimSpace(manifest.App) != "" && !strings.EqualFold(manifest.App, AppName) {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, "manifest app is not aifar")
	}
	if len(manifest.Services) == 0 {
		return nil, nil, errors.New(copy.BundleEmpty)
	}

	tempDir := ""
	cleanup := func() {}
	if extract {
		tempDir, err = os.MkdirTemp("", "aifar-artifact-bundle-*")
		if err != nil {
			return nil, nil, err
		}
		cleanup = func() {
			_ = os.RemoveAll(tempDir)
		}
	}
	fail := func(err error) ([]artifactBundleItem, func(), error) {
		if extract {
			cleanup()
		}
		return nil, nil, err
	}

	seen := map[string]bool{}
	items := make([]artifactBundleItem, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		serviceName := cleanAIFARServiceName(service.Service)
		if !aifarServiceSupported(serviceName) {
			return fail(fmt.Errorf(copy.UnsupportedService, service.Service))
		}
		if seen[serviceName] {
			return fail(fmt.Errorf(copy.BundleInvalid, fmt.Sprintf(copy.BundleDuplicateService, serviceName)))
		}
		seen[serviceName] = true
		artifactPath, err := cleanBundleZipPath(service.Artifact)
		if err != nil {
			return fail(fmt.Errorf(copy.BundleInvalid, err))
		}
		if artifactPath == artifactBundleManifestName {
			return fail(fmt.Errorf(copy.BundleInvalid, "manifest cannot be used as an artifact"))
		}
		zipFile := entries[artifactPath]
		if zipFile == nil {
			return fail(fmt.Errorf(copy.BundleArtifactMissing, artifactPath))
		}
		fileName := safeBundleArtifactFileName(service.FileName, artifactPath)
		if fileName == "" {
			return fail(errors.New(copy.ArtifactRequired))
		}
		if !artifactTypeAllowed(serviceName, fileName) {
			return fail(fmt.Errorf(copy.ArtifactTypeInvalid, serviceName))
		}
		item := artifactBundleItem{ServiceName: serviceName, FileName: fileName}
		if extract {
			localPath := filepath.Join(tempDir, serviceName, fileName)
			sum, size, err := extractBundleArtifact(zipFile, localPath)
			if err != nil {
				return fail(err)
			}
			item.LocalPath = localPath
			item.SHA256 = sum
			item.Size = size
		} else {
			sum, size, err := hashBundleArtifact(zipFile)
			if err != nil {
				return fail(err)
			}
			item.SHA256 = sum
			item.Size = size
		}
		if service.Size > 0 && service.Size != item.Size {
			return fail(fmt.Errorf(copy.BundleInvalid, fmt.Sprintf("artifact %s size mismatch: expected %d, got %d", artifactPath, service.Size, item.Size)))
		}
		if expected := strings.TrimSpace(service.SHA256); expected != "" && !strings.EqualFold(expected, item.SHA256) {
			return fail(fmt.Errorf(copy.BundleInvalid, fmt.Sprintf("artifact %s sha256 mismatch: expected %s, got %s", artifactPath, expected, item.SHA256)))
		}
		items = append(items, item)
	}
	return items, cleanup, nil
}

func readArtifactBundleManifest(file *zip.File) (artifactBundleManifest, error) {
	var manifest artifactBundleManifest
	reader, err := file.Open()
	if err != nil {
		return manifest, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return manifest, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func cleanBundleZipPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.Contains(value, "\x00") || path.IsAbs(value) || strings.Contains(value, ":") {
		return "", fmt.Errorf("unsafe bundle path: %s", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe bundle path: %s", value)
	}
	return clean, nil
}

func safeBundleArtifactFileName(fileName, artifactPath string) string {
	fileName = strings.TrimSpace(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "" {
		fileName = path.Base(artifactPath)
	}
	fileName = path.Base(fileName)
	if fileName == "." || fileName == "/" {
		return ""
	}
	return fileName
}

func hashBundleArtifact(file *zip.File) (string, int64, error) {
	reader, err := file.Open()
	if err != nil {
		return "", 0, err
	}
	defer reader.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func extractBundleArtifact(file *zip.File, localPath string) (string, int64, error) {
	reader, err := file.Open()
	if err != nil {
		return "", 0, err
	}
	defer reader.Close()
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(localPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hash), reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func prefixedUpdateStepName(serviceName, stepName string) string {
	return cleanAIFARServiceName(serviceName) + "-" + strings.TrimSpace(stepName)
}

func prefixedUpdateStepTitle(serviceName, title string) string {
	serviceName = cleanAIFARServiceName(serviceName)
	if serviceName == "" {
		return title
	}
	return serviceName + " / " + title
}

type prefixedStepLogger struct {
	Logger
	recorder    stepRecorder
	prefix      string
	titlePrefix string
	orderOffset int
}

func (l prefixedStepLogger) StartTarget(target string) {
	if l.recorder != nil {
		l.recorder.StartTarget(target)
	}
}

func (l prefixedStepLogger) FinishTarget(target, status, errText string) {
	if l.recorder != nil {
		l.recorder.FinishTarget(target, status, errText)
	}
}

func (l prefixedStepLogger) StartStep(target, name, title string, order int) {
	if l.recorder != nil {
		l.recorder.StartStep(target, prefixedUpdateStepName(l.prefix, name), prefixedUpdateStepTitle(l.titlePrefix, title), l.orderOffset+order)
	}
}

func (l prefixedStepLogger) FinishStep(target, name, status, errText string) {
	if l.recorder != nil {
		l.recorder.FinishStep(target, prefixedUpdateStepName(l.prefix, name), status, errText)
	}
}
