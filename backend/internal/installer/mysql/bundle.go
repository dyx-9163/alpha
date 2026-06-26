package mysql

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
	Version       string
	ArchivePath   string
	ArchiveSHA256 string
	RPMPaths      []string
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "latest"
	}
	var candidates []store.Resource
	for _, res := range resources {
		name := strings.ToLower(filepath.Base(res.Path))
		if res.App != "mysql" || res.Part != "backend" {
			continue
		}
		if version != "latest" && res.Version != version {
			continue
		}
		if strings.HasSuffix(name, ".sha256sum") || strings.HasSuffix(name, ".minisig") {
			continue
		}
		if looksLikeMySQLBundle(name) {
			candidates = append(candidates, res)
		}
	}
	if len(candidates) == 0 {
		return Bundle{}, fmt.Errorf("mysql resource %s not found", version)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Version < candidates[j].Version
	})
	selected := candidates[len(candidates)-1]
	return Bundle{
		Version:       selected.Version,
		ArchivePath:   selected.Path,
		ArchiveSHA256: selected.SHA256,
		RPMPaths:      listRPMs(filepath.Join(filepath.Dir(selected.Path), "rpms")),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if strings.TrimSpace(bundle.ArchivePath) == "" {
		return errors.New("mysql official bundle is required")
	}
	if _, err := os.Stat(bundle.ArchivePath); err != nil {
		return err
	}
	if strings.TrimSpace(bundle.ArchiveSHA256) == "" {
		return nil
	}
	sum, err := sha256File(bundle.ArchivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, bundle.ArchiveSHA256) {
		return fmt.Errorf("mysql bundle sha256 mismatch: expected %s got %s", bundle.ArchiveSHA256, sum)
	}
	return nil
}

func looksLikeMySQLBundle(name string) bool {
	if !strings.Contains(name, "mysql") {
		return false
	}
	return strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".tar.xz")
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
