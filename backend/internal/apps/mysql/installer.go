package mysql

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
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

type InstallOptions struct {
	Port         int
	RootUser     string
	RootPassword string
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, req InstallOptions, log Logger) (retErr error) {
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "mysql", bundle.Version, time.Now())
	installRoot := installerkit.InstallRoot(deployDir, "mysql")
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	credentialRemote := path.Join(workDir, "mysql-credential.context")
	credentialLocal, err := writeMySQLInstallCredentialContext(req.RootUser, req.RootPassword)
	if err != nil {
		return errors.New("unable to create MySQL install credential context")
	}
	remotePrepared := false
	defer func() {
		if cleanupErr := i.cleanupInstallCredentials(ctx, server, workDir, credentialLocal, remotePrepared); cleanupErr != nil {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()

	log.Info("prepare MySQL work directory: %s", workDir)
	prepareWork := "set -eu; install -d -m 0700 " + installerkit.ShellQuote(workDir) + "; test ! -L " + installerkit.ShellQuote(workDir) + "; install -d -m 0700 " + installerkit.ShellQuote(workDir+"/rpms") + "; test ! -L " + installerkit.ShellQuote(workDir+"/rpms")
	if _, err := i.run(ctx, server, prepareWork, log); err != nil {
		return err
	}
	remotePrepared = true
	if err := i.remote.UploadFile(ctx, server, credentialLocal, credentialRemote, 0o600); err != nil {
		return errors.New("upload MySQL credential context failed")
	}

	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      bundle.ArchivePath,
		RemotePath:     archiveRemote,
		LogMessage:     "upload MySQL official bundle: %s",
		LogArgs:        []any{bundle.ArchivePath},
		FailureMessage: "upload mysql bundle failed",
	}, log); err != nil {
		return err
	}
	for _, file := range uploadkit.RPMFiles(bundle.RPMPaths, workDir+"/rpms", "upload MySQL RPM dependency: %s", "upload mysql rpm %s failed") {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}

	script, err := installStandaloneScript(InstallScriptRequest{
		Version:     bundle.Version,
		WorkDir:     workDir,
		ArchivePath: archiveRemote,
		InstallRoot: installRoot,
		ReportHost:  strings.TrimSpace(server.Host),
		Port:        req.Port,
		ServerID:    mysqlServerID(server, req.Port),
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-mysql.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-mysql-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload MySQL installer script",
		FailureMessage: "upload mysql installer script failed",
	}, log); err != nil {
		return err
	}
	secureLog := mysqlSanitizedLogger{base: log, secrets: []string{req.RootPassword}}
	secureLog.Info("install MySQL service")
	if _, err := i.runSanitized(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote)+" "+installerkit.ShellQuote(credentialRemote), secureLog, req.RootPassword); err != nil {
		return err
	}
	secureLog.Info("MySQL %s installed and verified on port %d", bundle.Version, req.Port)
	return nil
}

func (i Installer) BootstrapInnoDBCluster(ctx context.Context, server store.Server, req InnoDBClusterBootstrapRequest, log Logger) error {
	connections := make([]mysqlConnectionCredential, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		connections = append(connections, mysqlConnectionCredential{Host: node.Host, Port: node.Port, User: req.RootUser, Password: req.RootPassword})
	}
	return i.runClusterCredentialScript(ctx, server, "bootstrap", connections, log, func(work, credentialPath string) (string, error) {
		return bootstrapInnoDBClusterScript(InnoDBClusterBootstrapScriptRequest{
			ClusterName: req.ClusterName, InstallRoot: req.InstallRoot, CredentialContextPath: credentialPath, Nodes: req.Nodes,
		})
	})
}

func (i Installer) StartInnoDBCluster(ctx context.Context, server store.Server, req InnoDBClusterStartRequest, log Logger) error {
	connections := req.Connections
	return i.runClusterCredentialScript(ctx, server, "start", connections, log, func(work, credentialPath string) (string, error) {
		return startInnoDBClusterScript(InnoDBClusterStartScriptRequest{
			ClusterName: req.ClusterName, InstallRoot: req.InstallRoot, CredentialContextPath: credentialPath, Nodes: req.Nodes,
		})
	})
}

func (i Installer) runClusterCredentialScript(ctx context.Context, server store.Server, action string, connections []mysqlConnectionCredential, log Logger, render func(work, credentialPath string) (string, error)) (retErr error) {
	if len(connections) == 0 {
		return mysqlOperationError(MySQLCredentialUnavailable)
	}
	workDir := installerkit.WorkDir(installerkit.RemoteDeployDir(server.DeployDir), "mysql-credential-"+action, "context", time.Now())
	credentialRemote := path.Join(workDir, "credential-context.json")
	scriptRemote := path.Join(workDir, action+"-cluster.sh")
	credentialLocal, err := writeMySQLJSONCredentialContext(connections)
	if err != nil {
		return errors.New("unable to create MySQL credential context")
	}
	remotePrepared := false
	defer func() {
		if cleanupErr := i.cleanupCredentialWork(ctx, server, workDir, credentialLocal, remotePrepared); cleanupErr != nil {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()
	if _, err := i.remote.Run(ctx, server, "set -eu; install -d -m 0700 "+installerkit.ShellQuote(workDir)+"; test ! -L "+installerkit.ShellQuote(workDir)); err != nil {
		return errors.New("unable to prepare MySQL credential work directory")
	}
	remotePrepared = true
	if err := i.remote.UploadFile(ctx, server, credentialLocal, credentialRemote, 0o600); err != nil {
		return errors.New("upload MySQL credential context failed")
	}
	script, err := render(workDir, credentialRemote)
	if err != nil {
		return err
	}
	scriptLocal, err := installerkit.WriteTempScript("aifar-mysql-"+action+"-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := i.remote.UploadFile(ctx, server, scriptLocal, scriptRemote, 0o700); err != nil {
		return errors.New("upload MySQL cluster script failed")
	}
	secrets := mysqlCredentialSecrets(connections)
	_, err = i.runSanitized(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), mysqlSanitizedLogger{base: log, secrets: secrets}, secrets...)
	return err
}

func (i Installer) cleanupInstallCredentials(ctx context.Context, server store.Server, workDir, localPath string, remotePrepared bool) error {
	var cleanupErrors []error
	if remotePrepared {
		cleanupCtx, cancel := mysqlCredentialCleanupContext(ctx)
		command := "set -eu; rm -f -- " + installerkit.ShellQuote(path.Join(workDir, "mysql-credential.context")) + " " + installerkit.ShellQuote(path.Join(workDir, "secure-root.sql")) + " " + installerkit.ShellQuote(path.Join(workDir, "secure-client.cnf"))
		if _, err := i.remote.Run(cleanupCtx, server, command); err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("unable to clean remote MySQL install credentials"))
		}
		cancel()
	}
	if err := removeMySQLCredentialContext(localPath); err != nil {
		cleanupErrors = append(cleanupErrors, errors.New("unable to clean local MySQL install credentials"))
	}
	return errors.Join(cleanupErrors...)
}

func (i Installer) cleanupCredentialWork(ctx context.Context, server store.Server, workDir, localPath string, remotePrepared bool) error {
	var cleanupErrors []error
	if remotePrepared {
		cleanupCtx, cancel := mysqlCredentialCleanupContext(ctx)
		if _, err := i.remote.Run(cleanupCtx, server, mysqlCredentialWorkCleanupCommand(workDir)); err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("unable to clean remote MySQL credential work directory"))
		}
		cancel()
	}
	if err := removeMySQLCredentialContext(localPath); err != nil {
		cleanupErrors = append(cleanupErrors, errors.New("unable to clean local MySQL credential context"))
	}
	return errors.Join(cleanupErrors...)
}

func (o InstallOptions) Validate() error {
	if o.Port <= 0 || o.Port > 65535 {
		return fmt.Errorf("invalid MySQL port: %d", o.Port)
	}
	if strings.TrimSpace(o.RootUser) == "" {
		return errors.New("MySQL root user is required")
	}
	if strings.TrimSpace(o.RootPassword) == "" {
		return errors.New("MySQL root password is required")
	}
	if strings.IndexFunc(o.RootUser, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MySQL root user must not contain whitespace")
	}
	if strings.IndexFunc(o.RootPassword, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MySQL root password must not contain whitespace")
	}
	if len(o.RootPassword) < 8 {
		return errors.New("MySQL root password must be at least 8 characters")
	}
	return nil
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "mysql remote command failed")
}

func (i Installer) runSanitized(ctx context.Context, server store.Server, command string, log Logger, secrets ...string) (installerkit.CommandResult, error) {
	result, err := i.remote.Run(ctx, server, command)
	result.Stdout = sanitizeMySQLCredentialText(result.Stdout, secrets...)
	result.Stderr = sanitizeMySQLCredentialText(result.Stderr, secrets...)
	installerkit.LogCommandResult(result, err, log)
	if err != nil {
		return result, errors.New("mysql remote command failed")
	}
	return result, nil
}

func mysqlServerID(server store.Server, port int) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(server.ID)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.TrimSpace(server.Host)))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(fmt.Sprint(port)))
	id := h.Sum32()
	if id == 0 {
		return 1
	}
	return id
}
