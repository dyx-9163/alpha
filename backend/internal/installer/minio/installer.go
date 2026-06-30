package minio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/store"
)

type Logger = installerkit.Logger

type Remote = installerkit.Remote

type Installer struct {
	remote Remote
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, req InstallOptions, log Logger) error {
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "minio", bundle.Version, time.Now())
	installRoot := path.Join(deployDir, "minio", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	goArchiveRemote := workDir + "/" + filepath.Base(bundle.GoArchivePath)
	goModCacheRemote := workDir + "/" + filepath.Base(bundle.GoModCachePath)
	mcRemote := ""
	if strings.TrimSpace(bundle.MCPath) != "" {
		mcRemote = workDir + "/" + filepath.Base(bundle.MCPath)
	}

	log.Info("prepare MinIO work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+installerkit.ShellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}

	uploads := []uploadkit.File{
		{
			LocalPath:      bundle.ArchivePath,
			RemotePath:     archiveRemote,
			LogMessage:     "upload MinIO source archive: %s",
			LogArgs:        []any{bundle.ArchivePath},
			FailureMessage: "upload minio archive failed",
		},
		{
			LocalPath:      bundle.GoArchivePath,
			RemotePath:     goArchiveRemote,
			LogMessage:     "upload Go toolchain archive: %s",
			LogArgs:        []any{bundle.GoArchivePath},
			FailureMessage: "upload minio go toolchain failed",
		},
		{
			LocalPath:      bundle.GoModCachePath,
			RemotePath:     goModCacheRemote,
			LogMessage:     "upload Go module cache archive: %s",
			LogArgs:        []any{bundle.GoModCachePath},
			FailureMessage: "upload minio go module cache failed",
		},
	}
	if mcRemote != "" {
		uploads = append(uploads, uploadkit.File{
			LocalPath:      bundle.MCPath,
			RemotePath:     mcRemote,
			Mode:           0o755,
			LogMessage:     "upload MinIO client: %s",
			LogArgs:        []any{bundle.MCPath},
			FailureMessage: "upload minio client failed",
		})
	}
	uploads = append(uploads, uploadkit.RPMFiles(bundle.RPMPaths, workDir+"/rpms", "upload MinIO RPM dependency: %s", "upload minio rpm %s failed")...)
	for _, file := range uploads {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}

	volumeDirs := minioVolumeDirs(req.DataDir, req.DataDirs, installRoot)
	script, err := installStandaloneScript(InstallScriptRequest{
		Version:        bundle.Version,
		WorkDir:        workDir,
		ArchivePath:    archiveRemote,
		GoArchivePath:  goArchiveRemote,
		GoModCachePath: goModCacheRemote,
		MCRemotePath:   mcRemote,
		InstallRoot:    installRoot,
		DataDir:        volumeDirs[0],
		DataDirs:       volumeDirs,
		VolumeList:     strings.Join(volumeDirs, " "),
		APIPort:        req.APIPort,
		ConsolePort:    req.ConsolePort,
		RootUser:       req.RootUser,
		RootPassword:   req.RootPassword,
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-minio.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-minio-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload MinIO installer script",
		FailureMessage: "upload minio installer script failed",
	}, log); err != nil {
		return err
	}
	log.Info("install MinIO standalone service")
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log); err != nil {
		return err
	}
	log.Info("MinIO %s installed and verified on ports %d/%d", bundle.Version, req.APIPort, req.ConsolePort)
	return nil
}

func (i Installer) ResolveDataDir(ctx context.Context, server store.Server, req DataDirRequest, log Logger) (string, error) {
	dataDirs, err := i.ResolveDataDirs(ctx, server, req, log)
	if err != nil {
		return "", err
	}
	if len(dataDirs) == 0 {
		return "", errors.New("MinIO data directory was not selected")
	}
	return dataDirs[0], nil
}

func (i Installer) ResolveDataDirs(ctx context.Context, server store.Server, req DataDirRequest, log Logger) ([]string, error) {
	systemDir := path.Join(req.InstallRoot, "data")
	selectedDir := preferredMinIODataDir(req.DataRoot, req.APIPort)
	if selectedDir == "" {
		selectedDir = systemDir
	}
	switch normalizeMinIOStorageMode(req.Mode) {
	case StorageModeLocalDisk:
		log.Info("using MinIO local data directory: %s", selectedDir)
		if _, err := i.run(ctx, server, minioLocalDataDirCommand(selectedDir), log); err != nil {
			return nil, err
		}
		return []string{selectedDir}, nil
	case StorageModeUnmountedDisk:
		devices := minioRequestDiskDevices(req)
		if len(devices) == 0 {
			return nil, errors.New("MinIO unmounted disk mode requires a disk device")
		}
		if strings.TrimSpace(req.DataRoot) == "" {
			return nil, errors.New("MinIO unmounted disk mode requires a data root")
		}
		dataDirs := make([]string, 0, len(devices))
		for idx, device := range devices {
			mountRoot := minioDiskMountRoot(req.DataRoot, idx)
			dataDir := path.Join(mountRoot, "minio")
			log.Info("preparing unmounted MinIO data disk: %s -> %s", device, mountRoot)
			result, err := i.run(ctx, server, minioUnmountedDiskDataDirCommand(device, mountRoot, dataDir), log)
			if err != nil {
				return nil, err
			}
			selected := selectedDataDirFromOutput(result.Stdout)
			if selected == "" {
				selected = dataDir
			}
			log.Info("selected MinIO data directory: %s", selected)
			dataDirs = append(dataDirs, selected)
		}
		return dataDirs, nil
	default:
		return nil, fmt.Errorf("unsupported MinIO storage mode: %s", req.Mode)
	}
}

func (i Installer) ConfigureDistributedNode(ctx context.Context, server store.Server, req DistributedNodeConfig, log Logger) error {
	script, err := configureDistributedNodeScript(req)
	if err != nil {
		return err
	}
	_, err = i.run(ctx, server, "sh -s <<'AIFAR_MINIO_DISTRIBUTED_CONFIGURE'\n"+script+"\nAIFAR_MINIO_DISTRIBUTED_CONFIGURE", log)
	return err
}

type InstallOptions struct {
	APIPort      int
	ConsolePort  int
	RootUser     string
	RootPassword string
	DataDir      string
	DataDirs     []string
}

const (
	StorageModeLocalDisk     = "local-disk"
	StorageModeUnmountedDisk = "unmounted-disk"
)

type DataDirRequest struct {
	Mode        string
	DataRoot    string
	DiskDevice  string
	DiskDevices []string
	InstallRoot string
	APIPort     int
}

func (o InstallOptions) Validate() error {
	if o.APIPort <= 0 || o.APIPort > 65535 {
		return fmt.Errorf("invalid MinIO API port: %d", o.APIPort)
	}
	if o.ConsolePort <= 0 || o.ConsolePort > 65535 {
		return fmt.Errorf("invalid MinIO console port: %d", o.ConsolePort)
	}
	if o.APIPort == o.ConsolePort {
		return errors.New("MinIO API port and console port must be different")
	}
	if strings.TrimSpace(o.RootUser) == "" {
		return errors.New("MinIO root user is required")
	}
	if strings.TrimSpace(o.RootPassword) == "" {
		return errors.New("MinIO root password is required")
	}
	if strings.IndexFunc(o.RootUser, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO root user must not contain whitespace")
	}
	if strings.IndexFunc(o.RootPassword, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO root password must not contain whitespace")
	}
	if len(o.RootPassword) < 8 {
		return errors.New("MinIO root password must be at least 8 characters")
	}
	if strings.TrimSpace(o.DataDir) != "" && !strings.HasPrefix(strings.TrimSpace(o.DataDir), "/") {
		return errors.New("MinIO data directory must be an absolute path")
	}
	for _, dir := range o.DataDirs {
		if strings.TrimSpace(dir) != "" && !strings.HasPrefix(strings.TrimSpace(dir), "/") {
			return errors.New("MinIO data directory must be an absolute path")
		}
	}
	return nil
}

func minioDataDir(dataDir, installRoot string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return path.Join(installRoot, "data")
	}
	return "/" + strings.Trim(dataDir, "/")
}

func minioVolumeDirs(dataDir string, dataDirs []string, installRoot string) []string {
	out := make([]string, 0, len(dataDirs)+1)
	for _, dir := range dataDirs {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			out = append(out, minioDataDir(dir, installRoot))
		}
	}
	if len(out) == 0 {
		out = append(out, minioDataDir(dataDir, installRoot))
	}
	return out
}

func preferredMinIODataDir(dataRoot string, apiPort int) string {
	dataRoot = strings.TrimSpace(dataRoot)
	if dataRoot == "" {
		return ""
	}
	dataRoot = "/" + strings.Trim(dataRoot, "/")
	return path.Join(dataRoot, fmt.Sprintf("aifar-minio-%d", apiPort))
}

func minioRequestDiskDevices(req DataDirRequest) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(req.DiskDevices)+1)
	add := func(device string) {
		device = strings.TrimSpace(device)
		if device == "" || seen[device] {
			return
		}
		seen[device] = true
		out = append(out, device)
	}
	for _, device := range req.DiskDevices {
		add(device)
	}
	add(req.DiskDevice)
	return out
}

func minioDiskMountRoot(dataRoot string, index int) string {
	dataRoot = "/" + strings.Trim(strings.TrimSpace(dataRoot), "/")
	return path.Join(dataRoot, fmt.Sprintf("disk%d", index+1))
}

func normalizeMinIOStorageMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "local", "local-disk", "local-dir", "directory":
		return StorageModeLocalDisk
	case "unmounted", "unmounted-disk", "raw-disk", "disk", "device":
		return StorageModeUnmountedDisk
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func minioLocalDataDirCommand(dataDir string) string {
	return strings.Join([]string{
		"SUDO=\"\"",
		"if [ \"$(id -u)\" != \"0\" ]; then SUDO=\"sudo -n\"; fi",
		"DATA_DIR=" + installerkit.ShellQuote(dataDir),
		"$SUDO mkdir -p \"$DATA_DIR\"",
		"echo \"using local MinIO data directory: $DATA_DIR\"",
		"echo \"AIFAR_SELECTED_MINIO_DATA_DIR=$DATA_DIR\"",
	}, "\n")
}

func minioUnmountedDiskDataDirCommand(device, mountRoot, dataDir string) string {
	mountRoot = "/" + strings.Trim(strings.TrimSpace(mountRoot), "/")
	return strings.Join([]string{
		"SUDO=\"\"",
		"if [ \"$(id -u)\" != \"0\" ]; then SUDO=\"sudo -n\"; fi",
		"DATA_DEVICE=" + installerkit.ShellQuote(strings.TrimSpace(device)),
		"MOUNT_ROOT=" + installerkit.ShellQuote(mountRoot),
		"DATA_DIR=" + installerkit.ShellQuote(dataDir),
		"command -v lsblk >/dev/null 2>&1 || { echo \"lsblk is required to prepare MinIO data disk\"; exit 1; }",
		"command -v mkfs.ext4 >/dev/null 2>&1 || { echo \"mkfs.ext4 is required to prepare MinIO data disk\"; exit 1; }",
		"command -v blkid >/dev/null 2>&1 || { echo \"blkid is required to prepare MinIO data disk\"; exit 1; }",
		"[ -b \"$DATA_DEVICE\" ] || { echo \"MinIO data device is not a block device: $DATA_DEVICE\"; exit 1; }",
		"DEVICE_TYPE=\"$(lsblk -dn -o TYPE \"$DATA_DEVICE\" | head -n 1)\"",
		"if [ \"$DEVICE_TYPE\" != \"disk\" ]; then",
		"  echo \"MinIO data device must be an unpartitioned disk: $DATA_DEVICE ($DEVICE_TYPE)\"",
		"  exit 1",
		"fi",
		"DEVICE_RO=\"$(lsblk -dn -o RO \"$DATA_DEVICE\" | head -n 1)\"",
		"if [ \"$DEVICE_RO\" = \"1\" ]; then",
		"  echo \"MinIO data disk is read-only: $DATA_DEVICE\"",
		"  exit 1",
		"fi",
		"DEVICE_RM=\"$(lsblk -dn -o RM \"$DATA_DEVICE\" | head -n 1)\"",
		"if [ \"$DEVICE_RM\" = \"1\" ]; then",
		"  echo \"MinIO data disk is removable: $DATA_DEVICE\"",
		"  exit 1",
		"fi",
		"DEVICE_FSTYPE=\"$(lsblk -dn -o FSTYPE \"$DATA_DEVICE\" | head -n 1)\"",
		"if [ \"$DEVICE_FSTYPE\" = \"iso9660\" ] || [ \"$DEVICE_FSTYPE\" = \"udf\" ]; then",
		"  echo \"MinIO data disk looks like optical media: $DATA_DEVICE ($DEVICE_FSTYPE)\"",
		"  exit 1",
		"fi",
		"if lsblk -nr -o MOUNTPOINT \"$DATA_DEVICE\" | grep -q .; then",
		"  echo \"MinIO data device or one of its partitions is already mounted: $DATA_DEVICE\"",
		"  exit 1",
		"fi",
		"if [ \"$(lsblk -nr \"$DATA_DEVICE\" | wc -l)\" -gt 1 ]; then",
		"  echo \"MinIO data disk has partitions; select a clean unmounted disk or prepare it manually: $DATA_DEVICE\"",
		"  exit 1",
		"fi",
		"if findmnt \"$MOUNT_ROOT\" >/dev/null 2>&1; then",
		"  echo \"MinIO mount root is already mounted: $MOUNT_ROOT\"",
		"  exit 1",
		"fi",
		"if [ -d \"$MOUNT_ROOT\" ] && find \"$MOUNT_ROOT\" -mindepth 1 -maxdepth 1 | grep -q .; then",
		"  echo \"MinIO mount root must be empty before mounting data disk: $MOUNT_ROOT\"",
		"  exit 1",
		"fi",
		"echo \"formatting MinIO data device as ext4: $DATA_DEVICE\"",
		"$SUDO mkfs.ext4 -F \"$DATA_DEVICE\"",
		"UUID=\"$(blkid -s UUID -o value \"$DATA_DEVICE\")\"",
		"[ -n \"$UUID\" ] || { echo \"unable to read UUID from MinIO data device: $DATA_DEVICE\"; exit 1; }",
		"$SUDO mkdir -p \"$MOUNT_ROOT\"",
		"FSTAB_TMP=\"$(mktemp)\"",
		"if [ -f /etc/fstab ]; then awk -v mount_root=\"$MOUNT_ROOT\" '$2 != mount_root {print}' /etc/fstab > \"$FSTAB_TMP\"; else : > \"$FSTAB_TMP\"; fi",
		"echo \"UUID=$UUID $MOUNT_ROOT ext4 defaults,nofail 0 2\" >> \"$FSTAB_TMP\"",
		"$SUDO install -m 0644 \"$FSTAB_TMP\" /etc/fstab",
		"rm -f \"$FSTAB_TMP\"",
		"$SUDO mount \"$MOUNT_ROOT\"",
		"$SUDO mkdir -p \"$DATA_DIR\"",
		"echo \"mounted MinIO data disk: $DATA_DEVICE -> $MOUNT_ROOT\"",
		"echo \"AIFAR_SELECTED_MINIO_DATA_DIR=$DATA_DIR\"",
	}, "\n")
}

func selectedDataDirFromOutput(output string) string {
	re := regexp.MustCompile(`(?m)^AIFAR_SELECTED_MINIO_DATA_DIR=(/.+)$`)
	match := re.FindStringSubmatch(output)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "minio remote command failed")
}
