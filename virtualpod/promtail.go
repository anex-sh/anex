package virtualpod

const promtailTemplate = `
server:
  http_listen_port: 0
  grpc_listen_port: 0

clients:
  - url: {{ .URL }}
    basic_auth:
      username: {{ .Username }}
      password: {{ .Password }}

scrape_configs:
  - job_name: experiment
    static_configs:
    - targets:
        - localhost
      labels:
        job: vastai
        vast_namespace: {{ .Namespace }}
        vast_pod: {{ .PodName }}
        __path__: /var/log/agent/main.log
`

type LokiPushGateway struct {
	PodName   string
	Namespace string
	URL       string
	Username  string
	Password  string
}
