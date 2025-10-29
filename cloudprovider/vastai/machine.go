package vastai

import (
	"strconv"
	"strings"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

type PortInfo struct {
	HostIp   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type Machine struct {
	ID            int                   `json:"id"`
	MachineID     int                   `json:"machine_id"`
	PublicIP      string                `json:"public_ipaddr"`
	ActualStatus  string                `json:"actual_status"`
	StatusMessage string                `json:"status_msg"`
	Label         string                `json:"label"`
	Ports         map[string][]PortInfo `json:"ports"`
	State         virtualpod.MachineState
}

func GenericMachineAdapter(vastAIMachine *Machine) *virtualpod.Machine {
	machine := &virtualpod.Machine{}
	machine.ID = strconv.Itoa(vastAIMachine.ID)
	machine.MachineID = strconv.Itoa(vastAIMachine.MachineID)
	machine.PublicIP = vastAIMachine.PublicIP

	if vastAIMachine.ActualStatus == "running" {
		machine.State = virtualpod.MachineStateRunning
		// TODO: Handle conversion error
		// TODO: Do not hardcode agent port
		machine.AgentPort, _ = strconv.Atoi(vastAIMachine.Ports["25001/tcp"][0].HostPort)
	} else if vastAIMachine.ActualStatus == "created" && strings.HasPrefix(vastAIMachine.StatusMessage, "Error") {
		machine.State = virtualpod.MachineStateFailed
	} else {
		machine.State = virtualpod.MachineStatePending
	}

	return machine
}

type BundleOffer struct {
	ID          int     `json:"id"`
	DphTotal    float64 `json:"dph_total"`
	Geolocation string  `json:"geolocation"`

	GPUName     string  `json:"gpu_name"`
	NumGPUs     int     `json:"num_gpus"`
	GPURam      int     `json:"gpu_ram"`
	GPUTotalRam int     `json:"gpu_total_ram"`
	CudaMaxGood float64 `json:"cuda_max_good"`
	MachineID   int     `json:"machine_id"`

	CPUName  string `json:"cpu_name"`
	CPUCores int    `json:"cpu_cores"`
	CPURam   int    `json:"cpu_ram"`

	DiskSpace    float64 `json:"disk_space"`
	Verification string  `json:"verification"`
}

type BundleOffers struct {
	Offers []BundleOffer `json:"offers"`
}

// buildInstanceFilters TODO: Allow for better filtering
func buildInstanceFilters(s virtualpod.MachineSpecification) map[string]map[string]interface{} {
	type FilterOp = map[string]interface{}
	type Filters = map[string]FilterOp

	filters := Filters{
		"rentable":  FilterOp{"eq": true},
		"rented":    FilterOp{"eq": false},
		"external":  FilterOp{"eq": false},
		"verified":  FilterOp{"eq": true},
		"inet_down": FilterOp{"gt": 600},
		"num_gpus":  FilterOp{"eq": 1},
		// "datacenter": FilterOp{"eq": true},
	}

	allowedCountries := getAllowedCountries(s.Regions)
	if len(allowedCountries) > 0 {
		filters["geolocation"] = FilterOp{"in": allowedCountries}
	}

	if s.MemoryPerGPUMB > 0 {
		filters["gpu_ram"] = FilterOp{"gte": s.MemoryPerGPUMB}
	}

	if s.TFLOPSMin > 0 {
		filters["total_flops"] = FilterOp{"gte": s.TFLOPSMin}
	}

	if s.DLPerfMin > 0 {
		filters["dlperf"] = FilterOp{"gte": s.DLPerfMin}
	}

	if s.CudaAvailable > 0 {
		filters["cuda_max_good"] = FilterOp{"gte": s.CudaAvailable}
	}

	if s.CPUCores > 0 {
		filters["cpu_cores"] = FilterOp{"gte": s.CPUCores}
	}

	if s.CPURamMB > 0 {
		filters["cpu_ram"] = FilterOp{"gte": s.CPURamMB}
	}

	if s.DiskSpace > 0 {
		filters["disk_space"] = FilterOp{"gte": s.DiskSpace}
	}

	if s.MaxPricePerHour > 0 {
		filters["dph_total"] = FilterOp{"lte": s.MaxPricePerHour}
	}

	return filters
}
