package virtualpod

import "fmt"

type MachineState int

const (
	MachineStatePending MachineState = iota
	MachineStateRunning
	MachineStateFailed
	MachineStateUnknown
)

type Region int

const (
	RegionEurope Region = iota
	RegionNorthAmerica
	RegionSouthAmerica
	RegionAsia
	RegionAfrica
	RegionOceania
	RegionAny
)

type MachineSpecification struct {
	GPUCount        int
	MemoryPerGPUMB  int
	TFLOPSMin       float64
	DLPerfMin       float64
	CudaMax         float64
	CPUCores        int
	CPURamMB        int
	DiskSpace       int
	MaxPricePerHour float64
	Regions         []Region
}

type Offer struct {
	OfferID   string
	MachineID string
}

type Machine struct {
	ID        string
	MachineID string
	PublicIP  string
	AgentPort int
	State     MachineState
}

func (m *Machine) GetAgentAddress() string {
	return fmt.Sprintf("http://%s:%d", m.PublicIP, m.AgentPort)
}
