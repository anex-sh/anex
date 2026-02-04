package virtualpod

import (
	"bytes"
	"context"
	"sort"
	"text/template"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gopkg.in/yaml.v3"
)

const wireproxyConfigTemplate = `
[Interface]
Address     = {{ .ProxyConfig.Client.Address }}
PrivateKey  = {{ .ProxyConfig.Client.PrivateKey }}
ListenPort  = {{ .WireproxyPort }}
DNS         = 10.254.254.1

[Peer]
PublicKey           = {{ .ProxyConfig.Server.PublicKey }}
Endpoint            = {{ .ProxyConfig.Server.Endpoint }}
AllowedIPs          = 0.0.0.0/0
PersistentKeepalive = 25

[HTTP]
BindAddress = 127.0.0.1:3128

[TCPServerTunnel]
ListenPort = {{ .AgentPublicPort }}
Target = 127.0.0.1:{{ .AgentLocalPort }}

{{- if .ContainerPorts }}
{{ range $i, $p := .ContainerPorts }}
[TCPServerTunnel]
ListenPort = {{ add $.ProxyConfig.Client.GatewayPortOffset $i 1 }}
Target     = 127.0.0.1:{{ $p }}
{{ end }}
{{- end }}

{{- if .ProxyTunnels }}
{{- range $i, $t := .ProxyTunnels.Endpoints }}
[TCPClientTunnel]
BindAddress = 127.0.0.1:{{ $t.ContainerPort }}
Target      = {{ $t.Address }}
{{ end }}
{{- end }}
`

type OnStartTemplateParams struct {
	ProxyConfig     PodProxyConfig
	ContainerPorts  []int
	ProxyTunnels    ProxyTunnels
	WireproxyPort   string
	AgentPublicPort string
	AgentLocalPort  string
}

func (vp *VirtualPod) generateWireproxyConfig(ctx context.Context, proxyConfig PodProxyConfig, wireproxyPort, agentPublicPort, agentLocalPort string) (string, error) {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	logger := log.G(ctx)

	// Open Ports
	var ports []int
	for _, cp := range vp.pod.Spec.Containers[0].Ports {
		ports = append(ports, int(cp.ContainerPort))
	}
	sort.Ints(ports)

	// Gateway proxyTunnels
	var proxyTunnels ProxyTunnels
	for _, cm := range vp.volumeMounts {
		if cm.TargetPath == "/etc/virtualpod/proxy.conf" {
			configMapData := []byte(vp.configMaps[cm.ConfigMapName]["proxy.conf"])
			err := yaml.Unmarshal(configMapData, &proxyTunnels)
			if err != nil {
				logger.Errorf("Unmarshal: %v", err)
			}
			break
		}
	}

	params := OnStartTemplateParams{
		ProxyConfig:     proxyConfig,
		ContainerPorts:  ports,
		ProxyTunnels:    proxyTunnels,
		WireproxyPort:   wireproxyPort,
		AgentPublicPort: agentPublicPort,
		AgentLocalPort:  agentLocalPort,
	}

	t := template.Must(template.New("wireproxy").Funcs(template.FuncMap{
		"add": func(a, b, c int) int { return a + b + c },
	}).Parse(wireproxyConfigTemplate))

	var output bytes.Buffer
	err := t.Execute(&output, params)

	return output.String(), err
}
