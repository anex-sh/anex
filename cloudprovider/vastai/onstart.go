package vastai

import (
	"bytes"
	"text/template"
)

type OnStartTemplateParams struct {
	Workdir      string
	Command      string
	AgentURL     string
	WireproxyURL string
	PromtailURL  string
	AuthToken    string
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

// TODO: Make Promtail optional
const onStartScriptTemplate = `
#!/bin/bash
set -euo pipefail
sleep 3

touch ~/.no_auto_tmux

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

/container_agent run -p 25001 -c "{{ .Command }}" --auth-token "{{ .AuthToken }}" --promtail
`
