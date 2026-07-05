package runtimeagent

import "testing"

func TestNormalizeSpecDefaultsRuntimeV2Fields(t *testing.T) {
	spec := NormalizeSpec(RuntimeSpec{
		InstallRoot: "/aifar/apps/admin",
		Services: []ServiceSpec{
			{Name: "gateway", Port: 38000},
			{Name: "web-vue3", Port: 8080},
			{Name: "file", ListenPort: 38005, TargetPort: 18080},
		},
		Deployments: []DeploymentSpec{
			{ServiceName: "file", Image: "aifar-file:test"},
		},
	})
	if spec.Version != DefaultAgentVersion || spec.Ingress.Mode != DefaultIngressMode {
		t.Fatalf("expected runtime-v2 web-nginx defaults, got version=%s mode=%s", spec.Version, spec.Ingress.Mode)
	}
	if spec.Nacos.Ephemeral == nil || !*spec.Nacos.Ephemeral {
		t.Fatalf("expected Nacos ephemeral default, got %#v", spec.Nacos.Ephemeral)
	}
	file, ok := serviceByName(spec, "file")
	if !ok || file.ListenPort != 38005 || file.TargetPort != 18080 {
		t.Fatalf("expected file service listen/target ports to be preserved, got %#v", file)
	}
	if len(spec.Deployments) != 1 || len(spec.Deployments[0].Ports) != 1 || spec.Deployments[0].Ports[0].ContainerPort != 18080 {
		t.Fatalf("expected deployment port to default from targetPort, got %#v", spec.Deployments)
	}
}

func TestValidateRuntimeSpecRejectsMissingGatewayAndWebServices(t *testing.T) {
	spec := NormalizeSpec(RuntimeSpec{
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Ingress: IngressSpec{
			GatewayService: "missing-gateway",
			WebService:     "missing-web",
			GatewayPort:    38000,
			WebPort:        8080,
		},
		Services: []ServiceSpec{
			{Name: "file", ListenPort: 38005, TargetPort: 38005},
		},
	})
	spec.Services = spec.Services[:1]
	if err := validateRuntimeSpec(spec); err == nil {
		t.Fatal("expected missing gateway/web services to be rejected")
	}
}
