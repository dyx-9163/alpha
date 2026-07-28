package mysql

import (
	"bytes"
	_ "embed"
	"fmt"
	"path"
	"regexp"
	"text/template"
)

const (
	maxLogicalThreads  = 64
	maxLogicalRateMBps = 10240
)

var (
	mysqlBackupWorkRoot = "/aifar/apps/mysql/_backup"
	mysqlInstallRoot    = "/aifar/apps/mysql"
)

var logicalTaskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

//go:embed templates/backup/logical-backup.sh
var logicalBackupScriptTemplate string

//go:embed templates/backup/logical-restore.sh
var logicalRestoreScriptTemplate string

//go:embed templates/backup/inspect.sql
var inspectSQLTemplate string

type logicalScriptTemplateData struct {
	MySQLShell  string
	BackupRoot  string
	TaskID      string
	Threads     int
	MaxRateMBps int
}

func RenderLogicalBackupScript(options LogicalBackupScriptOptions) (string, error) {
	if err := validateLogicalBackupOptions(options); err != nil {
		return "", err
	}
	return renderLogicalScript("logical-backup.sh", "mysql-logical-backup", logicalBackupScriptTemplate, logicalScriptTemplateData{
		MySQLShell:  path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh"),
		BackupRoot:  mysqlBackupWorkRoot,
		TaskID:      options.TaskID,
		Threads:     options.Threads,
		MaxRateMBps: options.MaxRateMBps,
	})
}

func RenderLogicalRestoreScript(options LogicalRestoreScriptOptions) (string, error) {
	if !validLogicalTaskID(options.TaskID) || !validLogicalThreads(options.Threads) {
		return "", fmt.Errorf("invalid controlled logical restore script options")
	}
	return renderLogicalScript("logical-restore.sh", "mysql-logical-restore", logicalRestoreScriptTemplate, logicalScriptTemplateData{
		MySQLShell: path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh"),
		BackupRoot: mysqlBackupWorkRoot,
		TaskID:     options.TaskID,
		Threads:    options.Threads,
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
	tpl, err := template.New(name).Parse(embedded)
	if err != nil {
		return "", fmt.Errorf("parse fixed MySQL backup template %s: %w", templatePath, err)
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, data); err != nil {
		return "", fmt.Errorf("render fixed MySQL backup template %s: %w", templatePath, err)
	}
	return output.String(), nil
}
