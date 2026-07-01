package nacos

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/store"
)

type databaseEndpoint struct {
	Host string
	Port int
}

func (s Service) resolveInstallOptions(options InstallOptions) (InstallOptions, error) {
	options.Database.Source = normalizeDatabaseSource(options.Database.Source)
	if !options.Database.Enabled || options.Database.Source != databaseSourceExisting {
		return options, nil
	}
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return options, err
	}
	selected, ok := findDatabaseInstance(instances, options.Database.InstanceID)
	if !ok {
		return options, fmt.Errorf("selected database instance was not found")
	}
	if selected.App != "mysql" && selected.App != "mysql-router" {
		return options, fmt.Errorf("selected database instance is not a MySQL instance")
	}
	target := selected
	if selected.App == "mysql" {
		metadata := metadataFromInstance(selected)
		clusterID := stringFromMetadata(metadata, "clusterId", "")
		if clusterID != "" {
			if router, found := findMySQLRouterForCluster(instances, clusterID); found {
				target = router
			}
		}
	}
	endpoint, ok := s.instanceEndpoint(target, mysqlDefaultPort(target), []string{"endpoint", "clusterEndpoint"}, mysqlPortKeys(target))
	if !ok {
		return options, fmt.Errorf("selected database instance has no usable endpoint")
	}
	options.Database.Host = endpoint.Host
	options.Database.Port = endpoint.Port
	return options, nil
}

func findDatabaseInstance(instances []store.AppInstance, id string) (store.AppInstance, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.AppInstance{}, false
	}
	for _, instance := range instances {
		if instance.ID == id {
			return instance, true
		}
	}
	return store.AppInstance{}, false
}

func findMySQLRouterForCluster(instances []store.AppInstance, clusterID string) (store.AppInstance, bool) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return store.AppInstance{}, false
	}
	for _, instance := range instances {
		if instance.App != "mysql-router" {
			continue
		}
		metadata := metadataFromInstance(instance)
		if stringFromMetadata(metadata, "clusterId", "") == clusterID {
			return instance, true
		}
	}
	return store.AppInstance{}, false
}

func mysqlDefaultPort(instance store.AppInstance) int {
	if instance.App == "mysql-router" {
		return 6446
	}
	return 3306
}

func mysqlPortKeys(instance store.AppInstance) []string {
	if instance.App == "mysql-router" {
		return []string{"basePort", "readWritePort", "port"}
	}
	return []string{"port"}
}

func (s Service) instanceEndpoint(instance store.AppInstance, defaultPort int, endpointKeys, portKeys []string) (databaseEndpoint, bool) {
	metadata := metadataFromInstance(instance)
	for _, key := range endpointKeys {
		if endpoint, ok := splitEndpoint(stringFromMetadata(metadata, key, ""), defaultPort); ok {
			return endpoint, true
		}
	}
	port := defaultPort
	for _, key := range portKeys {
		if value := intFromMetadata(metadata, key, 0); value > 0 {
			port = value
			break
		}
	}
	server, err := s.store.GetServer(instance.ServerID, false)
	if err == nil && strings.TrimSpace(server.Host) != "" && validDatabasePort(port) {
		return databaseEndpoint{Host: strings.TrimSpace(server.Host), Port: port}, true
	}
	return databaseEndpoint{}, false
}

func splitEndpoint(value string, defaultPort int) (databaseEndpoint, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return databaseEndpoint{}, false
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host != "" {
			value = parsed.Host
		}
	}
	if slash := strings.Index(value, "/"); slash >= 0 {
		value = value[:slash]
	}
	host, portText, err := net.SplitHostPort(value)
	if err == nil {
		port, parseErr := strconv.Atoi(portText)
		if parseErr == nil && strings.TrimSpace(host) != "" && validDatabasePort(port) {
			return databaseEndpoint{Host: strings.TrimSpace(host), Port: port}, true
		}
	}
	if strings.Count(value, ":") == 1 {
		parts := strings.SplitN(value, ":", 2)
		port, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if parseErr == nil && strings.TrimSpace(parts[0]) != "" && validDatabasePort(port) {
			return databaseEndpoint{Host: strings.TrimSpace(parts[0]), Port: port}, true
		}
	}
	if strings.TrimSpace(value) != "" && validDatabasePort(defaultPort) {
		return databaseEndpoint{Host: strings.TrimSpace(value), Port: defaultPort}, true
	}
	return databaseEndpoint{}, false
}

func metadataFromInstance(instance store.AppInstance) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return metadata
}

func stringFromMetadata(metadata map[string]any, key, fallback string) string {
	if value, ok := metadata[key]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return fallback
}

func intFromMetadata(metadata map[string]any, key string, fallback int) int {
	value, ok := metadata[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

func validDatabasePort(port int) bool {
	return port >= 1 && port <= 65535
}
