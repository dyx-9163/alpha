package runtimeagent

const (
	DefaultAgentVersion = "runtime-v1"
	DefaultGatewayPort  = 38000
	DefaultWebPort      = 8080
	DefaultNetwork      = "aifar-network"
	DefaultStateDir     = "/var/lib/aifar-agent/instances"
)

type RuntimeSpec struct {
	Version     string        `json:"version,omitempty"`
	InstanceID  string        `json:"instanceId,omitempty"`
	InstallRoot string        `json:"installRoot,omitempty"`
	Network     string        `json:"network,omitempty"`
	Services    []ServiceSpec `json:"services,omitempty"`
	Ingress     IngressSpec   `json:"ingress"`
}

type IngressSpec struct {
	GatewayService string `json:"gatewayService,omitempty"`
	WebService     string `json:"webService,omitempty"`
	GatewayPort    int    `json:"gatewayPort,omitempty"`
	WebPort        int    `json:"webPort,omitempty"`
}

type ServiceSpec struct {
	Name    string `json:"name"`
	AppName string `json:"appName,omitempty"`
	Port    int    `json:"port"`
}

func NormalizeSpec(spec RuntimeSpec) RuntimeSpec {
	if spec.Version == "" {
		spec.Version = DefaultAgentVersion
	}
	if spec.InstanceID == "" {
		spec.InstanceID = "admin"
	}
	if spec.Network == "" {
		spec.Network = DefaultNetwork
	}
	if spec.Ingress.GatewayService == "" {
		spec.Ingress.GatewayService = "gateway"
	}
	if spec.Ingress.WebService == "" {
		spec.Ingress.WebService = "web-vue3"
	}
	if spec.Ingress.GatewayPort == 0 {
		spec.Ingress.GatewayPort = DefaultGatewayPort
	}
	if spec.Ingress.WebPort == 0 {
		spec.Ingress.WebPort = DefaultWebPort
	}
	seen := map[string]bool{}
	services := make([]ServiceSpec, 0, len(spec.Services)+2)
	for _, service := range spec.Services {
		if service.Name == "" || service.Port <= 0 || seen[service.Name] {
			continue
		}
		seen[service.Name] = true
		services = append(services, service)
	}
	if !seen[spec.Ingress.GatewayService] {
		services = append(services, ServiceSpec{Name: spec.Ingress.GatewayService, AppName: "alpha-gateway", Port: spec.Ingress.GatewayPort})
	}
	if !seen[spec.Ingress.WebService] {
		services = append(services, ServiceSpec{Name: spec.Ingress.WebService, Port: spec.Ingress.WebPort})
	}
	spec.Services = services
	return spec
}
