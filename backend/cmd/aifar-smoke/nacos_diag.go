package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/store"
)

type nacosDiagnoseReport struct {
	Command      string                 `json:"command"`
	StartedAt    time.Time              `json:"startedAt"`
	FinishedAt   time.Time              `json:"finishedAt"`
	DatabasePath string                 `json:"databasePath"`
	OutputPath   string                 `json:"outputPath,omitempty"`
	Servers      []nacosServerDiagnose  `json:"servers"`
	Findings     []nacosDiagnoseFinding `json:"findings"`
	Passed       bool                   `json:"passed"`
	Failures     []string               `json:"failures"`
}

type nacosServerDiagnose struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Host           string   `json:"host"`
	Status         string   `json:"status"`
	DurationMS     int64    `json:"durationMs"`
	Summary        []string `json:"summary,omitempty"`
	Stdout         string   `json:"stdout,omitempty"`
	Stderr         string   `json:"stderr,omitempty"`
	Error          string   `json:"error,omitempty"`
	ServiceActive  string   `json:"serviceActive,omitempty"`
	ServiceEnabled string   `json:"serviceEnabled,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	DBPlatform     string   `json:"dbPlatform,omitempty"`
	DBConfigured   bool     `json:"dbConfigured"`
	DBHost         string   `json:"dbHost,omitempty"`
	DBPort         string   `json:"dbPort,omitempty"`
	DBTCP          string   `json:"dbTcp,omitempty"`
	DBQuery        string   `json:"dbQuery,omitempty"`
	ClusterNodes   []string `json:"clusterNodes,omitempty"`
	ListeningPorts []string `json:"listeningPorts,omitempty"`
}

type nacosDiagnoseFinding struct {
	ServerID string `json:"serverId,omitempty"`
	Level    string `json:"level"`
	Message  string `json:"message"`
}

func runNacosDiagnose(args []string) int {
	cfg := config.Load()
	flags := flag.NewFlagSet("nacos-diagnose", flag.ContinueOnError)
	databasePath := flags.String("database", cfg.DatabasePath, "SQLite database path")
	outputRoot := flags.String("output-dir", defaultOutputRoot(), "directory for JSON smoke reports")
	serverIDsFlag := flags.String("server-ids", os.Getenv("AIFAR_NACOS_DIAG_SERVER_IDS"), "comma-separated server IDs; defaults to all configured servers")
	timeout := flags.Duration("timeout", 45*time.Second, "per-server SSH diagnostic timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	rep := nacosDiagnoseReport{
		Command:      "nacos-diagnose",
		StartedAt:    time.Now(),
		DatabasePath: *databasePath,
	}
	db, err := store.OpenReadOnlyWithSecret(*databasePath, cfg.CredentialSecret)
	if err != nil {
		rep.Failures = append(rep.Failures, "open read-only database: "+cleanErrText(err.Error()))
		return finishNacosDiagnose(*outputRoot, &rep)
	}
	defer db.Close()

	servers, err := loadDiagnoseServers(db, splitCSV(*serverIDsFlag))
	if err != nil {
		rep.Failures = append(rep.Failures, cleanErrText(err.Error()))
		return finishNacosDiagnose(*outputRoot, &rep)
	}
	if len(servers) == 0 {
		rep.Failures = append(rep.Failures, "no configured servers to diagnose")
		return finishNacosDiagnose(*outputRoot, &rep)
	}

	remote := adapter.SSHRemote{}
	for _, server := range servers {
		item := diagnoseNacosServer(remote, server, *timeout)
		rep.Servers = append(rep.Servers, item)
		rep.Findings = append(rep.Findings, nacosFindingsForServer(item)...)
		if item.Status != "passed" {
			rep.Failures = append(rep.Failures, fmt.Sprintf("%s(%s): %s", item.Name, item.Host, item.Error))
		}
	}
	rep.Passed = len(rep.Failures) == 0 && noErrorFindings(rep.Findings)
	return finishNacosDiagnose(*outputRoot, &rep)
}

func loadDiagnoseServers(db *store.Store, ids []string) ([]store.Server, error) {
	publicServers, err := db.ListServers()
	if err != nil {
		return nil, err
	}
	wanted := setFromStrings(ids)
	var out []store.Server
	for _, public := range publicServers {
		if len(wanted) > 0 && !wanted[public.ID] {
			continue
		}
		server, err := db.GetServer(public.ID, true)
		if err != nil {
			return nil, err
		}
		out = append(out, server)
	}
	if len(wanted) > 0 && len(out) != len(wanted) {
		found := map[string]bool{}
		for _, server := range out {
			found[server.ID] = true
		}
		var missing []string
		for id := range wanted {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("server IDs not found: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func diagnoseNacosServer(remote adapter.SSHRemote, server store.Server, timeout time.Duration) nacosServerDiagnose {
	start := time.Now()
	item := nacosServerDiagnose{
		ID:     server.ID,
		Name:   server.Name,
		Host:   server.Host,
		Status: "passed",
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := remote.Run(ctx, server, nacosDiagnoseScript())
	item.DurationMS = time.Since(start).Milliseconds()
	item.Stdout = cleanNacosDiagnoseText(result.Stdout)
	item.Stderr = cleanNacosDiagnoseText(result.Stderr)
	item.ServiceActive = firstMatch(item.Stdout, `(?m)^active=(.*)$`)
	item.ServiceEnabled = firstMatch(item.Stdout, `(?m)^enabled=(.*)$`)
	item.Mode = firstMatch(item.Stdout, `(?m)^mode=(.*)$`)
	item.DBPlatform = firstMatch(item.Stdout, `(?m)^dbPlatform=(.*)$`)
	item.DBConfigured = strings.Contains(item.Stdout, "dbConfigured=true")
	item.DBHost = firstMatch(item.Stdout, `(?m)^dbHost=(.*)$`)
	item.DBPort = firstMatch(item.Stdout, `(?m)^dbPort=(.*)$`)
	item.DBTCP = firstMatch(item.Stdout, `(?m)^dbTcp=(.*)$`)
	item.DBQuery = firstMatch(item.Stdout, `(?m)^dbQuery=(.*)$`)
	item.ClusterNodes = sectionLines(item.Stdout, "cluster.conf")
	item.ListeningPorts = matchingLines(item.Stdout, `(?m)^LISTEN\b.*:(8848|9848|9849|7848)\b`)
	item.Summary = summarizeNacosDiag(item.Stdout)
	if err != nil {
		item.Status = "failed"
		item.Error = cleanErrText(err.Error())
	}
	if strings.Contains(item.Stdout, "No DataSource set") || strings.Contains(item.Stdout, "Startup errors") || strings.Contains(item.Stdout, "Application run failed") {
		item.Status = "failed"
		if item.Error == "" {
			item.Error = "Nacos startup errors found in logs"
		}
	}
	return item
}

func nacosDiagnoseScript() string {
	return `sh -s <<'AIFAR_NACOS_DIAG'
set +e
SERVICE_NAME="aifar-nacos.service"
INSTALL_ROOT="/aifar/apps/nacos"
NACOS_HOME="$INSTALL_ROOT/nacos"
SUDO=""
if [ "$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  SUDO="sudo -n"
fi
echo "## identity"
echo "hostname=$(hostname 2>/dev/null)"
echo "date=$(date -Is 2>/dev/null)"
echo "user=$(id -un 2>/dev/null)"
uname -a 2>/dev/null
echo "## service"
echo "active=$($SUDO systemctl is-active "$SERVICE_NAME" 2>/dev/null)"
echo "enabled=$($SUDO systemctl is-enabled "$SERVICE_NAME" 2>/dev/null)"
$SUDO systemctl --no-pager --full status "$SERVICE_NAME" 2>&1 | sed -n '1,80p'
echo "## unit"
if [ -f "/etc/systemd/system/$SERVICE_NAME" ]; then
  $SUDO sed -n '1,220p' "/etc/systemd/system/$SERVICE_NAME" 2>/dev/null
else
  echo "missing /etc/systemd/system/$SERVICE_NAME"
fi
echo "## application.properties"
if [ -f "$NACOS_HOME/conf/application.properties" ]; then
  $SUDO grep -nE '^(server\.port|spring\.sql\.init\.platform|spring\.datasource\.platform|db\.num|db\.url\.0|db\.user\.0|db\.password\.0|nacos\.core\.auth)' "$NACOS_HOME/conf/application.properties" 2>/dev/null \
    | sed -E 's/(db\.password\.0=).*/\1<redacted>/; s/(token\.secret\.key=).*/\1<redacted>/; s/(identity\.value=).*/\1<redacted>/'
  MODE_LINE="$($SUDO grep -n '^ExecStart=' "/etc/systemd/system/$SERVICE_NAME" 2>/dev/null | tail -n 1)"
  case "$MODE_LINE" in
    *"-m cluster"*) echo "mode=cluster" ;;
    *"-m standalone"*) echo "mode=standalone" ;;
    *) echo "mode=unknown" ;;
  esac
  if $SUDO grep -Eq '^spring\.sql\.init\.platform=mysql|^db\.url\.0=' "$NACOS_HOME/conf/application.properties" 2>/dev/null; then
    echo "dbConfigured=true"
  else
    echo "dbConfigured=false"
  fi
  DB_PLATFORM="$($SUDO sed -n 's/^spring\.sql\.init\.platform=//p' "$NACOS_HOME/conf/application.properties" 2>/dev/null | tail -n 1)"
  echo "dbPlatform=$DB_PLATFORM"
  DB_URL="$($SUDO sed -n 's/^db\.url\.0=//p' "$NACOS_HOME/conf/application.properties" 2>/dev/null | tail -n 1)"
  DB_HOST="$(printf "%s" "$DB_URL" | sed -E 's#^jdbc:mysql://([^:/?]+):?([0-9]*)/([^?]+).*$#\1#')"
  DB_PORT="$(printf "%s" "$DB_URL" | sed -E 's#^jdbc:mysql://([^:/?]+):?([0-9]*)/([^?]+).*$#\2#')"
  DB_NAME="$(printf "%s" "$DB_URL" | sed -E 's#^jdbc:mysql://([^:/?]+):?([0-9]*)/([^?]+).*$#\3#')"
  DB_USER="$($SUDO sed -n 's/^db\.user\.0=//p' "$NACOS_HOME/conf/application.properties" 2>/dev/null | tail -n 1)"
  DB_PASSWORD="$($SUDO sed -n 's/^db\.password\.0=//p' "$NACOS_HOME/conf/application.properties" 2>/dev/null | tail -n 1)"
  [ -n "$DB_PORT" ] || DB_PORT=3306
  echo "dbHost=$DB_HOST"
  echo "dbPort=$DB_PORT"
  echo "dbName=$DB_NAME"
  echo "dbUser=$DB_USER"
  if [ -n "$DB_HOST" ] && [ -n "$DB_PORT" ] && command -v bash >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
    if timeout 3 bash -c ":</dev/tcp/$DB_HOST/$DB_PORT" >/dev/null 2>&1; then
      echo "dbTcp=ok"
    else
      echo "dbTcp=failed"
    fi
  else
    echo "dbTcp=skipped"
  fi
  MYSQL_BIN=""
  if command -v mysql >/dev/null 2>&1; then
    MYSQL_BIN="$(command -v mysql)"
  else
    for CANDIDATE in /aifar/apps/mysql/mysql/bin/mysql /aifar/apps/mysql/bin/mysql /aifar/apps/mysql-*/mysql/bin/mysql /aifar/apps/mysql*/bin/mysql; do
      if [ -x "$CANDIDATE" ]; then
        MYSQL_BIN="$CANDIDATE"
        break
      fi
    done
  fi
  if [ -n "$DB_HOST" ] && [ -n "$DB_USER" ] && [ -n "$DB_NAME" ] && [ -n "$MYSQL_BIN" ] && command -v timeout >/dev/null 2>&1; then
    MYSQL_OUT="$(MYSQL_PWD="$DB_PASSWORD" timeout 8 "$MYSQL_BIN" -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -N -B -e "SELECT CONCAT('read_only=', @@read_only); SELECT CONCAT('super_read_only=', @@super_read_only); SELECT CONCAT('schema_tables=', COUNT(*)) FROM information_schema.tables WHERE table_schema='$DB_NAME';" 2>&1)"
    MYSQL_CODE=$?
    if [ "$MYSQL_CODE" = "0" ]; then
      echo "dbQuery=ok $(printf "%s" "$MYSQL_OUT" | tr '\n' ' ' | sed -E 's/[[:space:]]+/ /g')"
    else
      echo "dbQuery=failed $(printf "%s" "$MYSQL_OUT" | tail -n 1 | sed -E 's/password=[^ ]+/password=<redacted>/g')"
    fi
  else
    echo "dbQuery=skipped"
  fi
else
  echo "missing $NACOS_HOME/conf/application.properties"
  echo "mode=unknown"
  echo "dbConfigured=false"
fi
echo "## cluster.conf"
if [ -f "$NACOS_HOME/conf/cluster.conf" ]; then
  $SUDO sed -n '1,80p' "$NACOS_HOME/conf/cluster.conf" 2>/dev/null
else
  echo "missing cluster.conf"
fi
echo "## ports"
if command -v ss >/dev/null 2>&1; then
  ss -lntp 2>/dev/null | grep -E '(:|\.)((8848)|(9848)|(9849)|(7848))([[:space:]]|$)' || true
elif command -v netstat >/dev/null 2>&1; then
  netstat -lntp 2>/dev/null | grep -E '(:|\.)((8848)|(9848)|(9849)|(7848))([[:space:]]|$)' || true
else
  echo "ss/netstat not found"
fi
echo "## log-summary"
for LOG_FILE in "$NACOS_HOME/logs/start.out" "$NACOS_HOME/logs/nacos.log" "$NACOS_HOME/logs/config.log" "$NACOS_HOME/logs/naming-server.log"; do
  echo "--- $LOG_FILE ---"
  if [ -f "$LOG_FILE" ]; then
    $SUDO grep -nE 'No DataSource set|Startup errors|Application run failed|Server check fail|Connection refused|server IP list|Nacos is starting|Tomcat initialized|dumpservice|DataSource|Hikari|SQLException|Communications link|Access denied|Unknown database|Public Key Retrieval|Table .* does not exist|Table .* doesn.t exist|started with cluster|started with standalone|Running in cluster mode|Running in stand alone mode' "$LOG_FILE" 2>/dev/null | tail -n 140
  else
    echo "missing"
  fi
done
echo "## journal"
$SUDO journalctl -u "$SERVICE_NAME" -n 100 --no-pager 2>/dev/null \
  | grep -E 'Starting|Started|Failed|status=|startup.sh|shutdown.sh|cluster|standalone|nacos is starting' \
  | tail -n 100
exit 0
AIFAR_NACOS_DIAG`
}

func nacosFindingsForServer(item nacosServerDiagnose) []nacosDiagnoseFinding {
	var findings []nacosDiagnoseFinding
	add := func(level, message string) {
		findings = append(findings, nacosDiagnoseFinding{ServerID: item.ID, Level: level, Message: message})
	}
	if item.Status != "passed" {
		add("error", item.Error)
	}
	if item.Mode == "cluster" && !item.DBConfigured {
		add("error", "Nacos is configured as cluster mode but application.properties has no MySQL datasource")
	}
	if item.DBTCP == "failed" {
		add("error", fmt.Sprintf("cannot open TCP connection to MySQL %s:%s", item.DBHost, item.DBPort))
	}
	if strings.HasPrefix(item.DBQuery, "failed") {
		add("error", "MySQL query check failed: "+item.DBQuery)
	}
	if item.Mode == "cluster" && len(item.ClusterNodes) < 3 {
		add("error", fmt.Sprintf("Nacos cluster.conf has %d node(s), expected 3", len(item.ClusterNodes)))
	}
	if item.ServiceActive != "active" {
		add("error", "aifar-nacos.service is not active")
	}
	if item.Mode == "cluster" {
		required := []string{"8848", "9848", "9849"}
		for _, port := range required {
			if !linesContain(item.ListeningPorts, ":"+port) {
				add("warning", "port "+port+" is not listening on this node")
			}
		}
	}
	if strings.Contains(item.Stdout, "No DataSource set") {
		add("error", "Nacos log contains No DataSource set")
	}
	if strings.Contains(item.Stdout, "Connection refused") && strings.Contains(item.Stdout, "9849") {
		add("warning", "Nacos log shows gRPC raft port 9849 connection refused to peer node")
	}
	return findings
}

func noErrorFindings(findings []nacosDiagnoseFinding) bool {
	for _, finding := range findings {
		if finding.Level == "error" {
			return false
		}
	}
	return true
}

func finishNacosDiagnose(outputRoot string, rep *nacosDiagnoseReport) int {
	rep.FinishedAt = time.Now()
	if err := writeNacosDiagnoseReport(outputRoot, rep); err != nil {
		fmt.Fprintf(os.Stderr, "write Nacos diagnose report failed: %v\n", err)
		rep.Passed = false
	}
	printNacosDiagnose(*rep)
	if rep.Passed {
		return 0
	}
	return 1
}

func writeNacosDiagnoseReport(outputRoot string, rep *nacosDiagnoseReport) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("output directory is required")
	}
	runDir := filepath.Join(outputRoot, rep.StartedAt.Format("20060102-150405"))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(runDir, rep.Command+".json")
	rep.OutputPath = path
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func printNacosDiagnose(rep nacosDiagnoseReport) {
	fmt.Printf("AIFAR smoke %s started=%s finished=%s\n", rep.Command, rep.StartedAt.Format(time.RFC3339), rep.FinishedAt.Format(time.RFC3339))
	fmt.Printf("database=%s\n", rep.DatabasePath)
	if rep.OutputPath != "" {
		fmt.Printf("json_report=%s\n", rep.OutputPath)
	}
	for i, server := range rep.Servers {
		fmt.Printf("[%d/%d] %s host=%s status=%s active=%s enabled=%s mode=%s dbConfigured=%v ports=%s duration_ms=%d\n",
			i+1, len(rep.Servers), server.Name, server.Host, server.Status, server.ServiceActive, server.ServiceEnabled, server.Mode, server.DBConfigured, strings.Join(server.ListeningPorts, " | "), server.DurationMS)
		for _, line := range server.Summary {
			fmt.Printf("  - %s\n", line)
		}
		if server.Error != "" {
			fmt.Printf("  error=%s\n", server.Error)
		}
	}
	if len(rep.Findings) > 0 {
		fmt.Println("findings:")
		for _, finding := range rep.Findings {
			fmt.Printf("  - [%s] %s %s\n", finding.Level, finding.ServerID, finding.Message)
		}
	}
	if len(rep.Failures) > 0 {
		fmt.Println("failures:")
		for _, failure := range rep.Failures {
			fmt.Printf("  - %s\n", failure)
		}
	}
	if rep.Passed {
		fmt.Println("result=PASS")
	} else {
		fmt.Println("result=FAIL")
	}
}

func cleanNacosDiagnoseText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	redactors := []*regexp.Regexp{
		regexp.MustCompile(`(?im)(db\.password\.0=).*$`),
		regexp.MustCompile(`(?im)(nacos\.core\.auth\.plugin\.nacos\.token\.secret\.key=).*$`),
		regexp.MustCompile(`(?im)(nacos\.core\.auth\.server\.identity\.value=).*$`),
	}
	for _, re := range redactors {
		value = re.ReplaceAllString(value, `${1}<redacted>`)
	}
	return strings.TrimSpace(value)
}

func summarizeNacosDiag(stdout string) []string {
	var out []string
	for _, pattern := range []string{
		`No DataSource set`,
		`Startup errors`,
		`Application run failed`,
		`Server check fail.*9849`,
		`Connection refused.*9849`,
		`server IP list.*`,
	} {
		re := regexp.MustCompile(`(?im).*` + pattern + `.*`)
		if match := re.FindString(stdout); strings.TrimSpace(match) != "" {
			out = append(out, strings.TrimSpace(match))
		}
	}
	return out
}

func firstMatch(value, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func matchingLines(value, pattern string) []string {
	re := regexp.MustCompile(pattern)
	var out []string
	for _, line := range strings.Split(value, "\n") {
		if re.MatchString(line) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func sectionLines(value, title string) []string {
	lines := strings.Split(value, "\n")
	var out []string
	inSection := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == title
			continue
		}
		if !inSection {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "missing ") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func linesContain(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
