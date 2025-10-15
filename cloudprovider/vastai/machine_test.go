package vastai

import (
	"testing"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

func TestGenericMachineAdapterStatesAndPort(t *testing.T) {
	// Running maps to Running and parses agent port from 25001/tcp
	vm := &Machine{
		ID:           42,
		PublicIP:     "1.2.3.4",
		ActualStatus: "running",
		Ports: map[string][]PortInfo{
			"25001/tcp": []PortInfo{{HostIp: "0.0.0.0", HostPort: "25001"}},
		},
	}
	m := GenericMachineAdapter(vm)
	if m.State != virtualpod.MachineStateRunning {
									 t.Fatalf("expected running state, got %v", m.State)
	}
	if m.AgentPort != 25001 {
		 t.Fatalf("expected agent port 25001, got %d", m.AgentPort)
	}
	if m.PublicIP != "1.2.3.4" || m.ID != "42" {
		 t.Fatalf("unexpected fields: %+v", m)
	}

	// Created with Error maps to Failed
	vm = &Machine{ID: 7, ActualStatus: "created", StatusMessage: "Error: something"}
	m = GenericMachineAdapter(vm)
	if m.State != virtualpod.MachineStateFailed {
		 t.Fatalf("expected failed state, got %v", m.State)
	}

	// Other -> Pending
	vm = &Machine{ID: 8, ActualStatus: "queued"}
	m = GenericMachineAdapter(vm)
	if m.State != virtualpod.MachineStatePending {
		 t.Fatalf("expected pending state, got %v", m.State)
	}
}
