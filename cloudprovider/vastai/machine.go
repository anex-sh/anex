package vastai

import (
	"strconv"
	"strings"

	"github.com/anex-sh/anex/virtualpod"
)

type PortInfo struct {
	HostIp   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type Machine struct {
	ID                 int                   `json:"id"`
	MachineID          int                   `json:"machine_id"`
	PublicIP           string                `json:"public_ipaddr"`
	ActualStatus       string                `json:"actual_status"`
	StatusMessage      string                `json:"status_msg"`
	Label              string                `json:"label"`
	Ports              map[string][]PortInfo `json:"ports"`
	GpuName            string                `json:"gpu_name"`
	GpuVRAM            float64               `json:"gpu_totalram"`
	GpuTFLOPS          float64               `json:"total_flops"`
	GpuMemoryBandwidth float64               `json:"gpu_mem_bw"`
	CpuCores           float64               `json:"cpu_cores_effective"`
	CpuRam             float64               `json:"cpu_ram"`
	PricePerHr         float64               `json:"dph_total"`
	State              virtualpod.MachineState
}

func GenericMachineAdapter(vastAIMachine *Machine) *virtualpod.Machine {
	machine := &virtualpod.Machine{}
	machine.ID = strconv.Itoa(vastAIMachine.ID)
	machine.MachineID = strconv.Itoa(vastAIMachine.MachineID)
	machine.PublicIP = vastAIMachine.PublicIP

	machine.States.GpuName = vastAIMachine.GpuName
	machine.States.GpuVRAM = vastAIMachine.GpuVRAM
	machine.States.GpuTFLOPS = vastAIMachine.GpuTFLOPS
	machine.States.GpuMemoryBandwidth = vastAIMachine.GpuMemoryBandwidth
	machine.States.CpuCores = vastAIMachine.CpuCores
	machine.States.CpuRam = vastAIMachine.CpuRam
	machine.States.PricePerHr = vastAIMachine.PricePerHr

	if vastAIMachine.ActualStatus == "running" {
		machine.State = virtualpod.MachineStateRunning
	} else if vastAIMachine.ActualStatus == "created" && strings.HasPrefix(vastAIMachine.StatusMessage, "Error") {
		machine.State = virtualpod.MachineStateFailed
	} else {
		machine.State = virtualpod.MachineStatePending
	}

	return machine
}

// } else if (vastAIMachine.ActualStatus == "created" || vastAIMachine.ActualStatus == "loading") && strings.HasPrefix(vastAIMachine.StatusMessage, "Error") {

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

	CPUName  string  `json:"cpu_name"`
	CPUCores float64 `json:"cpu_cores_effective"`
	CPURam   int     `json:"cpu_ram"`

	DiskSpace    float64 `json:"disk_space"`
	Verification string  `json:"verification"`
}

type BundleOffers struct {
	Offers []BundleOffer `json:"offers"`
}

// buildInstanceFilters builds VastAI API filters from MachineSpecification
func buildInstanceFilters(s virtualpod.MachineSpecification) map[string]interface{} {
	type FilterOp = map[string]interface{}

	filters := map[string]interface{}{
		"rentable": FilterOp{"eq": true},
		"rented":   FilterOp{"eq": false},
		"order":    [][]string{{"dph_total", "asc"}},
	}

	// Bool fields
	if s.VastAI.VerifiedOnly {
		filters["verified"] = FilterOp{"eq": true}
	}
	if s.VastAI.DatacenterOnly {
		filters["datacenter"] = FilterOp{"eq": true}
	}

	// Region filtering
	allowedCountries := getAllowedCountries(s.Regions)
	if len(allowedCountries) > 0 {
		filters["geolocation"] = FilterOp{"in": allowedCountries}
	}

	// GPU names list
	if len(s.GPUNames) > 0 {
		filters["gpu_name"] = FilterOp{"in": s.GPUNames}
	}

	// Compute capability list
	if len(s.ComputeCap) > 0 {
		filters["compute_cap"] = FilterOp{"in": s.ComputeCap}
	}

	// GPU Count (num_gpus)
	if s.GPUCount != nil {
		filters["num_gpus"] = FilterOp{"eq": *s.GPUCount}
	} else {
		if s.GPUCountMin != nil && s.GPUCountMax != nil {
			filters["num_gpus"] = FilterOp{"gte": *s.GPUCountMin, "lte": *s.GPUCountMax}
		} else if s.GPUCountMin != nil {
			filters["num_gpus"] = FilterOp{"gte": *s.GPUCountMin}
		} else if s.GPUCountMax != nil {
			filters["num_gpus"] = FilterOp{"lte": *s.GPUCountMax}
		}
	}

	// VRAM per GPU (gpu_ram) - in MB
	if s.VRAM != nil {
		filters["gpu_ram"] = FilterOp{"eq": *s.VRAM}
	} else {
		if s.VRAMMin != nil && s.VRAMMax != nil {
			filters["gpu_ram"] = FilterOp{"gte": *s.VRAMMin, "lte": *s.VRAMMax}
		} else if s.VRAMMin != nil {
			filters["gpu_ram"] = FilterOp{"gte": *s.VRAMMin}
		} else if s.VRAMMax != nil {
			filters["gpu_ram"] = FilterOp{"lte": *s.VRAMMax}
		}
	}

	// VRAM Total (gpu_total_ram) - in MB
	if s.VRAMTotal != nil {
		filters["gpu_total_ram"] = FilterOp{"eq": *s.VRAMTotal}
	} else {
		if s.VRAMTotalMin != nil && s.VRAMTotalMax != nil {
			filters["gpu_total_ram"] = FilterOp{"gte": *s.VRAMTotalMin, "lte": *s.VRAMTotalMax}
		} else if s.VRAMTotalMin != nil {
			filters["gpu_total_ram"] = FilterOp{"gte": *s.VRAMTotalMin}
		} else if s.VRAMTotalMax != nil {
			filters["gpu_total_ram"] = FilterOp{"lte": *s.VRAMTotalMax}
		}
	}

	// VRAM Bandwidth (gpu_mem_bw) - in GB/s
	if s.VRAMBandwidth != nil {
		filters["gpu_mem_bw"] = FilterOp{"eq": *s.VRAMBandwidth}
	} else {
		if s.VRAMBandwidthMin != nil && s.VRAMBandwidthMax != nil {
			filters["gpu_mem_bw"] = FilterOp{"gte": *s.VRAMBandwidthMin, "lte": *s.VRAMBandwidthMax}
		} else if s.VRAMBandwidthMin != nil {
			filters["gpu_mem_bw"] = FilterOp{"gte": *s.VRAMBandwidthMin}
		} else if s.VRAMBandwidthMax != nil {
			filters["gpu_mem_bw"] = FilterOp{"lte": *s.VRAMBandwidthMax}
		}
	}

	// TFLOPS (total_flops)
	if s.TFLOPS != nil {
		filters["total_flops"] = FilterOp{"eq": *s.TFLOPS}
	} else {
		if s.TFLOPSMin != nil && s.TFLOPSMax != nil {
			filters["total_flops"] = FilterOp{"gte": *s.TFLOPSMin, "lte": *s.TFLOPSMax}
		} else if s.TFLOPSMin != nil {
			filters["total_flops"] = FilterOp{"gte": *s.TFLOPSMin}
		} else if s.TFLOPSMax != nil {
			filters["total_flops"] = FilterOp{"lte": *s.TFLOPSMax}
		}
	}

	// CUDA version (cuda_max_good)
	if s.CUDA != nil {
		filters["cuda_max_good"] = FilterOp{"eq": *s.CUDA}
	} else {
		if s.CUDAMin != nil && s.CUDAMax != nil {
			filters["cuda_max_good"] = FilterOp{"gte": *s.CUDAMin, "lte": *s.CUDAMax}
		} else if s.CUDAMin != nil {
			filters["cuda_max_good"] = FilterOp{"gte": *s.CUDAMin}
		} else if s.CUDAMax != nil {
			filters["cuda_max_good"] = FilterOp{"lte": *s.CUDAMax}
		}
	}

	// CPU cores (cpu_cores_effective)
	if s.CPU != nil {
		filters["cpu_cores_effective"] = FilterOp{"eq": *s.CPU}
	} else {
		if s.CPUMin != nil && s.CPUMax != nil {
			filters["cpu_cores_effective"] = FilterOp{"gte": *s.CPUMin, "lte": *s.CPUMax}
		} else if s.CPUMin != nil {
			filters["cpu_cores_effective"] = FilterOp{"gte": *s.CPUMin}
		} else if s.CPUMax != nil {
			filters["cpu_cores_effective"] = FilterOp{"lte": *s.CPUMax}
		}
	}

	// RAM (cpu_ram) - in MB
	if s.RAM != nil {
		filters["cpu_ram"] = FilterOp{"eq": *s.RAM}
	} else {
		if s.RAMMin != nil && s.RAMMax != nil {
			filters["cpu_ram"] = FilterOp{"gte": *s.RAMMin, "lte": *s.RAMMax}
		} else if s.RAMMin != nil {
			filters["cpu_ram"] = FilterOp{"gte": *s.RAMMin}
		} else if s.RAMMax != nil {
			filters["cpu_ram"] = FilterOp{"lte": *s.RAMMax}
		}
	}

	// Price (dph_total) - dollars per hour
	if s.Price != nil {
		filters["dph_total"] = FilterOp{"eq": *s.Price}
	} else {
		if s.PriceMin != nil && s.PriceMax != nil {
			filters["dph_total"] = FilterOp{"gte": *s.PriceMin, "lte": *s.PriceMax}
		} else if s.PriceMin != nil {
			filters["dph_total"] = FilterOp{"gte": *s.PriceMin}
		} else if s.PriceMax != nil {
			filters["dph_total"] = FilterOp{"lte": *s.PriceMax}
		}
	}

	// VastAI DLPerf (dlperf)
	if s.VastAI.DLPerf != nil {
		filters["dlperf"] = FilterOp{"eq": *s.VastAI.DLPerf}
	} else {
		if s.VastAI.DLPerfMin != nil && s.VastAI.DLPerfMax != nil {
			filters["dlperf"] = FilterOp{"gte": *s.VastAI.DLPerfMin, "lte": *s.VastAI.DLPerfMax}
		} else if s.VastAI.DLPerfMin != nil {
			filters["dlperf"] = FilterOp{"gte": *s.VastAI.DLPerfMin}
		} else if s.VastAI.DLPerfMax != nil {
			filters["dlperf"] = FilterOp{"lte": *s.VastAI.DLPerfMax}
		}
	}

	// Upload Speed (inet_up) - in Mbps
	if s.UploadSpeed != nil {
		filters["inet_up"] = FilterOp{"eq": *s.UploadSpeed}
	} else {
		if s.UploadSpeedMin != nil && s.UploadSpeedMax != nil {
			filters["inet_up"] = FilterOp{"gte": *s.UploadSpeedMin, "lte": *s.UploadSpeedMax}
		} else if s.UploadSpeedMin != nil {
			filters["inet_up"] = FilterOp{"gte": *s.UploadSpeedMin}
		} else if s.UploadSpeedMax != nil {
			filters["inet_up"] = FilterOp{"lte": *s.UploadSpeedMax}
		}
	}

	// Download Speed (inet_down) - in Mbps
	if s.DownloadSpeed != nil {
		filters["inet_down"] = FilterOp{"eq": *s.DownloadSpeed}
	} else {
		if s.DownloadSpeedMin != nil && s.DownloadSpeedMax != nil {
			filters["inet_down"] = FilterOp{"gte": *s.DownloadSpeedMin, "lte": *s.DownloadSpeedMax}
		} else if s.DownloadSpeedMin != nil {
			filters["inet_down"] = FilterOp{"gte": *s.DownloadSpeedMin}
		} else if s.DownloadSpeedMax != nil {
			filters["inet_down"] = FilterOp{"lte": *s.DownloadSpeedMax}
		}
	}

	return filters
}
