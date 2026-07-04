package runtimeagent

const (
	DefaultAgentVersion      = "runtime-v1"
	DefaultIngressImage      = "nginx:stable-alpine"
	DefaultIngressContainer  = "aifar-admin-ingress"
	DefaultGatewayService    = "aifar-svc-admin-gateway"
	DefaultWebService        = "aifar-svc-admin-web-vue3"
	DefaultGatewayPort       = 38000
	DefaultWebPort           = 8080
	DefaultNetwork           = "aifar-network"
	DefaultIngressConfigPath = "/var/lib/aifar-agent/instances/admin/ingress/nginx.conf"
)

type RuntimeSpec struct {
	Version     string      `json:"version,omitempty"`
	InstanceID  string      `json:"instanceId,omitempty"`
	InstallRoot string      `json:"installRoot,omitempty"`
	Network     string      `json:"network,omitempty"`
	Ingress     IngressSpec `json:"ingress"`
}

type IngressSpec struct {
	Container      string            `json:"container,omitempty"`
	Image          string            `json:"image,omitempty"`
	ConfigPath     string            `json:"configPath,omitempty"`
	GatewayService string            `json:"gatewayService,omitempty"`
	WebService     string            `json:"webService,omitempty"`
	GatewayPort    int               `json:"gatewayPort,omitempty"`
	WebPort        int               `json:"webPort,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
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
	if spec.Ingress.Container == "" {
		spec.Ingress.Container = DefaultIngressContainer
	}
	if spec.Ingress.Image == "" {
		spec.Ingress.Image = DefaultIngressImage
	}
	if spec.Ingress.ConfigPath == "" {
		spec.Ingress.ConfigPath = DefaultIngressConfigPath
	}
	if spec.Ingress.GatewayService == "" {
		spec.Ingress.GatewayService = DefaultGatewayService
	}
	if spec.Ingress.WebService == "" {
		spec.Ingress.WebService = DefaultWebService
	}
	if spec.Ingress.GatewayPort == 0 {
		spec.Ingress.GatewayPort = DefaultGatewayPort
	}
	if spec.Ingress.WebPort == 0 {
		spec.Ingress.WebPort = DefaultWebPort
	}
	if spec.Ingress.Labels == nil {
		spec.Ingress.Labels = map[string]string{}
	}
	spec.Ingress.Labels["aifar.app"] = "aifar"
	spec.Ingress.Labels["aifar.component"] = "ingress"
	spec.Ingress.Labels["aifar.instance"] = spec.InstanceID
	if spec.InstallRoot != "" {
		spec.Ingress.Labels["aifar.install-root"] = spec.InstallRoot
	}
	return spec
}
