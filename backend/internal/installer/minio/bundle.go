package minio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aifar-deployment/backend/internal/resourcekit"
	"aifar-deployment/backend/internal/store"
)

type Bundle struct {
	Version        string
	ArchivePath    string
	ArchiveSHA256  string
	MCPath         string
	GoArchivePath  string
	GoModCachePath string
	RPMPaths       []string
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	selected, ok := resourcekit.Select(resources, resourcekit.SelectOptions{
		App:            "minio",
		Part:           "backend",
		Version:        version,
		SkipSignatures: true,
		Match: func(baseLower string, _ store.Resource) bool {
			return looksLikeMinioArchive(baseLower)
		},
	})
	if !ok {
		version = resourcekit.NormalizeVersion(version)
		return Bundle{}, fmt.Errorf("minio resource %s not found", version)
	}
	root := filepath.Dir(selected.Path)
	return Bundle{
		Version:        selected.Version,
		ArchivePath:    selected.Path,
		ArchiveSHA256:  selected.SHA256,
		MCPath:         resourcekit.FirstGlob(filepath.Join(root, "mc.linux-amd64*"), true),
		GoArchivePath:  resourcekit.FirstGlob(filepath.Join(root, "go", "*", "go*.linux-amd64.tar.gz"), true),
		GoModCachePath: resourcekit.FirstGlob(filepath.Join(root, "go", "cache", "gomodcache*.tar.gz"), true),
		RPMPaths:       resourcekit.ListRPMs(filepath.Join(root, "rpms")),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if strings.TrimSpace(bundle.ArchivePath) == "" {
		return errors.New("minio archive is required")
	}
	if _, err := os.Stat(bundle.ArchivePath); err != nil {
		return err
	}
	if strings.TrimSpace(bundle.GoArchivePath) == "" {
		return errors.New("minio go toolchain archive is required")
	}
	if _, err := os.Stat(bundle.GoArchivePath); err != nil {
		return err
	}
	if strings.TrimSpace(bundle.GoModCachePath) == "" {
		return errors.New("minio go module cache archive is required")
	}
	if _, err := os.Stat(bundle.GoModCachePath); err != nil {
		return err
	}
	if strings.TrimSpace(bundle.MCPath) != "" {
		if _, err := os.Stat(bundle.MCPath); err != nil {
			return err
		}
	}
	if strings.TrimSpace(bundle.ArchiveSHA256) == "" {
		return nil
	}
	return resourcekit.VerifySHA256(bundle.ArchivePath, bundle.ArchiveSHA256, "minio archive")
}

func looksLikeMinioArchive(name string) bool {
	return strings.HasPrefix(name, "minio-") && (strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"))
}
