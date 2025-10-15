package vastai

import (
	"strings"
	"testing"
)

func TestGenerateOnStartScriptRendersParams(t *testing.T) {
	params := OnStartTemplateParams{
		Workdir:      "/work",
		Command:      "python app.py",
		AgentURL:     "https://agent/bin",
		WireproxyURL: "https://wireproxy/bin",
		PromtailURL:  "https://promtail/bin",
		AuthToken:    "secrettok",
	}
	out := GenerateOnStartScript(params)
	checks := []string{
		"cd /work",
		"curl https://wireproxy/bin -o /usr/bin/wireproxy",
		"curl https://promtail/bin -o /usr/bin/promtail",
		"curl https://agent/bin -o /container_agent",
		"/container_agent run -p 25001 -c \"python app.py\" --auth-token \"secrettok\" --promtail",
	}
	for _, sub := range checks {
		if !strings.Contains(out, sub) {
			t.Fatalf("script missing fragment %q. Output:\n%s", sub, out)
		}
	}
}
