package resourcekit

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

type SelectOptions struct {
	App            string
	Part           string
	Version        string
	Match          func(baseLower string, res store.Resource) bool
	SkipSignatures bool
}

func Select(resources []store.Resource, opts SelectOptions) (store.Resource, bool) {
	version := NormalizeVersion(opts.Version)
	candidates := make([]store.Resource, 0)
	for _, res := range resources {
		baseLower := strings.ToLower(filepath.Base(res.Path))
		if res.App != opts.App || res.Part != opts.Part {
			continue
		}
		if version != "latest" && res.Version != version {
			continue
		}
		if opts.SkipSignatures && IsSignatureFile(baseLower) {
			continue
		}
		if opts.Match != nil && !opts.Match(baseLower, res) {
			continue
		}
		candidates = append(candidates, res)
	}
	if len(candidates) == 0 {
		return store.Resource{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Version < candidates[j].Version
	})
	return candidates[len(candidates)-1], true
}

func NormalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "latest"
	}
	return version
}

func IsSignatureFile(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(name, ".sha256sum") || strings.HasSuffix(name, ".minisig")
}

func ListRPMs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".rpm") {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(out)
	return out
}

func FirstGlob(pattern string, skipSignatures bool) string {
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	for _, match := range matches {
		if skipSignatures && IsSignatureFile(filepath.Base(match)) {
			continue
		}
		return match
	}
	return ""
}

func VerifyFile(pathValue, label string) error {
	if strings.TrimSpace(pathValue) == "" {
		if strings.TrimSpace(label) == "" {
			label = "file"
		}
		return fmt.Errorf("%s is required", label)
	}
	if _, err := os.Stat(pathValue); err != nil {
		return err
	}
	return nil
}

func SHA256File(pathValue string) (string, error) {
	f, err := os.Open(pathValue)
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

func VerifySHA256(pathValue, expected, label string) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil
	}
	if strings.TrimSpace(pathValue) == "" {
		return errors.New("sha256 target path is required")
	}
	sum, err := SHA256File(pathValue)
	if err != nil {
		return err
	}
	if strings.EqualFold(sum, expected) {
		return nil
	}
	if strings.TrimSpace(label) == "" {
		label = filepath.Base(pathValue)
	}
	return fmt.Errorf("%s sha256 mismatch: expected %s got %s", label, expected, sum)
}
