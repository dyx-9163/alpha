package mysql

import (
	_ "embed"
	"fmt"
	"path"
	"regexp"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

const (
	mysqlBackupWorkRoot = "/aifar/apps/mysql/_backup"
	maxLogicalThreads   = 64
	maxLogicalRateMBps  = 10240
)

var logicalTaskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

//go:embed templates/backup/logical-backup.sh
var logicalBackupScriptTemplate string

//go:embed templates/backup/logical-restore.sh
var logicalRestoreScriptTemplate string

//go:embed templates/backup/inspect.sql
var inspectSQLTemplate string

type logicalScriptTemplateData struct {
	WorkDir     string
	DumpDir     string
	Threads     int
	MaxRateMBps int
}

func RenderLogicalBackupScript(options LogicalBackupScriptOptions) (string, error) {
	if err := validateLogicalBackupOptions(options); err != nil {
		return "", err
	}
	return renderLogicalScript("logical-backup.sh", "mysql-logical-backup", logicalBackupScriptTemplate, logicalScriptTemplateData{
		WorkDir:     mysqlBackupWorkDir(options.TaskID),
		DumpDir:     path.Join(mysqlBackupWorkDir(options.TaskID), "dump"),
		Threads:     options.Threads,
		MaxRateMBps: options.MaxRateMBps,
	})
}

func RenderLogicalRestoreScript(options LogicalRestoreScriptOptions) (string, error) {
	if !validLogicalTaskID(options.TaskID) || !validLogicalThreads(options.Threads) {
		return "", fmt.Errorf("invalid controlled logical restore script options")
	}
	workDir := mysqlBackupWorkDir(options.TaskID)
	return renderLogicalScript("logical-restore.sh", "mysql-logical-restore", logicalRestoreScriptTemplate, logicalScriptTemplateData{
		WorkDir: workDir,
		DumpDir: path.Join(workDir, "dump"),
		Threads: options.Threads,
	})
}

// MySQLInspectSQL returns the fixed inspection queries used by later lifecycle
// steps. It has no caller interpolation surface.
func MySQLInspectSQL() string {
	return inspectSQLTemplate
}

func validateLogicalBackupOptions(options LogicalBackupScriptOptions) error {
	if !validLogicalTaskID(options.TaskID) || !validLogicalThreads(options.Threads) || options.MaxRateMBps < 0 || options.MaxRateMBps > maxLogicalRateMBps {
		return fmt.Errorf("invalid controlled logical backup script options")
	}
	return nil
}

func validLogicalTaskID(taskID string) bool {
	return logicalTaskID.MatchString(taskID)
}

func validLogicalThreads(threads int) bool {
	return threads >= 1 && threads <= maxLogicalThreads
}

func mysqlBackupWorkDir(taskID string) string {
	return path.Join(mysqlBackupWorkRoot, taskID)
}

func renderLogicalScript(templatePath, name, embedded string, data logicalScriptTemplateData) (string, error) {
	return installerkit.RenderTemplate("mysql", "backup/"+templatePath, name, embedded, template.FuncMap{}, data)
}
