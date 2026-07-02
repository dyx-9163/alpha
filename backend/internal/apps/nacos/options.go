package nacos

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type InstallOptions struct {
	Topology      string
	Port          int
	GRPCPort      int
	GRPCRaftPort  int
	RaftPort      int
	JVMXMS        string
	JVMXMX        string
	JVMXMN        string
	AdminUser     string
	AdminPassword string
	Database      DatabaseOptions
}

type DatabaseOptions struct {
	Enabled    bool
	Source     string
	InstanceID string
	Host       string
	Port       int
	Name       string
	User       string
	Password   string
	Init       bool
}

const (
	databaseSourceLocal    = "local"
	databaseSourceManual   = "manual"
	databaseSourceExisting = "existing"
)

func nacosOptions(params map[string]any, topology string) InstallOptions {
	port := intParam(params, "port", 8848)
	databaseSource := normalizeDatabaseSource(stringParam(params, "dbSource", databaseSourceLocal))
	return InstallOptions{
		Topology:      normalizeTopology(topology),
		Port:          port,
		GRPCPort:      intParam(params, "grpcPort", port+1000),
		GRPCRaftPort:  intParam(params, "grpcRaftPort", port+1001),
		RaftPort:      intParam(params, "raftPort", 7848),
		JVMXMS:        stringParam(params, "jvmXms", "512m"),
		JVMXMX:        stringParam(params, "jvmXmx", "512m"),
		JVMXMN:        stringParam(params, "jvmXmn", "256m"),
		AdminUser:     stringParam(params, "nacosUser", "nacos"),
		AdminPassword: stringParam(params, "nacosPassword", "nacos"),
		Database: DatabaseOptions{
			Enabled:    databaseSource != databaseSourceLocal,
			Source:     databaseSource,
			InstanceID: stringParam(params, "dbInstanceId", ""),
			Host:       stringParam(params, "dbHost", ""),
			Port:       intParam(params, "dbPort", 3306),
			Name:       stringParam(params, "dbName", "aifar_nacos"),
			User:       stringParam(params, "dbUser", "root"),
			Password:   stringParam(params, "dbPassword", ""),
			Init:       databaseSource != databaseSourceLocal && boolParam(params["initDatabase"]),
		},
	}
}

func (o InstallOptions) Validate() error {
	if o.Port <= 0 || o.Port > 65535 {
		return fmt.Errorf("invalid Nacos port: %d", o.Port)
	}
	for label, port := range map[string]int{"Nacos gRPC port": o.GRPCPort, "Nacos gRPC raft port": o.GRPCRaftPort, "Nacos raft port": o.RaftPort} {
		if port <= 0 || port > 65535 {
			return fmt.Errorf("invalid %s: %d", label, port)
		}
	}
	for label, value := range map[string]string{"JVM Xms": o.JVMXMS, "JVM Xmx": o.JVMXMX, "JVM Xmn": o.JVMXMN} {
		if strings.TrimSpace(value) == "" || strings.IndexFunc(value, func(r rune) bool { return r <= ' ' }) >= 0 {
			return fmt.Errorf("%s is required and must not contain whitespace", label)
		}
	}
	if strings.TrimSpace(o.AdminUser) == "" || strings.IndexFunc(o.AdminUser, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("Nacos admin user is required and must not contain whitespace")
	}
	if strings.TrimSpace(o.AdminPassword) == "" || strings.IndexFunc(o.AdminPassword, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("Nacos admin password is required and must not contain whitespace")
	}
	if o.Database.Enabled {
		if o.Database.Source == databaseSourceExisting && strings.TrimSpace(o.Database.InstanceID) == "" {
			return errors.New("Nacos requires a selected MySQL database instance")
		}
		if strings.TrimSpace(o.Database.Host) == "" {
			return errors.New("Nacos requires MySQL database host")
		}
		if o.Database.Port <= 0 || o.Database.Port > 65535 {
			return fmt.Errorf("invalid Nacos database port: %d", o.Database.Port)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`).MatchString(o.Database.Name) {
			return errors.New("Nacos database name can contain only letters, numbers, and underscore")
		}
		if strings.TrimSpace(o.Database.User) == "" || strings.IndexFunc(o.Database.User, func(r rune) bool { return r <= ' ' }) >= 0 {
			return errors.New("Nacos database user is required and must not contain whitespace")
		}
		if strings.TrimSpace(o.Database.Password) == "" || strings.IndexFunc(o.Database.Password, func(r rune) bool { return r <= ' ' }) >= 0 {
			return errors.New("Nacos database password is required and must not contain whitespace")
		}
	}
	return nil
}

func normalizeDatabaseSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case databaseSourceExisting:
		return databaseSourceExisting
	case databaseSourceManual:
		return databaseSourceManual
	case "embedded", "none", databaseSourceLocal:
		return databaseSourceLocal
	default:
		return databaseSourceLocal
	}
}

func normalizeTopology(topology string) string {
	topology = strings.ToLower(strings.TrimSpace(topology))
	switch topology {
	case "", "single":
		return "standalone"
	case "cluster-mode", "cluster_mode", "nacos-cluster":
		return "cluster"
	default:
		return topology
	}
}

func targetServerIDs(req InstallRequest) []string {
	return uniqueStrings(append([]string{req.ServerID}, req.ServerIDs...))
}

func clusterServerIDs(params map[string]any, fallback []string) []string {
	for _, key := range []string{"nacosServerIds", "clusterServerIds", "serverIds"} {
		if values := stringSliceParam(params, key); len(values) > 0 {
			return values
		}
	}
	return uniqueStrings(fallback)
}

func stringParam(params map[string]any, key, fallback string) string {
	if params == nil {
		return fallback
	}
	if value, ok := params[key]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return fallback
}

func intParam(params map[string]any, key string, fallback int) int {
	if params == nil {
		return fallback
	}
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return normalizePort(v, fallback)
	case int64:
		return normalizePort(int(v), fallback)
	case float64:
		return normalizePort(int(v), fallback)
	case json.Number:
		n, _ := v.Int64()
		return normalizePort(int(n), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePort(n, fallback)
	default:
		return fallback
	}
}

func boolParam(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case json.Number:
		n, _ := v.Int64()
		return n != 0
	}
	return false
}

func stringSliceParam(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	value, ok := params[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return uniqueStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return uniqueStrings(out)
	case string:
		return uniqueStrings(strings.Split(v, ","))
	default:
		return nil
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizePort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
