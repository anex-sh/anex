package virtualpod

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"text/template"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gopkg.in/yaml.v3"
)

const wireproxyConfigTemplate = `
[Interface]
Address     = {{ .ProxyConfig.Client.Address }}
PrivateKey  = {{ .ProxyConfig.Client.PrivateKey }}
ListenPort  = {{ "${VAST_UDP_PORT_72000}" }}
DNS         = 1.1.1.1

[Peer]
PublicKey           = {{ .ProxyConfig.Server.PublicKey }}
Endpoint            = {{ .ProxyConfig.Server.Endpoint }}
AllowedIPs          = 0.0.0.0/0
PersistentKeepalive = 25

[HTTP]
BindAddress = 127.0.0.1:3128

{{- if .ContainerPorts }}
{{ range $i, $p := .ContainerPorts }}
[TCPServerTunnel]
ListenPort = {{ add $.ProxyConfig.Client.GatewayPortOffset $i }}
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

type ProxyServerConfig struct {
	Endpoint  string `yaml:"endpoint"`
	PublicKey string `yaml:"public_key"`
	DNS       string `yaml:"dns,omitempty"`
}

type ProxyClientConfig struct {
	Address           string `yaml:"address"`
	PrivateKey        string `yaml:"private_key"`
	PublicKey         string `yaml:"public_key"`
	GatewayPortOffset int    `yaml:"gateway_port_offset"`
	Assigned          bool
}

type ProxyConfig struct {
	Server ProxyServerConfig `yaml:"server"`
	Client ProxyClientConfig `yaml:"client"`
}

type ProxyTunnels struct {
	Endpoints []struct {
		Address       string `yaml:"address"`
		ContainerPort int    `yaml:"containerPort"`
	} `yaml:"endpoints"`
}

func (vp *VirtualPod) generateWireproxyConfig(ctx context.Context) (string, error) {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	logger := log.G(ctx)

	if vp.proxyConfig == nil {
		return "", errors.New("proxy config not set")
	}

	// Open Ports
	var ports []int
	for _, cp := range vp.pod.Spec.Containers[0].Ports {
		ports = append(ports, int(cp.ContainerPort))
	}
	sort.Ints(ports)

	// Proxy proxyTunnels
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

	type OnStartTemplateParams struct {
		ProxyConfig    ProxyConfig
		ContainerPorts []int
		ProxyTunnels   ProxyTunnels
	}

	params := OnStartTemplateParams{
		ProxyConfig:    *vp.proxyConfig,
		ContainerPorts: ports,
		ProxyTunnels:   proxyTunnels,
	}

	t := template.Must(template.New("wireproxy").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).Parse(wireproxyConfigTemplate))

	var output bytes.Buffer
	err := t.Execute(&output, params)

	return output.String(), err
}
