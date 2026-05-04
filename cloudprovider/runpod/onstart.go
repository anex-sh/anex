package runpod

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/anex-sh/anex/virtualpod"
	v1 "k8s.io/api/core/v1"
)

type proxyTunnel struct {
	ContainerPort int
	Address       string
}

type wireproxyTemplateParams struct {
	Proxy          virtualpod.PodProxyConfig
	ContainerPorts []int
	ProxyTunnels   []proxyTunnel
}

const wireproxyConfigTemplate = `[Interface]
Address     = {{ .Proxy.Client.Address }}
PrivateKey  = {{ .Proxy.Client.PrivateKey }}
DNS         = 10.254.254.1
MTU         = 1400

[Peer]
PublicKey           = {{ .Proxy.Server.PublicKey }}
Endpoint            = 127.0.0.1:51820
AllowedIPs          = 0.0.0.0/0
PersistentKeepalive = 25

[HTTP]
BindAddress = 127.0.0.1:3128

[TCPServerTunnel]
ListenPort = {{ .Proxy.Client.GatewayPortOffset }}
Target = 127.0.0.1:8080
{{ range $i, $p := .ContainerPorts }}
[TCPServerTunnel]
ListenPort = {{ add $.Proxy.Client.GatewayPortOffset $i 1 }}
Target     = 127.0.0.1:{{ $p }}
{{ end }}
{{- range .ProxyTunnels }}
[TCPClientTunnel]
BindAddress = 127.0.0.1:{{ .ContainerPort }}
Target      = {{ .Address }}
{{ end }}`

func generateWireproxyConfig(params wireproxyTemplateParams) string {
	t := template.Must(template.New("wireproxy").Funcs(template.FuncMap{
		"add": func(a, b, c int) int { return a + b + c },
	}).Parse(wireproxyConfigTemplate))
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		panic(err)
	}
	return buf.String()
}

// shellQuote single-quotes a string for safe bash embedding.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func joinShellArgs(args []string) string {
	escaped := make([]string, 0, len(args))
	for _, a := range args {
		escaped = append(escaped, shellQuote(a))
	}
	return strings.Join(escaped, " ")
}

// ProvisionEnv holds all environment variables needed by init.sh.
type ProvisionEnv struct {
	WireproxyConfig string // base64-encoded wireproxy config
	AgentCmd        string // /container_agent run ...
	Workdir         string
	AgentURL        string
	WireproxyURL    string
	WstunnelURL     string
	PromtailURL     string
	InitURL         string
	GatewayEndpoint string // WG server IP/DNS (for wstunnel)
	GatewayWSPort   string // WG server port (for wstunnel)
}

// BuildProvisionEnv builds the environment variables for the RunPod init script.
func BuildProvisionEnv(pod *v1.Pod, proxy virtualpod.PodProxyConfig, promtail bool, urls URLConfig) ProvisionEnv {
	// Collect and sort container ports
	var ports []int
	for _, c := range pod.Spec.Containers {
		for _, cp := range c.Ports {
			ports = append(ports, int(cp.ContainerPort))
		}
	}
	sort.Ints(ports)

	// Collect proxy tunnels from GW_TUNNEL_ env vars
	var tunnels []proxyTunnel
	if len(pod.Spec.Containers) > 0 {
		for _, env := range pod.Spec.Containers[0].Env {
			const prefix = "GW_TUNNEL_"
			if !strings.HasPrefix(env.Name, prefix) || env.Value == "" {
				continue
			}
			portStr := strings.TrimPrefix(env.Name, prefix)
			port, err := strconv.Atoi(portStr)
			if err != nil {
				continue
			}
			tunnels = append(tunnels, proxyTunnel{
				ContainerPort: port,
				Address:       env.Value,
			})
		}
	}

	// Generate wireproxy config
	wpConfig := generateWireproxyConfig(wireproxyTemplateParams{
		Proxy:          proxy,
		ContainerPorts: ports,
		ProxyTunnels:   tunnels,
	})

	// Build agent command
	var argv []string
	if len(pod.Spec.Containers) > 0 {
		cn := pod.Spec.Containers[0]
		if len(cn.Command) > 0 {
			argv = append(argv, cn.Command...)
			argv = append(argv, cn.Args...)
		} else if len(cn.Args) > 0 {
			argv = []string{"/bin/sh", "-lc", strings.Join(cn.Args, " ")}
		}
	}

	agentCmd := "/container_agent run -p 8080"
	if proxy.Enabled {
		agentCmd += " --proxy"
	}
	if promtail {
		agentCmd += " --promtail"
	}
	if len(argv) > 0 {
		agentCmd += " -- " + joinShellArgs(argv)
	}

	var workdir string
	if len(pod.Spec.Containers) > 0 {
		workdir = pod.Spec.Containers[0].WorkingDir
	}

	return ProvisionEnv{
		WireproxyConfig: base64.StdEncoding.EncodeToString([]byte(wpConfig)),
		AgentCmd:        agentCmd,
		Workdir:         workdir,
		AgentURL:        urls.AgentURL,
		WireproxyURL:    urls.WireproxyURL,
		WstunnelURL:     urls.WstunnelURL,
		PromtailURL:     urls.PromtailURL,
		InitURL:         urls.InitURL,
		GatewayEndpoint: proxy.Server.Endpoint,
		GatewayWSPort:   fmt.Sprintf("%d", proxy.Server.PortTCP),
	}
}

// ToEnvMap returns the environment variables as a map for the RunPod API.
func (e ProvisionEnv) ToEnvMap() map[string]string {
	env := map[string]string{
		"GPU_PROVIDER_WIREPROXY_CONFIG": e.WireproxyConfig,
		"GPU_PROVIDER_AGENT_CMD":        e.AgentCmd,
		"GPU_PROVIDER_AGENT_URL":        e.AgentURL,
		"GPU_PROVIDER_WIREPROXY_URL":    e.WireproxyURL,
		"GPU_PROVIDER_WSTUNNEL_URL":     e.WstunnelURL,
		"GPU_PROVIDER_INIT_URL":         e.InitURL,
		"GPU_PROVIDER_GATEWAY_ENDPOINT": e.GatewayEndpoint,
		"GPU_PROVIDER_GATEWAY_WS_PORT":  e.GatewayWSPort,
	}
	if e.PromtailURL != "" {
		env["GPU_PROVIDER_PROMTAIL_URL"] = e.PromtailURL
	}
	if e.Workdir != "" {
		env["GPU_PROVIDER_WORKDIR"] = e.Workdir
	}
	return env
}
