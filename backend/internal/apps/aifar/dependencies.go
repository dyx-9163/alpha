package aifar

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/store"
)

func (s Service) resolveInstallOptions(options InstallOptions) (InstallOptions, error) {
	options.NacosSource = normalizeDependencySource(options.NacosSource)
	options.DBSource = normalizeDependencySource(options.DBSource)
	options.RedisSource = normalizeDependencySource(options.RedisSource)
	options.MinioSource = normalizeDependencySource(options.MinioSource)
	options.RedisMode = normalizeRedisMode(options.RedisMode)
	minioNeedsResolve := options.MinioEnableStorage && options.MinioSource == dependencyExisting
	if options.NacosSource != dependencyExisting && options.DBSource != dependencyExisting && options.RedisSource != dependencyExisting && !minioNeedsResolve {
		return options, nil
	}
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return options, err
	}
	if options.NacosSource == dependencyExisting {
		if err := s.resolveNacosDependency(&options, instances); err != nil {
			return options, err
		}
	}
	if options.DBSource == dependencyExisting {
		if err := s.resolveMySQLDependency(&options, instances); err != nil {
			return options, err
		}
	}
	if options.RedisSource == dependencyExisting {
		if err := s.resolveRedisDependency(&options, instances); err != nil {
			return options, err
		}
	}
	if minioNeedsResolve {
		if err := s.resolveMinioDependency(&options, instances); err != nil {
			return options, err
		}
	}
	return options, nil
}

func (s Service) resolveNacosDependency(options *InstallOptions, instances []store.AppInstance) error {
	selected, ok := findDependencyInstance(instances, options.NacosInstanceID)
	if !ok {
		return fmt.Errorf("selected nacos instance was not found")
	}
	if selected.App != "nacos" {
		return fmt.Errorf("selected nacos instance is not a Nacos instance")
	}
	endpoint, ok := s.instanceEndpoint(selected, defaultNacosWebPort, []string{"endpoint", "clusterEndpoint"}, []string{"port"})
	if !ok {
		return fmt.Errorf("selected nacos instance has no usable endpoint")
	}
	options.NacosHost = endpoint.Host
	return nil
}

func (s Service) resolveMySQLDependency(options *InstallOptions, instances []store.AppInstance) error {
	selected, ok := findDependencyInstance(instances, options.DBInstanceID)
	if !ok {
		return fmt.Errorf("selected database instance was not found")
	}
	if selected.App != "mysql" && selected.App != "mysql-router" {
		return fmt.Errorf("selected database instance is not a MySQL instance")
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
		return fmt.Errorf("selected database instance has no usable endpoint")
	}
	options.DBHost = endpoint.Host
	options.DBPort = endpoint.Port
	return nil
}

func (s Service) resolveRedisDependency(options *InstallOptions, instances []store.AppInstance) error {
	selected, ok := findDependencyInstance(instances, options.RedisInstanceID)
	if !ok {
		return fmt.Errorf("selected redis instance was not found")
	}
	if selected.App != "redis" {
		return fmt.Errorf("selected redis instance is not a Redis instance")
	}
	switch redisTopology(selected) {
	case redisModeSentinel:
		metadata := metadataFromInstance(selected)
		options.RedisMode = redisModeSentinel
		options.RedisSentinelMasterName = stringFromMetadata(metadata, "masterName", "aifar-master")
		options.RedisSentinelNodes = s.redisSentinelEndpoints(selected, instances)
		if len(options.RedisSentinelNodes) == 0 {
			return fmt.Errorf("selected redis sentinel instance has no usable sentinel endpoint")
		}
		first, ok := splitEndpoint(options.RedisSentinelNodes[0], defaultRedisPort)
		if !ok {
			return fmt.Errorf("selected redis sentinel instance has an invalid endpoint")
		}
		options.RedisHost = first.Host
		options.RedisPort = first.Port
	case redisModeCluster:
		options.RedisMode = redisModeCluster
		options.RedisClusterNodes = s.redisDataEndpoints(selected, instances, redisModeCluster)
		if len(options.RedisClusterNodes) == 0 {
			return fmt.Errorf("selected redis cluster instance has no usable endpoint")
		}
		first, ok := splitEndpoint(options.RedisClusterNodes[0], defaultRedisPort)
		if !ok {
			return fmt.Errorf("selected redis cluster instance has an invalid endpoint")
		}
		options.RedisHost = first.Host
		options.RedisPort = first.Port
	default:
		endpoint, ok := s.instanceEndpoint(selected, defaultRedisPort, []string{"endpoint"}, []string{"port"})
		if !ok {
			return fmt.Errorf("selected redis instance has no usable endpoint")
		}
		options.RedisMode = redisModeStandalone
		options.RedisHost = endpoint.Host
		options.RedisPort = endpoint.Port
	}
	return nil
}

func (s Service) resolveMinioDependency(options *InstallOptions, instances []store.AppInstance) error {
	selected, ok := findDependencyInstance(instances, options.MinioInstanceID)
	if !ok {
		return fmt.Errorf("selected minio instance was not found")
	}
	if selected.App != "minio" {
		return fmt.Errorf("selected minio instance is not a MinIO instance")
	}
	metadata := metadataFromInstance(selected)
	if endpoint, ok := endpointURLFromMetadata(metadata, defaultMinioAPIPort); ok {
		options.MinioEndpoint = endpoint
	} else if endpoint, ok := s.instanceEndpoint(selected, defaultMinioAPIPort, []string{"endpoint", "peerEndpoint"}, []string{"apiPort", "port"}); ok {
		options.MinioEndpoint = fmt.Sprintf("http://%s:%d", endpoint.Host, endpoint.Port)
	} else {
		return fmt.Errorf("selected minio instance has no usable endpoint")
	}
	if strings.TrimSpace(options.MinioDomain) == "" {
		options.MinioDomain = deriveMinioDomain(options.MinioEndpoint, options.MinioBucketName)
	}
	return nil
}

type dependencyEndpoint struct {
	Host string
	Port int
}

func findDependencyInstance(instances []store.AppInstance, id string) (store.AppInstance, bool) {
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
	return defaultDBPort
}

func mysqlPortKeys(instance store.AppInstance) []string {
	if instance.App == "mysql-router" {
		return []string{"basePort", "readWritePort", "port"}
	}
	return []string{"port"}
}

func (s Service) instanceEndpoint(instance store.AppInstance, defaultPort int, endpointKeys, portKeys []string) (dependencyEndpoint, bool) {
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
	if err == nil && strings.TrimSpace(server.Host) != "" && validPort(port) {
		return dependencyEndpoint{Host: strings.TrimSpace(server.Host), Port: port}, true
	}
	return dependencyEndpoint{}, false
}

func (s Service) redisSentinelEndpoints(selected store.AppInstance, instances []store.AppInstance) []string {
	metadata := metadataFromInstance(selected)
	endpoints := endpointListFromMetadata(metadata, "sentinelEndpoints")
	if len(endpoints) > 0 {
		return endpoints
	}
	for _, candidate := range instances {
		if !sameRedisDependencyGroup(selected, candidate, redisModeSentinel) || !redisRunsSentinel(candidate) {
			continue
		}
		if endpoint, ok := s.instanceEndpoint(candidate, 26379, []string{"sentinelEndpoint"}, []string{"sentinelPort"}); ok {
			endpoints = appendUniqueEndpoint(endpoints, fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port))
		}
	}
	return endpoints
}

func (s Service) redisDataEndpoints(selected store.AppInstance, instances []store.AppInstance, topology string) []string {
	metadata := metadataFromInstance(selected)
	endpoints := endpointListFromMetadata(metadata, "clusterEndpoints")
	if len(endpoints) > 0 {
		return endpoints
	}
	for _, candidate := range instances {
		if !sameRedisDependencyGroup(selected, candidate, topology) {
			continue
		}
		if endpoint, ok := s.instanceEndpoint(candidate, defaultRedisPort, []string{"endpoint"}, []string{"port"}); ok {
			endpoints = appendUniqueEndpoint(endpoints, fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port))
		}
	}
	return endpoints
}

func sameRedisDependencyGroup(base, candidate store.AppInstance, topology string) bool {
	if candidate.App != "redis" || redisTopology(candidate) != topology {
		return false
	}
	baseMetadata := metadataFromInstance(base)
	candidateMetadata := metadataFromInstance(candidate)
	if clusterID := stringFromMetadata(baseMetadata, "clusterId", ""); clusterID != "" {
		return clusterID == stringFromMetadata(candidateMetadata, "clusterId", "")
	}
	if masterName := stringFromMetadata(baseMetadata, "masterName", ""); masterName != "" {
		return strings.EqualFold(masterName, stringFromMetadata(candidateMetadata, "masterName", ""))
	}
	return base.ID != "" && base.ID == candidate.ID
}

func redisTopology(instance store.AppInstance) string {
	if topology := normalizeRedisMode(instance.Topology); topology != redisModeStandalone || strings.TrimSpace(instance.Topology) != "" {
		return topology
	}
	metadata := metadataFromInstance(instance)
	if topology := normalizeRedisMode(stringFromMetadata(metadata, "topology", "")); topology != redisModeStandalone {
		return topology
	}
	if stringFromMetadata(metadata, "masterName", "") != "" || boolFromMetadata(metadata, "sentinel") {
		return redisModeSentinel
	}
	return redisModeStandalone
}

func redisRunsSentinel(instance store.AppInstance) bool {
	metadata := metadataFromInstance(instance)
	return boolFromMetadata(metadata, "sentinel") ||
		stringFromMetadata(metadata, "sentinelName", "") != "" ||
		strings.EqualFold(stringFromMetadata(metadata, "role", ""), "sentinel")
}

func splitEndpoint(value string, defaultPort int) (dependencyEndpoint, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return dependencyEndpoint{}, false
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
		if parseErr == nil && strings.TrimSpace(host) != "" && validPort(port) {
			return dependencyEndpoint{Host: strings.TrimSpace(host), Port: port}, true
		}
	}
	if strings.Count(value, ":") == 1 {
		parts := strings.SplitN(value, ":", 2)
		port, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if parseErr == nil && strings.TrimSpace(parts[0]) != "" && validPort(port) {
			return dependencyEndpoint{Host: strings.TrimSpace(parts[0]), Port: port}, true
		}
	}
	if strings.TrimSpace(value) != "" && validPort(defaultPort) {
		return dependencyEndpoint{Host: strings.TrimSpace(value), Port: defaultPort}, true
	}
	return dependencyEndpoint{}, false
}

func endpointURLFromMetadata(metadata map[string]any, defaultPort int) (string, bool) {
	for _, key := range []string{"endpoint", "peerEndpoint"} {
		raw := stringFromMetadata(metadata, key, "")
		endpoint, ok := splitEndpoint(raw, defaultPort)
		if !ok {
			continue
		}
		scheme := "http"
		if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
			scheme = parsed.Scheme
		}
		return fmt.Sprintf("%s://%s:%d", scheme, endpoint.Host, endpoint.Port), true
	}
	return "", false
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

func boolFromMetadata(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		v = strings.TrimSpace(strings.ToLower(v))
		return v == "true" || v == "1" || v == "yes"
	default:
		return false
	}
}

func endpointListFromMetadata(metadata map[string]any, key string) []string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	var out []string
	switch v := value.(type) {
	case []string:
		for _, item := range v {
			out = appendUniqueEndpoint(out, item)
		}
	case []any:
		for _, item := range v {
			out = appendUniqueEndpoint(out, fmt.Sprint(item))
		}
	case string:
		for _, item := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
			out = appendUniqueEndpoint(out, item)
		}
	}
	return out
}

func appendUniqueEndpoint(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
