package docker

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

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
)

type Bundle struct {
	Version       string
	ArchivePath   string
	ArchiveSHA256 string
	RPMPaths      []string
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	return SelectBundleWithLanguage(resources, version, "")
}

func SelectBundleWithLanguage(resources []store.Resource, version, lang string) (Bundle, error) {
	var candidates []store.Resource
	for _, res := range resources {
		if res.App != "docker" || res.Part != "backend" {
			continue
		}
		if version != "" && version != "latest" && res.Version != version {
			continue
		}
		if looksLikeDockerArchive(res.Path) {
			candidates = append(candidates, res)
		}
	}
	if len(candidates) == 0 {
		if version == "" {
			version = "latest"
		}
		return Bundle{}, fmt.Errorf(i18n.Text(lang, "docker.noArchive"), version)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Version < candidates[j].Version
	})
	selected := candidates[len(candidates)-1]
	rpms, err := listRPMs(filepath.Join(filepath.Dir(selected.Path), "rpms"))
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{
		Version:       selected.Version,
		ArchivePath:   selected.Path,
		ArchiveSHA256: selected.SHA256,
		RPMPaths:      rpms,
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	return VerifyBundleWithLanguage(bundle, "")
}

func VerifyBundleWithLanguage(bundle Bundle, lang string) error {
	if bundle.ArchivePath == "" {
		return errors.New(i18n.Text(lang, "docker.archivePathEmpty"))
	}
	if _, err := os.Stat(bundle.ArchivePath); err != nil {
		return err
	}
	if bundle.ArchiveSHA256 == "" {
		return nil
	}
	sum, err := sha256File(bundle.ArchivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, bundle.ArchiveSHA256) {
		return fmt.Errorf(i18n.Text(lang, "docker.shaMismatch"), bundle.ArchiveSHA256, sum)
	}
	return nil
}

func looksLikeDockerArchive(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.Contains(name, "docker") && (strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".tar.gz"))
}

func listRPMs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".rpm") {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(out)
	return out, nil
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
