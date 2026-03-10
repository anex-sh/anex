package runpod

import (
	"fmt"
	"sort"
	"strings"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

// GPU prices per hour (whole machine, 1 GPU) by cloud type.
// Source: RunPod pricing as of 2025. Updated manually.
var gpuPrices = map[string]map[string]float64{
	"SECURE": {
		"NVIDIA H200":              3.59,
		"NVIDIA B200":              4.99,
		"NVIDIA RTX Pro 6000":      1.89,
		"NVIDIA H100 NVL":          3.07,
		"NVIDIA H100 80GB HBM3":    2.39,
		"NVIDIA H100 SXM":          2.69,
		"NVIDIA A100 80GB PCIe":    1.39,
		"NVIDIA A100-SXM4-80GB":    1.49,
		"NVIDIA L40S":              0.86,
		"NVIDIA RTX 6000 Ada Generation": 0.77,
		"NVIDIA A40":               0.40,
		"NVIDIA L40":               0.99,
		"NVIDIA RTX A6000":         0.49,
		"NVIDIA GeForce RTX 5090":  0.89,
		"NVIDIA L4":                0.39,
		"NVIDIA GeForce RTX 3090":  0.46,
		"NVIDIA GeForce RTX 4090":  0.59,
		"NVIDIA RTX A5000":         0.27,
	},
	"COMMUNITY": {
		"NVIDIA H200":              3.59,
		"NVIDIA B200":              4.99,
		"NVIDIA RTX Pro 6000":      1.89,
		"NVIDIA H100 NVL":          3.07,
		"NVIDIA H100 80GB HBM3":    2.39,
		"NVIDIA H100 SXM":          2.69,
		"NVIDIA A100 80GB PCIe":    1.39,
		"NVIDIA A100-SXM4-80GB":    1.49,
		"NVIDIA L40S":              0.86,
		"NVIDIA RTX 6000 Ada Generation": 0.77,
		"NVIDIA A40":               0.40,
		"NVIDIA L40":               0.99,
		"NVIDIA RTX A6000":         0.49,
		"NVIDIA GeForce RTX 5090":  0.89,
		"NVIDIA L4":                0.39,
		"NVIDIA GeForce RTX 3090":  0.46,
		"NVIDIA GeForce RTX 4090":  0.59,
		"NVIDIA RTX A5000":         0.27,
	},
}

// knownGPUNames is the set of valid GPU type IDs for RunPod.
var knownGPUNames map[string]bool

func init() {
	knownGPUNames = make(map[string]bool)
	for _, tier := range gpuPrices {
		for name := range tier {
			knownGPUNames[name] = true
		}
	}
}

// Known CUDA versions available on RunPod (from their allowedCudaVersions enum).
var knownCUDAVersions = []string{
	"11.1", "11.2", "11.3", "11.4", "11.5", "11.6", "11.7", "11.8",
	"12.0", "12.1", "12.2", "12.3", "12.4", "12.5", "12.6", "12.7", "12.8",
}

// validateGPUNames filters gpu names against the known RunPod set.
// Unknown names are dropped with a warning message returned.
func validateGPUNames(names []string) (valid []string, warnings []string) {
	for _, name := range names {
		if knownGPUNames[name] {
			valid = append(valid, name)
		} else {
			warnings = append(warnings, fmt.Sprintf("unknown RunPod GPU type %q, skipping", name))
		}
	}
	return
}

// filterGPUsByPrice returns only GPUs whose per-machine price (for gpuCount GPUs)
// falls within [priceMin, priceMax]. Price lookup uses the hardcoded dict.
func filterGPUsByPrice(gpuNames []string, cloudType string, gpuCount int, priceMin, priceMax *float64) []string {
	prices, ok := gpuPrices[cloudType]
	if !ok {
		prices = gpuPrices["SECURE"]
	}

	var result []string
	for _, name := range gpuNames {
		perGPU, ok := prices[name]
		if !ok {
			continue
		}
		totalPrice := perGPU * float64(gpuCount)
		if priceMin != nil && totalPrice < *priceMin {
			continue
		}
		if priceMax != nil && totalPrice > *priceMax {
			continue
		}
		result = append(result, name)
	}
	return result
}

// filterCUDAVersions returns CUDA version strings within [min, max].
func filterCUDAVersions(cudaMin, cudaMax *float64) []string {
	if cudaMin == nil && cudaMax == nil {
		return nil
	}
	var result []string
	for _, v := range knownCUDAVersions {
		var fv float64
		fmt.Sscanf(v, "%f", &fv)
		if cudaMin != nil && fv < *cudaMin {
			continue
		}
		if cudaMax != nil && fv > *cudaMax {
			continue
		}
		result = append(result, v)
	}
	return result
}

// BuildProvisionQuery translates a MachineSpecification into a RunPod REST API
// pod creation payload (POST /v1/pods). Returns the payload map and any warnings.
func BuildProvisionQuery(spec virtualpod.MachineSpecification) (map[string]interface{}, []string) {
	var warnings []string
	query := make(map[string]interface{})

	// Cloud type
	cloudType := spec.RunPod.CloudType
	if cloudType == "" {
		cloudType = "SECURE"
	}
	query["cloudType"] = cloudType

	if strings.ToUpper(cloudType) == "COMMUNITY" {
		query["supportPublicIp"] = true
	}

	// GPU count (default 1)
	gpuCount := 1
	if spec.GPUCount != nil {
		gpuCount = *spec.GPUCount
	}
	query["gpuCount"] = gpuCount

	// GPU names: validate, then filter by price
	gpuNames := spec.GPUNames
	if len(gpuNames) == 0 {
		// No filter: use all known GPUs
		for name := range knownGPUNames {
			gpuNames = append(gpuNames, name)
		}
		sort.Strings(gpuNames)
	} else {
		var w []string
		gpuNames, w = validateGPUNames(gpuNames)
		warnings = append(warnings, w...)
	}

	// Price filtering via local lookup
	if spec.PriceMin != nil || spec.PriceMax != nil {
		gpuNames = filterGPUsByPrice(gpuNames, cloudType, gpuCount, spec.PriceMin, spec.PriceMax)
	}

	if len(gpuNames) > 0 {
		query["gpuTypeIds"] = gpuNames
	}

	// GPU type priority
	if spec.RunPod.KeepGPUTypePriority {
		query["gpuTypePriority"] = "custom"
	}

	// CUDA filtering
	cudaVersions := filterCUDAVersions(spec.CUDAMin, spec.CUDAMax)
	if len(cudaVersions) > 0 {
		query["allowedCudaVersions"] = cudaVersions
	}

	// RAM: annotation is total MB, API wants per-GPU in GB
	if spec.RAMMin != nil {
		ramPerGPU := *spec.RAMMin / 1024 / gpuCount
		if ramPerGPU < 1 {
			ramPerGPU = 1
		}
		query["minRAMPerGPU"] = ramPerGPU
	}

	// vCPU: annotation is total cores, API wants per-GPU
	if spec.CPUMin != nil {
		vcpuPerGPU := *spec.CPUMin / gpuCount
		if vcpuPerGPU < 1 {
			vcpuPerGPU = 1
		}
		query["minVCPUPerGPU"] = vcpuPerGPU
	}

	// Container disk
	if spec.ContainerDiskInGB != nil {
		query["containerDiskInGb"] = *spec.ContainerDiskInGB
	}

	// Network speed
	if spec.DownloadSpeedMin != nil {
		query["minDownloadMbps"] = *spec.DownloadSpeedMin
	}
	if spec.UploadSpeedMin != nil {
		query["minUploadMbps"] = *spec.UploadSpeedMin
	}

	// Disk bandwidth
	if spec.DiskBW != nil {
		query["minDiskBandwidthMBps"] = *spec.DiskBW
	}

	// Data center IDs
	if len(spec.RunPod.DataCenterIds) > 0 {
		query["dataCenterIds"] = spec.RunPod.DataCenterIds
	}

	// VRAM per GPU: annotation is MB, RunPod doesn't have a direct filter for this
	// but we can use it to further filter gpuTypeIds if needed in the future
	if spec.VRAMMin != nil {
		warnings = append(warnings, "RunPod API does not support vRAM filtering directly; use gpu-names instead")
	}

	return query, warnings
}
