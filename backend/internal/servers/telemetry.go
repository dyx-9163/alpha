package servers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Telemetry struct {
	ServerID   string    `json:"serverId"`
	CPU        float64   `json:"cpu"`
	CPUText    string    `json:"cpuText"`
	Memory     float64   `json:"memory"`
	MemoryText string    `json:"memoryText"`
	Disk       float64   `json:"disk"`
	DiskText   string    `json:"diskText"`
	DiskPath   string    `json:"diskPath"`
	Load       []float64 `json:"load"`
	SampledAt  time.Time `json:"sampledAt"`
}

func (s Service) Telemetry(ctx context.Context, id string) (Telemetry, error) {
	server, err := s.store.GetServer(id, true)
	if err != nil {
		return Telemetry{}, err
	}
	deployDir := strings.TrimSpace(server.DeployDir)
	if deployDir == "" {
		deployDir = s.defaultDeployDir
	}
	result, err := s.remote.Run(ctx, server, telemetryCommand(deployDir))
	if err != nil {
		return Telemetry{}, err
	}
	return parseTelemetryOutput(id, result.Stdout)
}

func telemetryCommand(deployDir string) string {
	if strings.TrimSpace(deployDir) == "" {
		deployDir = "/"
	}
	return strings.Join([]string{
		"DEPLOY_DIR=" + shellQuote(deployDir),
		`if [ ! -e "$DEPLOY_DIR" ]; then DEPLOY_DIR="/"; fi`,
		`read_cpu() { awk '/^cpu / {print $2+$3+$4+$5+$6+$7+$8, $5+$6; exit}' /proc/stat; }`,
		`set -- $(read_cpu); CPU_TOTAL_1="$1"; CPU_IDLE_1="$2"`,
		`sleep 0.2`,
		`set -- $(read_cpu); CPU_TOTAL_2="$1"; CPU_IDLE_2="$2"`,
		`CPU_PCT="$(awk -v t1="$CPU_TOTAL_1" -v i1="$CPU_IDLE_1" -v t2="$CPU_TOTAL_2" -v i2="$CPU_IDLE_2" 'BEGIN { dt=t2-t1; di=i2-i1; if (dt <= 0) printf "0.0"; else printf "%.1f", (dt-di)*100/dt }')"`,
		`MEM_LINE="$(awk '/^MemTotal:/ {total=$2} /^MemAvailable:/ {avail=$2} END { if (total <= 0) { printf "0.0|0|0" } else { used=total-avail; printf "%.1f|%d|%d", used*100/total, used*1024, total*1024 } }' /proc/meminfo)"`,
		`DISK_LINE="$(df -Pk "$DEPLOY_DIR" | awk 'NR==2 { pct=$5; gsub(/%/,"",pct); printf "%s|%d|%d|%s", pct, $3*1024, $2*1024, $6 }')"`,
		`LOAD_LINE="$(awk '{print $1"|"$2"|"$3}' /proc/loadavg)"`,
		`echo "cpu=$CPU_PCT"`,
		`echo "memory=$MEM_LINE"`,
		`echo "disk=$DISK_LINE"`,
		`echo "load=$LOAD_LINE"`,
	}, "\n")
}

func parseTelemetryOutput(serverID, output string) (Telemetry, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	cpu := parsePercent(values["cpu"])
	memoryPct, memoryText := parseUsage(values["memory"])
	diskPct, diskText, diskPath := parseDisk(values["disk"])
	return Telemetry{
		ServerID:   serverID,
		CPU:        cpu,
		CPUText:    formatPercent(cpu),
		Memory:     memoryPct,
		MemoryText: memoryText,
		Disk:       diskPct,
		DiskText:   diskText,
		DiskPath:   diskPath,
		Load:       parseLoad(values["load"]),
		SampledAt:  time.Now(),
	}, nil
}

func parseUsage(value string) (float64, string) {
	parts := strings.Split(value, "|")
	if len(parts) < 3 {
		return 0, "-"
	}
	percent := parsePercent(parts[0])
	used, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	total, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	return percent, fmt.Sprintf("%s / %s", formatBytes(used), formatBytes(total))
}

func parseDisk(value string) (float64, string, string) {
	parts := strings.Split(value, "|")
	if len(parts) < 4 {
		return 0, "-", "-"
	}
	percent := parsePercent(parts[0])
	used, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	total, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	path := strings.TrimSpace(parts[3])
	if path == "" {
		path = "-"
	}
	return percent, fmt.Sprintf("%s / %s", formatBytes(used), formatBytes(total)), path
}

func parseLoad(value string) []float64 {
	parts := strings.Split(value, "|")
	out := make([]float64, 0, 3)
	for _, part := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err == nil {
			out = append(out, n)
		}
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out
}

func parsePercent(value string) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
	if err != nil || n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value)
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
