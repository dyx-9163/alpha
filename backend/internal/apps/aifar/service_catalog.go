package aifar

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/runtimeagent"
)

const serviceDefinitionName = "service.json"

var serviceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type serviceDefinition struct {
	Schema             string                    `json:"schema"`
	Name               string                    `json:"name"`
	DisplayName        map[string]string         `json:"displayName"`
	Kind               string                    `json:"kind"`
	ApplicationName    string                    `json:"applicationName"`
	Port               int                       `json:"port"`
	Required           bool                      `json:"required"`
	Role               string                    `json:"role"`
	ArtifactExtensions []string                  `json:"artifactExtensions"`
	HealthPath         string                    `json:"healthPath"`
	AffinityPolicy     string                    `json:"affinityPolicy"`
	Resources          runtimeagent.ResourceSpec `json:"resources,omitempty"`
}

func discoverBundleServices(bundle Bundle) ([]serviceDefinition, error) {
	entries, err := os.ReadDir(bundle.AppDir)
	if err != nil {
		return nil, fmt.Errorf("read AIFAR services directory: %w", err)
	}
	definitions := make([]serviceDefinition, 0, len(entries))
	seenNames := map[string]bool{}
	seenPorts := map[int]string{}
	roles := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entry.Name()))
		if name == "nacos" {
			continue
		}
		path := filepath.Join(bundle.AppDir, entry.Name(), serviceDefinitionName)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("AIFAR service %s requires %s: %w", name, serviceDefinitionName, readErr)
		}
		var definition serviceDefinition
		if err := json.Unmarshal(data, &definition); err != nil {
			return nil, fmt.Errorf("AIFAR service %s has invalid %s: %w", name, serviceDefinitionName, err)
		}
		definition.Name = strings.ToLower(strings.TrimSpace(definition.Name))
		definition.Kind = strings.ToLower(strings.TrimSpace(definition.Kind))
		definition.Role = strings.ToLower(strings.TrimSpace(definition.Role))
		definition.ApplicationName = strings.TrimSpace(definition.ApplicationName)
		definition.HealthPath = strings.TrimSpace(definition.HealthPath)
		definition.AffinityPolicy = strings.ToLower(strings.TrimSpace(definition.AffinityPolicy))
		if definition.Schema != "aifar-runtime-service-v1" {
			return nil, fmt.Errorf("AIFAR service %s schema must be aifar-runtime-service-v1", name)
		}
		if definition.Name != name || !serviceNamePattern.MatchString(definition.Name) {
			return nil, fmt.Errorf("AIFAR service directory %s must match a valid service name in %s", name, serviceDefinitionName)
		}
		if seenNames[name] {
			return nil, fmt.Errorf("duplicate AIFAR service: %s", name)
		}
		seenNames[name] = true
		if definition.Kind != "java" && definition.Kind != "web" {
			return nil, fmt.Errorf("AIFAR service %s kind must be java or web", name)
		}
		if definition.Kind == "java" && definition.ApplicationName == "" {
			return nil, fmt.Errorf("AIFAR Java service %s requires applicationName", name)
		}
		if !validPort(definition.Port) {
			return nil, fmt.Errorf("AIFAR service %s port must be between 1 and 65535", name)
		}
		if other := seenPorts[definition.Port]; other != "" {
			return nil, fmt.Errorf("AIFAR services %s and %s use the same port %d", other, name, definition.Port)
		}
		seenPorts[definition.Port] = name
		if definition.Role != "" {
			if definition.Role != "gateway" && definition.Role != "web" {
				return nil, fmt.Errorf("AIFAR service %s has unsupported role %s", name, definition.Role)
			}
			if (definition.Role == "gateway" && name != "gateway") || (definition.Role == "web" && name != "web-vue3") {
				return nil, fmt.Errorf("AIFAR role %s must use the reserved service name %s", definition.Role, map[string]string{"gateway": "gateway", "web": "web-vue3"}[definition.Role])
			}
			if other := roles[definition.Role]; other != "" {
				return nil, fmt.Errorf("AIFAR services %s and %s both declare role %s", other, name, definition.Role)
			}
			roles[definition.Role] = name
			definition.Required = true
		}
		if definition.AffinityPolicy == "" {
			definition.AffinityPolicy = "round-robin"
		}
		if definition.AffinityPolicy != "round-robin" && definition.AffinityPolicy != "stable" {
			return nil, fmt.Errorf("AIFAR service %s has unsupported affinityPolicy %s", name, definition.AffinityPolicy)
		}
		if len(definition.ArtifactExtensions) == 0 {
			if definition.Kind == "web" {
				definition.ArtifactExtensions = []string{".zip", ".tar", ".tgz", ".tar.gz"}
			} else {
				definition.ArtifactExtensions = []string{".jar"}
			}
		}
		if _, err := os.Stat(filepath.Join(bundle.AppDir, name, "Dockerfile")); err != nil {
			return nil, fmt.Errorf("AIFAR service %s Dockerfile is required: %w", name, err)
		}
		definitions = append(definitions, definition)
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("AIFAR services directory does not contain any module definitions")
	}
	if roles["gateway"] == "" || roles["web"] == "" {
		return nil, fmt.Errorf("AIFAR service catalog requires one gateway role and one web role")
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions, nil
}

func installModuleDefinitions(definitions []serviceDefinition, language string) []registry.InstallModuleDefinition {
	out := make([]registry.InstallModuleDefinition, 0, len(definitions))
	lang := "zh"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(language)), "en") {
		lang = "en"
	}
	for _, definition := range definitions {
		display := strings.TrimSpace(definition.DisplayName[lang])
		if display == "" {
			display = strings.TrimSpace(definition.DisplayName["en"])
		}
		if display == "" {
			display = definition.Name
		}
		out = append(out, registry.InstallModuleDefinition{
			Name: definition.Name, DisplayName: display, Kind: definition.Kind,
			ApplicationName: definition.ApplicationName, Port: definition.Port,
			Required: definition.Required, Role: definition.Role,
			ArtifactExtensions: append([]string(nil), definition.ArtifactExtensions...),
			HealthPath:         definition.HealthPath, AffinityPolicy: definition.AffinityPolicy,
		})
	}
	return out
}

func serviceNames(definitions []serviceDefinition) []string {
	out := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, definition.Name)
	}
	return out
}

func requiredServiceNames(definitions []serviceDefinition) []string {
	var out []string
	for _, definition := range definitions {
		if definition.Required {
			out = append(out, definition.Name)
		}
	}
	return out
}

func normalizeSelectedServicesForCatalog(values []string, definitions []serviceDefinition) []string {
	wanted := map[string]bool{}
	for _, value := range values {
		wanted[cleanAIFARServiceName(value)] = true
	}
	for _, required := range requiredServiceNames(definitions) {
		wanted[required] = true
	}
	out := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if wanted[definition.Name] {
			out = append(out, definition.Name)
		}
	}
	if len(out) == 0 {
		return serviceNames(definitions)
	}
	return out
}

func validateSelectedServicesForCatalog(values []string, definitions []serviceDefinition) error {
	known := map[string]bool{}
	for _, definition := range definitions {
		known[definition.Name] = true
	}
	for _, value := range values {
		name := cleanAIFARServiceName(value)
		if !known[name] {
			return fmt.Errorf("AIFAR service %s is not defined by the runtime-v2 services directory", value)
		}
	}
	return nil
}

func serviceCatalogMetadata(definitions []serviceDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, map[string]any{
			"name": definition.Name, "kind": definition.Kind, "applicationName": definition.ApplicationName,
			"port": definition.Port, "required": definition.Required,
			"role": definition.Role, "artifactExtensions": definition.ArtifactExtensions,
			"healthPath": definition.HealthPath, "affinityPolicy": definition.AffinityPolicy,
			"resources": definition.Resources,
		})
	}
	return out
}

func serviceCatalogMetadataForInstall(definitions []serviceDefinition, gatewayPort, webPort int) []map[string]any {
	copyDefinitions := append([]serviceDefinition(nil), definitions...)
	for idx := range copyDefinitions {
		switch copyDefinitions[idx].Role {
		case "gateway":
			copyDefinitions[idx].Port = gatewayPort
		case "web":
			copyDefinitions[idx].Port = webPort
		}
	}
	return serviceCatalogMetadata(copyDefinitions)
}

func serviceDefinitionsFromMetadata(metadata map[string]any) []serviceDefinition {
	data, err := json.Marshal(metadata["serviceCatalog"])
	if err != nil {
		return legacyServiceDefinitions()
	}
	var definitions []serviceDefinition
	if err := json.Unmarshal(data, &definitions); err != nil || len(definitions) == 0 {
		return legacyServiceDefinitions()
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions
}

func legacyServiceDefinitions() []serviceDefinition {
	out := make([]serviceDefinition, 0, len(serviceOrder))
	for _, name := range serviceOrder {
		definition := serviceDefinition{
			Name: name, Kind: "java", ApplicationName: "alpha-" + name,
			Port:               serviceDefaultPort(name, defaultGatewayPort, defaultWebPort),
			ArtifactExtensions: []string{".jar"}, HealthPath: "/actuator/health/readiness", AffinityPolicy: "round-robin",
		}
		if name == "gateway" {
			definition.Required = true
			definition.Role = "gateway"
			definition.AffinityPolicy = "stable"
		}
		if name == "file" {
			definition.AffinityPolicy = "stable"
		}
		if name == "web-vue3" {
			definition.Kind = "web"
			definition.ApplicationName = ""
			definition.Required = true
			definition.Role = "web"
			definition.ArtifactExtensions = []string{".zip", ".tar", ".tgz", ".tar.gz"}
			definition.HealthPath = "/"
		}
		out = append(out, definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func catalogDefinition(definitions []serviceDefinition, name string) (serviceDefinition, bool) {
	name = cleanAIFARServiceName(name)
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, true
		}
	}
	return serviceDefinition{}, false
}

func serviceCatalogPairs(definitions []serviceDefinition, selected []string, value func(serviceDefinition) string) string {
	wanted := map[string]bool{}
	for _, name := range selected {
		wanted[name] = true
	}
	var pairs []string
	for _, definition := range definitions {
		if wanted[definition.Name] {
			pairs = append(pairs, definition.Name+"="+value(definition))
		}
	}
	return strings.Join(pairs, " ")
}

func serviceNameForRole(definitions []serviceDefinition, role string) string {
	for _, definition := range definitions {
		if definition.Role == role {
			return definition.Name
		}
	}
	return ""
}

func servicePorts(definitions []serviceDefinition) map[string]int {
	out := make(map[string]int, len(definitions))
	for _, definition := range definitions {
		out[definition.Name] = definition.Port
	}
	return out
}
