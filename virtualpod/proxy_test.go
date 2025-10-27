package virtualpod

import (
	"context"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestGenerateWireproxyConfigRendersPortsAndTunnels(t *testing.T) {
	pod := newTestPod()
	pod.Spec.Containers[0].Ports = []v1.ContainerPort{{ContainerPort: 8080}, {ContainerPort: 80}}
	cfgMaps := map[string]map[string]string{
		"proxy": {"proxy.conf": "endpoints:\n  - address: a.example:1\n    containerPort: 9090\n"},
	}

	vp := NewVirtualPod(
		"id1",
		pod,
		&Machine{PublicIP: "1.2.3.4", AgentPort: 1234},
		&ProxyConfig{Client: ProxyClientConfig{GatewayPortOffset: 32000}},
		cfgMaps, []FileMapping{{
			TargetPath:    "/etc/virtualpod/proxy.conf",
			ConfigMapName: "proxy",
			Key:           "proxy.conf",
		}},
		"abcd",
	)

	out, err := vp.generateWireproxyConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "ListenPort = 32000") || !strings.Contains(out, "ListenPort = 32001") {
		t.Fatalf("generated config missing expected ListenPort lines:\n%s", out)
	}
	if !strings.Contains(out, "BindAddress = 127.0.0.1:9090") {
		t.Fatalf("missing tunnel bind address in config: %s", out)
	}
}
