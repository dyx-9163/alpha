package minio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	version = strings.TrimSpace(version)
	if version == "" {
		version = "latest"
	}
	var candidates []store.Resource
	for _, res := range resources {
		name := strings.ToLower(filepath.Base(res.Path))
		if res.App != "minio" || res.Part != "backend" {
			continue
		}
		if version != "latest" && res.Version != version {
			continue
		}
		if strings.HasSuffix(name, ".sha256sum") || strings.HasSuffix(name, ".minisig") {
			continue
		}
		if looksLikeMinioArchive(name) {
			candidates = append(candidates, res)
		}
	}
	if len(candidates) == 0 {
		return Bundle{}, fmt.Errorf("minio resource %s not found", version)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Version < candidates[j].Version
	})
	selected := candidates[len(candidates)-1]
	root := filepath.Dir(selected.Path)
	return Bundle{
		Version:        selected.Version,
		ArchivePath:    selected.Path,
		ArchiveSHA256:  selected.SHA256,
		MCPath:         firstGlob(filepath.Join(root, "mc.linux-amd64*")),
		GoArchivePath:  firstGlob(filepath.Join(root, "go", "*", "go*.linux-amd64.tar.gz")),
		GoModCachePath: firstGlob(filepath.Join(root, "go", "cache", "gomodcache*.tar.gz")),
		RPMPaths:       listRPMs(filepath.Join(root, "rpms")),
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
	sum, err := sha256File(bundle.ArchivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, bundle.ArchiveSHA256) {
		return fmt.Errorf("minio archive sha256 mismatch: expected %s got %s", bundle.ArchiveSHA256, sum)
	}
	return nil
}

func looksLikeMinioArchive(name string) bool {
	return strings.HasPrefix(name, "minio-") && (strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"))
}

func firstGlob(pattern string) string {
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	for _, match := range matches {
		lower := strings.ToLower(filepath.Base(match))
		if strings.HasSuffix(lower, ".sha256sum") || strings.HasSuffix(lower, ".minisig") {
			continue
		}
		return match
	}
	return ""
}

func listRPMs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".rpm") {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(out)
	return out
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
