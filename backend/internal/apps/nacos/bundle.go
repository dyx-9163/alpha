package nacos

import (
	"fmt"
	"path/filepath"
	"strings"

	"aifar-deployment/backend/internal/resourcekit"
	"aifar-deployment/backend/internal/store"
)

type Bundle struct {
	Version        string
	ArchivePath    string
	ArchiveSHA256  string
	JDKX64Path     string
	JDKAarch64Path string
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	selected, ok := resourcekit.Select(resources, resourcekit.SelectOptions{
		App:            "nacos",
		Part:           "backend",
		Version:        version,
		SkipSignatures: true,
		Match: func(baseLower string, _ store.Resource) bool {
			return strings.HasPrefix(baseLower, "nacos-server-") && (strings.HasSuffix(baseLower, ".tar.gz") || strings.HasSuffix(baseLower, ".tgz"))
		},
	})
	if !ok {
		version = resourcekit.NormalizeVersion(version)
		return Bundle{}, fmt.Errorf("nacos resource %s not found", version)
	}
	root := filepath.Dir(selected.Path)
	return Bundle{
		Version:        selected.Version,
		ArchivePath:    selected.Path,
		ArchiveSHA256:  selected.SHA256,
		JDKX64Path:     firstNacosGlob(root, "*jdk*x64*linux*.tar.gz"),
		JDKAarch64Path: firstNacosGlob(root, "*jdk*aarch64*linux*.tar.gz"),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if err := resourcekit.VerifyFile(bundle.ArchivePath, "nacos archive"); err != nil {
		return err
	}
	if err := resourcekit.VerifySHA256(bundle.ArchivePath, bundle.ArchiveSHA256, "nacos archive"); err != nil {
		return err
	}
	if err := resourcekit.VerifyFile(bundle.JDKX64Path, "nacos x64 JDK archive"); err != nil {
		return err
	}
	if err := resourcekit.VerifyFile(bundle.JDKAarch64Path, "nacos aarch64 JDK archive"); err != nil {
		return err
	}
	return nil
}

func firstNacosGlob(root, pattern string) string {
	return resourcekit.FirstGlob(filepath.Join(root, pattern), true)
}
