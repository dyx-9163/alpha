package agentdist

import (
	"os"
	"path/filepath"
)

var binaryNames = []string{"aifar-agent-linux-amd64", "aifar-agent"}

// FindBinary locates the aifar-agent binary in both packaged and local dev layouts.
func FindBinary() string {
	if value := os.Getenv("AIFAR_AGENT_BINARY"); value != "" {
		if fileExists(value) {
			return value
		}
		return ""
	}
	for _, root := range candidateRoots() {
		for _, name := range binaryNames {
			candidate := filepath.Join(root, name)
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func candidateRoots() []string {
	var roots []string
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots,
			cwd,
			filepath.Join(cwd, "bin"),
			filepath.Join(cwd, "..", "bin"),
			filepath.Join(cwd, "deploy", "bin"),
			filepath.Join(cwd, "..", "deploy", "bin"),
			filepath.Join(cwd, "..", "..", "bin"),
			filepath.Join(cwd, "..", "..", "deploy", "bin"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		roots = append(roots,
			dir,
			filepath.Join(dir, "bin"),
			filepath.Join(dir, "..", "bin"),
			filepath.Join(dir, "deploy", "bin"),
			filepath.Join(dir, "..", "deploy", "bin"),
			filepath.Join(dir, "..", "..", "bin"),
			filepath.Join(dir, "..", "..", "deploy", "bin"),
		)
	}
	roots = append(roots,
		filepath.Join("deploy", "bin"),
		"bin",
		".",
	)
	return uniqueCleanRoots(roots)
}

func uniqueCleanRoots(roots []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		clean := filepath.Clean(root)
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		result = append(result, clean)
	}
	return result
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
