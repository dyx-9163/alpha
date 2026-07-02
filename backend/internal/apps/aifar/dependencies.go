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
	options.RedisMode = normalizeRedisMode(options.RedisMode)
	if options.NacosSource != dependencyExisting {
		return options, nil
	}
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return options, err
	}
	if err := s.resolveNacosDependency(&options, instances); err != nil {
		return options, err
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
	options.NacosWebPort = endpoint.Port
	metadata := metadataFromInstance(selected)
	if grpcPort := intFromMetadata(metadata, "grpcPort", 0); validPort(grpcPort) {
		options.NacosAPIPort = grpcPort
	} else if validPort(endpoint.Port + 1000) {
		options.NacosAPIPort = endpoint.Port + 1000
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
