package runtimeagent

const (
	DefaultAgentVersion = "runtime-v2"
	DefaultGatewayPort  = 38000
	DefaultWebPort      = 8080
	DefaultNetwork      = "aifar-network"
	DefaultStateDir     = "/var/lib/aifar-agent/instances"
	DefaultIngressMode  = "web-nginx"
)

type RuntimeSpec struct {
	Version     string           `json:"version,omitempty"`
	InstanceID  string           `json:"instanceId,omitempty"`
	InstallRoot string           `json:"installRoot,omitempty"`
	Network     string           `json:"network,omitempty"`
	Deployments []DeploymentSpec `json:"deployments,omitempty"`
	Services    []ServiceSpec    `json:"services,omitempty"`
	Ingress     IngressSpec      `json:"ingress"`
	Nacos       NacosSpec        `json:"nacos,omitempty"`
}

type IngressSpec struct {
	Mode           string `json:"mode,omitempty"`
	GatewayService string `json:"gatewayService,omitempty"`
	WebService     string `json:"webService,omitempty"`
	GatewayPort    int    `json:"gatewayPort,omitempty"`
	WebPort        int    `json:"webPort,omitempty"`
}

type ServiceSpec struct {
	Name           string `json:"name"`
	AppName        string `json:"appName,omitempty"`
	Port           int    `json:"port,omitempty"`
	ListenPort     int    `json:"listenPort,omitempty"`
	TargetPort     int    `json:"targetPort,omitempty"`
	AffinityPolicy string `json:"affinityPolicy,omitempty"`
}

type DeploymentSpec struct {
	ServiceName string            `json:"serviceName"`
	Image       string            `json:"image,omitempty"`
	Revision    string            `json:"revision,omitempty"`
	Replicas    int               `json:"replicas,omitempty"`
	Ports       []ContainerPort   `json:"ports,omitempty"`
	EnvFiles    []string          `json:"envFiles,omitempty"`
	Volumes     []VolumeMount     `json:"volumes,omitempty"`
	Resources   ResourceSpec      `json:"resources,omitempty"`
	HealthCheck HealthCheckSpec   `json:"healthCheck,omitempty"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type ContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int   `json:"containerPort"`
}

type VolumeMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type ResourceSpec struct {
	CPUs   string `json:"cpus,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type HealthCheckSpec struct {
	Command     string `json:"command,omitempty"`
	Interval    string `json:"interval,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
	Retries     int    `json:"retries,omitempty"`
	StartPeriod string `json:"startPeriod,omitempty"`
}

type NacosSpec struct {
	Namespace       string `json:"namespace,omitempty"`
	Group           string `json:"group,omitempty"`
	Ephemeral       *bool  `json:"ephemeral,omitempty"`
	AgentIPStrategy string `json:"agentIPStrategy,omitempty"`
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
	if spec.Ingress.Mode == "" {
		spec.Ingress.Mode = DefaultIngressMode
	}
	if spec.Ingress.GatewayPort == 0 {
		spec.Ingress.GatewayPort = DefaultGatewayPort
	}
	if spec.Ingress.WebPort == 0 {
		spec.Ingress.WebPort = DefaultWebPort
	}
	if spec.Nacos.Namespace == "" {
		spec.Nacos.Namespace = "prod"
	}
	if spec.Nacos.AgentIPStrategy == "" {
		spec.Nacos.AgentIPStrategy = "auto"
	}
	if spec.Nacos.Ephemeral == nil {
		value := true
		spec.Nacos.Ephemeral = &value
	}
	seen := map[string]bool{}
	services := make([]ServiceSpec, 0, len(spec.Services)+2)
	for _, service := range spec.Services {
		if service.Name == "" || seen[service.Name] {
			continue
		}
		if service.ListenPort == 0 {
			service.ListenPort = service.Port
		}
		if service.TargetPort == 0 {
			service.TargetPort = service.Port
		}
		if service.Port == 0 {
			service.Port = service.ListenPort
		}
		if service.ListenPort <= 0 || service.TargetPort <= 0 {
			continue
		}
		seen[service.Name] = true
		services = append(services, service)
	}
	if !seen[spec.Ingress.GatewayService] {
		services = append(services, ServiceSpec{Name: spec.Ingress.GatewayService, AppName: "alpha-gateway", Port: spec.Ingress.GatewayPort, ListenPort: spec.Ingress.GatewayPort, TargetPort: spec.Ingress.GatewayPort})
	}
	if !seen[spec.Ingress.WebService] {
		services = append(services, ServiceSpec{Name: spec.Ingress.WebService, Port: spec.Ingress.WebPort, ListenPort: spec.Ingress.WebPort, TargetPort: spec.Ingress.WebPort})
	}
	spec.Services = services
	deploymentSeen := map[string]bool{}
	deployments := make([]DeploymentSpec, 0, len(spec.Deployments))
	for _, deployment := range spec.Deployments {
		if deployment.ServiceName == "" || deploymentSeen[deployment.ServiceName] {
			continue
		}
		if deployment.Replicas < 1 {
			deployment.Replicas = 1
		}
		if len(deployment.Ports) == 0 {
			if service, ok := serviceByName(spec, deployment.ServiceName); ok {
				deployment.Ports = []ContainerPort{{Name: "http", ContainerPort: service.TargetPort}}
			}
		}
		deploymentSeen[deployment.ServiceName] = true
		deployments = append(deployments, deployment)
	}
	spec.Deployments = deployments
	return spec
}
