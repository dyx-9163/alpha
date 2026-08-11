package runtimeagent

const (
	DefaultAgentVersion = "runtime-v2"
	DefaultGatewayPort  = 38000
	DefaultWebPort      = 8080
	DefaultNetwork      = "aifar-network"
	DefaultStateDir     = "/var/lib/aifar-agent/instances"
	DefaultIngressMode  = "web-nginx"

	DefaultDeploymentStrategyType    = "RollingUpdate"
	DefaultDeploymentMaxSurge        = 1
	DefaultDeploymentMaxUnavailable  = 0
	DefaultProgressDeadlineSeconds   = 300
	DefaultDeploymentRollbackOnError = true
)

type IngressSpec struct {
	Mode           string `json:"mode,omitempty"`
	GatewayService string `json:"gatewayService,omitempty"`
	WebService     string `json:"webService,omitempty"`
	GatewayPort    int    `json:"gatewayPort,omitempty"`
	WebPort        int    `json:"webPort,omitempty"`
}

type DeploymentStrategySpec struct {
	Type                    string `json:"type,omitempty"`
	MaxSurge                int    `json:"maxSurge,omitempty"`
	MaxUnavailable          int    `json:"maxUnavailable,omitempty"`
	ProgressDeadlineSeconds int    `json:"progressDeadlineSeconds,omitempty"`
	RollbackOnFailure       *bool  `json:"rollbackOnFailure,omitempty"`
}

type ContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort"`
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

func NormalizeDeploymentStrategy(strategy DeploymentStrategySpec) DeploymentStrategySpec {
	if strategy.Type == "" {
		strategy.Type = DefaultDeploymentStrategyType
	}
	if strategy.MaxSurge < 0 {
		strategy.MaxSurge = 0
	}
	if strategy.MaxSurge == 0 && strategy.MaxUnavailable == 0 {
		strategy.MaxSurge = DefaultDeploymentMaxSurge
	}
	if strategy.MaxUnavailable < 0 {
		strategy.MaxUnavailable = DefaultDeploymentMaxUnavailable
	}
	if strategy.ProgressDeadlineSeconds <= 0 {
		strategy.ProgressDeadlineSeconds = DefaultProgressDeadlineSeconds
	}
	if strategy.RollbackOnFailure == nil {
		value := DefaultDeploymentRollbackOnError
		strategy.RollbackOnFailure = &value
	}
	return strategy
}
