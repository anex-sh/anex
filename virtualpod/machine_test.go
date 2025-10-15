package virtualpod

import "testing"

func TestMachineGetAgentAddress(t *testing.T) {
	m := &Machine{PublicIP: "10.0.0.5", AgentPort: 8080}
	if got := m.GetAgentAddress(); got != "http://10.0.0.5:8080" {
		t.Fatalf("unexpected agent address: %s", got)
	}
}
