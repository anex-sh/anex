package vastai

import (
	"bytes"
	"text/template"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

type OnStartTemplateParams struct {
	Workdir      string
	Command      string
	AgentURL     string
	WireproxyURL string
	PromtailURL  string
	ProxyConfig  virtualpod.PodProxyConfig
}

func GenerateOnStartScript(params OnStartTemplateParams) string {
	t := template.Must(template.New("onstart").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
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

export GPU_PROVIDER_GATEWAY_CLIENT_ADDRESS={{ .ProxyConfig.Client.Address }}
export GPU_PROVIDER_GATEWAY_CLIENT_PK={{ .ProxyConfig.Client.PrivateKey }}
export GPU_PROVIDER_GATEWAY_CLIENT_SERVER_ENDPOINT={{ .ProxyConfig.Server.Endpoint }}
export GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PK={{ .ProxyConfig.Server.PublicKey }}

mkdir -p /etc/virtualpod
cat <<EOF > /etc/virtualpod/wireproxy.tpl
[Interface]
Address     = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_ADDRESS}" }}
PrivateKey  = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_PK}" }}
ListenPort  = {{ "${VAST_UDP_PORT_72000}" }}

[Peer]
PublicKey           = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PK}" }}
Endpoint            = {{ "${GPU_PROVIDER_GATEWAY_CLIENT_SERVER_ENDPOINT}" }}
AllowedIPs          = 0.0.0.0/0
PersistentKeepalive = 25

[TCPServerTunnel]
ListenPort = 9000
Target = 127.0.0.1:8080
EOF

# rm -rf /etc/pip.conf
export PIP_PROXY="http://127.0.0.1:3128"
unset AWS_WEB_IDENTITY_TOKEN_FILE

cd {{ .Workdir }}

curl {{ .WireproxyURL }} -o /usr/bin/wireproxy
curl {{ .PromtailURL }} -o /usr/bin/promtail
curl {{ .AgentURL }} -o /container_agent

chmod +x /usr/bin/wireproxy
chmod +x /usr/bin/promtail
chmod +x /container_agent

{{ .Command }}
`
