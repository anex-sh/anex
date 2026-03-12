package vastai

import (
	"bytes"
	"text/template"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

type OnStartTemplateParams struct {
	Workdir        string
	Command        string
	AgentURL       string
	WireproxyURL   string
	PromtailURL    string
	ProxyConfig    virtualpod.PodProxyConfig
	ContainerPorts []int
	ProxyTunnels   []ProxyTunnel
}

func GenerateOnStartScript(params OnStartTemplateParams) string {
	t := template.Must(template.New("onstart").Funcs(template.FuncMap{
		"add": func(a, b, c int) int { return a + b + c },
	}).Parse(onStartScriptTemplate))
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		panic(err)
	}

	return buf.String()
}

const onStartScriptTemplate = `
#!/bin/bash
set -euo pipefail
sleep 3

touch ~/.no_auto_tmux

ensure_curl() {
    if command -v curl >/dev/null 2>&1; then
        return 0
    fi

    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -y
        apt-get install -y curl
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y curl
    elif command -v apk >/dev/null 2>&1; then
        apk add --no-cache curl
    else
        echo "No supported package manager found (apt-get, dnf, apk)" >&2
        return 1
    fi
}

ensure_curl || {
    echo "curl is required but could not be installed"
    exit 1
}

export GPU_PROVIDER_GATEWAY_CLIENT_ADDRESS={{ .ProxyConfig.Client.Address }}
export GPU_PROVIDER_GATEWAY_CLIENT_PK={{ .ProxyConfig.Client.PrivateKey }}
export GPU_PROVIDER_GATEWAY_CLIENT_SERVER_ENDPOINT={{ .ProxyConfig.Server.Endpoint }}
export GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PORT={{ .ProxyConfig.Server.PortUDP }}
export GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PK={{ .ProxyConfig.Server.PublicKey }}

mkdir -p /etc/virtualpod
cat <<EOF > /etc/virtualpod/wireproxy.tpl
[Interface]
Address     = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_ADDRESS}" }}
PrivateKey  = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_PK}" }}
ListenPort  = {{ "${VAST_UDP_PORT_72000}" }}
DNS         = 10.254.254.1

[Peer]
PublicKey           = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PK}" }}
Endpoint            = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_SERVER_ENDPOINT}" }}:{{ "${GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PORT}" }}
AllowedIPs          = 0.0.0.0/0
PersistentKeepalive = 25

[HTTP]
BindAddress = 127.0.0.1:3128

[TCPServerTunnel]
ListenPort = {{ .ProxyConfig.Client.GatewayPortOffset }}
Target = 127.0.0.1:8080

{{ range $i, $p := .ContainerPorts }}
[TCPServerTunnel]
ListenPort = {{ add $.ProxyConfig.Client.GatewayPortOffset $i 1 }}
Target     = 127.0.0.1:{{ $p }}
{{ end }}

{{- range $i, $t := .ProxyTunnels }}
[TCPClientTunnel]
BindAddress = 127.0.0.1:{{ $t.ContainerPort }}
Target      = {{ $t.Address }}
{{ end }}
EOF

# rm -rf /etc/pip.conf
export PIP_PROXY="http://127.0.0.1:3128"
unset AWS_WEB_IDENTITY_TOKEN_FILE

{{- if .Workdir }}
cd {{ .Workdir }}
{{- end }}

curl {{ .WireproxyURL }} -o /usr/bin/wireproxy
curl {{ .PromtailURL }} -o /usr/bin/promtail
curl {{ .AgentURL }} -o /container_agent

chmod +x /usr/bin/wireproxy
chmod +x /usr/bin/promtail
chmod +x /container_agent

{{ .Command }}
`
