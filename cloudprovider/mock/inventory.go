package mock

import (
	"strconv"
	"strings"

	"github.com/anex-sh/anex/virtualpod"
)

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

var inventory = []BundleOffer{
	{
		ID:           1001,
		MachineID:    1001,
		DphTotal:     0.22,
		Geolocation:  "CZ",
		GPUName:      "RTX 3090",
		NumGPUs:      1,
		GPURam:       24576,
		GPUTotalRam:  24576,
		CudaMaxGood:  12.2,
		CPUName:      "AMD EPYC 7402",
		CPUCores:     8,
		CPURam:       65536,
		DiskSpace:    200,
		Verification: "verified",
	},
	{
		ID:           1002,
		MachineID:    1002,
		DphTotal:     0.38,
		Geolocation:  "DE",
		GPUName:      "RTX 4090",
		NumGPUs:      1,
		GPURam:       24576,
		GPUTotalRam:  24576,
		CudaMaxGood:  12.4,
		CPUName:      "Intel Xeon Gold 6338",
		CPUCores:     12,
		CPURam:       131072,
		DiskSpace:    500,
		Verification: "verified",
	},
	{
		ID:           1003,
		MachineID:    1003,
		DphTotal:     0.74,
		Geolocation:  "US",
		GPUName:      "RTX 4090",
		NumGPUs:      2,
		GPURam:       24576,
		GPUTotalRam:  49152,
		CudaMaxGood:  12.4,
		CPUName:      "AMD EPYC 7763",
		CPUCores:     24,
		CPURam:       262144,
		DiskSpace:    1000,
		Verification: "verified",
	},
	{
		ID:           1004,
		MachineID:    1004,
		DphTotal:     1.45,
		Geolocation:  "US",
		GPUName:      "A100 SXM4",
		NumGPUs:      1,
		GPURam:       81920,
		GPUTotalRam:  81920,
		CudaMaxGood:  12.4,
		CPUName:      "AMD EPYC 7543",
		CPUCores:     16,
		CPURam:       262144,
		DiskSpace:    1000,
		Verification: "verified",
	},
	{
		ID:           1005,
		MachineID:    1005,
		DphTotal:     2.95,
		Geolocation:  "NL",
		GPUName:      "H100 SXM5",
		NumGPUs:      1,
		GPURam:       81920,
		GPUTotalRam:  81920,
		CudaMaxGood:  12.6,
		CPUName:      "Intel Xeon Platinum 8480+",
		CPUCores:     20,
		CPURam:       524288,
		DiskSpace:    2000,
		Verification: "verified",
	},
}

func lookupOffer(offerID string) *BundleOffer {
	for i := range inventory {
		if strconv.Itoa(inventory[i].ID) == offerID {
			return &inventory[i]
		}
	}
	return nil
}

func matchesSpec(o BundleOffer, s virtualpod.MachineSpecification) bool {
	if len(s.GPUNames) > 0 {
		ok := false
		for _, name := range s.GPUNames {
			if strings.EqualFold(o.GPUName, name) || strings.Contains(strings.ToLower(o.GPUName), strings.ToLower(name)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if s.GPUCount != nil && o.NumGPUs != *s.GPUCount {
		return false
	}
	if s.GPUCountMin != nil && o.NumGPUs < *s.GPUCountMin {
		return false
	}
	if s.GPUCountMax != nil && o.NumGPUs > *s.GPUCountMax {
		return false
	}
	if s.VRAM != nil && o.GPURam != *s.VRAM {
		return false
	}
	if s.VRAMMin != nil && o.GPURam < *s.VRAMMin {
		return false
	}
	if s.VRAMMax != nil && o.GPURam > *s.VRAMMax {
		return false
	}
	if s.PriceMax != nil && o.DphTotal > *s.PriceMax {
		return false
	}
	return true
}
