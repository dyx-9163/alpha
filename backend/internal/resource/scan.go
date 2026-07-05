package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aifar-deployment/backend/internal/store"
)

func ScanAndSave(s *store.Store, root string) error {
	resources, err := Scan(root)
	if err != nil {
		return err
	}
	return s.ReplaceResources(resources)
}

func Scan(root string) ([]store.Resource, error) {
	var out []store.Resource
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	appDirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, appDir := range appDirs {
		if !appDir.IsDir() {
			continue
		}
		app, part := parseAppPart(appDir.Name())
		versionDirs, err := os.ReadDir(filepath.Join(root, appDir.Name()))
		if err != nil {
			return nil, err
		}
		for _, versionDir := range versionDirs {
			if !versionDir.IsDir() {
				continue
			}
			version := versionDir.Name()
			versionPath := filepath.Join(root, appDir.Name(), version)
			scanned, err := scanVersion(app, part, version, versionPath)
			if err != nil {
				return nil, err
			}
			out = append(out, scanned...)
		}
	}
	return out, nil
}

func scanVersion(app, part, version, versionPath string) ([]store.Resource, error) {
	var out []store.Resource
	rpmCount := countRPMs(filepath.Join(versionPath, "rpms"))
	manifest := readVersionManifest(versionPath)
	entries, err := os.ReadDir(versionPath)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(versionPath, name)
		if name == "rpms" {
			continue
		}
		if strings.EqualFold(name, "manifest.json") {
			if !entry.IsDir() {
				if resource, ok, err := manifestEntryResource(app, part, version, path, rpmCount, manifest); err != nil {
					return nil, err
				} else if ok {
					out = append(out, resource)
				}
			}
			continue
		}
		if entry.IsDir() {
			nestedPart := normalizePart(name)
			if nestedPart == "" {
				continue
			}
			nested, err := scanResourceFiles(app, nestedPart, version, path, rpmCount, manifest, name)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		resourcePart := part
		hash, manifestPart := manifestResource(manifest, name)
		if manifestPart != "" {
			resourcePart = manifestPart
		}
		if shouldHash(name, info.Size()) {
			if hash == "" {
				hash, _ = sha256File(path)
			}
		}
		out = append(out, store.Resource{
			App:      app,
			Part:     resourcePart,
			Version:  version,
			Path:     path,
			Size:     info.Size(),
			SHA256:   hash,
			RPMCount: rpmCount,
		})
	}
	return out, nil
}

func manifestEntryResource(app, part, version, path string, rpmCount int, manifest map[string]manifestFile) (store.Resource, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return store.Resource{}, false, err
	}
	hash, manifestPart := manifestResource(manifest, filepath.Base(path))
	if manifestPart == "" {
		return store.Resource{}, false, nil
	}
	resourcePart := part
	if manifestPart != "" {
		resourcePart = manifestPart
	}
	if shouldHash(filepath.Base(path), info.Size()) && hash == "" {
		hash, _ = sha256File(path)
	}
	return store.Resource{
		App:      app,
		Part:     resourcePart,
		Version:  version,
		Path:     path,
		Size:     info.Size(),
		SHA256:   hash,
		RPMCount: rpmCount,
	}, true, nil
}

func scanResourceFiles(app, part, version, dir string, rpmCount int, manifest map[string]manifestFile, baseRel string) ([]store.Resource, error) {
	var out []store.Resource
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		rel := filepath.ToSlash(filepath.Join(baseRel, entry.Name()))
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		resourcePart := part
		hash, manifestPart := manifestResource(manifest, rel)
		if manifestPart != "" {
			resourcePart = manifestPart
		}
		if shouldHash(entry.Name(), info.Size()) {
			if hash == "" {
				hash, _ = sha256File(path)
			}
		}
		out = append(out, store.Resource{
			App:      app,
			Part:     resourcePart,
			Version:  version,
			Path:     path,
			Size:     info.Size(),
			SHA256:   hash,
			RPMCount: rpmCount,
		})
	}
	return out, nil
}

type manifestFile struct {
	SHA256 string `json:"sha256"`
	Part   string `json:"part"`
}

type versionManifest struct {
	Files map[string]manifestFile `json:"files"`
}

func readVersionManifest(versionPath string) map[string]manifestFile {
	path := filepath.Join(versionPath, "manifest.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var manifest versionManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil
	}
	out := map[string]manifestFile{}
	for name, file := range manifest.Files {
		key := filepath.ToSlash(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		file.Part = normalizeManifestPart(file.Part)
		out[key] = file
	}
	return out
}

func manifestResource(manifest map[string]manifestFile, rel string) (string, string) {
	if len(manifest) == 0 {
		return "", ""
	}
	rel = filepath.ToSlash(rel)
	if file, ok := manifest[rel]; ok {
		return strings.TrimSpace(file.SHA256), file.Part
	}
	if file, ok := manifest[filepath.Base(rel)]; ok {
		return strings.TrimSpace(file.SHA256), file.Part
	}
	return "", ""
}

func normalizeManifestPart(part string) string {
	if normalized := normalizePart(part); normalized != "" {
		return normalized
	}
	return strings.TrimSpace(part)
}

func parseAppPart(name string) (string, string) {
	lower := strings.ToLower(name)
	for _, suffix := range []string{"-frontend", "_frontend", ".frontend", "-front", "_front", "-web", "_web", "-ui", "_ui", "-前端", "_前端"} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimSuffix(name, name[len(name)-len(suffix):]), "frontend"
		}
	}
	for _, suffix := range []string{"-backend", "_backend", ".backend", "-server", "_server", "-api", "_api", "-后端", "_后端"} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimSuffix(name, name[len(name)-len(suffix):]), "backend"
		}
	}
	return name, "backend"
}

func normalizePart(name string) string {
	switch strings.ToLower(name) {
	case "frontend", "front", "web", "ui", "前端":
		return "frontend"
	case "backend", "server", "api", "后端":
		return "backend"
	default:
		return ""
	}
}

func countRPMs(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".rpm") {
			count++
		}
	}
	return count
}

func shouldHash(name string, size int64) bool {
	if strings.HasSuffix(name, ".sha256sum") || strings.HasSuffix(name, ".minisig") {
		return false
	}
	return size <= 512*1024*1024
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
