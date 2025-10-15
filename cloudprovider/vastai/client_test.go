package vastai

import (
	"reflect"
	"strings"
	"testing"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

func TestBuildMachineLabel(t *testing.T) {
	c := NewClient("http://example", "apikey", "cluster123", "node-a")
	label := c.buildMachineLabel("pod-uid-1")
	if label != "vk:cluster123:node-a:pod-uid-1" {
		t.Fatalf("unexpected label: %s", label)
	}
	// empty pod uid allowed for prefix checks
	if p := c.buildMachineLabel(""); !strings.HasPrefix("vk:cluster123:node-a:", p) {
		t.Fatalf("expected prefix with empty pod uid, got %s", p)
	}
}

func TestSortCandidatesByPriceAscending(t *testing.T) {
	cands := []BundleOffer{{ID: 1, DphTotal: 0.5}, {ID: 2, DphTotal: 0.1}, {ID: 3, DphTotal: 0.3}}
	out := sortCandidates(cands)
	if out[0].ID != 2 || out[1].ID != 3 || out[2].ID != 1 {
		t.Fatalf("unexpected order: %#v", out)
	}
}

func TestParseMachineLabel(t *testing.T) {
	lbl := "vk:clu:nod:uid"
	li := parseMachineLabel(lbl)
	if li == nil || li.Prefix != "vk" || li.ClusterUID != "clu" || li.NodeName != "nod" || li.PodUID != "uid" {
		t.Fatalf("unexpected parse: %+v", li)
	}
	// invalid cases
	if parseMachineLabel("vk:too:few") != nil {
		t.Fatal("expected nil for invalid format")
	}
	if parseMachineLabel("xx:a:b:c") != nil {
		t.Fatal("expected nil for wrong prefix")
	}
}

func TestBuildInstanceFiltersFromSpec(t *testing.T) {
	spec := virtualpod.MachineSpecification{
		GPUCount:        1, // currently hardcoded in builder to 1
		MemoryPerGPUMB:  20000,
		CudaAvailable:   11.2,
		CPUCores:        8,
		CPURamMB:        16000,
		DiskSpace:       200,
		MaxPricePerHour: 1.23,
		Regions:         []virtualpod.Region{virtualpod.RegionEurope, virtualpod.RegionNorthAmerica},
	}
	filters := buildInstanceFilters(spec)

	// basic flags
	wantBools := map[string]interface{}{
		"rentable":  map[string]interface{}{"eq": true},
		"rented":    map[string]interface{}{"eq": false},
		"external":  map[string]interface{}{"eq": false},
		"verified":  map[string]interface{}{"eq": true},
		"inet_down": map[string]interface{}{"gt": 600},
		"num_gpus":  map[string]interface{}{"eq": 1},
	}
	for k, v := range wantBools {
		if !reflect.DeepEqual(filters[k], v) {
			t.Fatalf("filter %s mismatch: got %#v want %#v", k, filters[k], v)
		}
	}

	// numeric thresholds
	if got := filters["gpu_ram"]; !reflect.DeepEqual(got, map[string]interface{}{"gte": 20000}) {
		t.Fatalf("gpu_ram filter mismatch: %#v", got)
	}
	if got := filters["cuda_max_good"]; !reflect.DeepEqual(got, map[string]interface{}{"gte": 11.2}) {
		t.Fatalf("cuda_max_good filter mismatch: %#v", got)
	}
	if got := filters["cpu_cores"]; !reflect.DeepEqual(got, map[string]interface{}{"gte": 8}) {
		t.Fatalf("cpu_cores filter mismatch: %#v", got)
	}
	if got := filters["cpu_ram"]; !reflect.DeepEqual(got, map[string]interface{}{"gte": 16000}) {
		t.Fatalf("cpu_ram filter mismatch: %#v", got)
	}
	if got := filters["disk_space"]; !reflect.DeepEqual(got, map[string]interface{}{"gte": 200}) {
		t.Fatalf("disk_space filter mismatch: %#v", got)
	}
	if got := filters["dph_total"]; !reflect.DeepEqual(got, map[string]interface{}{"lte": 1.23}) {
		t.Fatalf("dph_total filter mismatch: %#v", got)
	}

	// geolocation countries from regions must be non-empty and include EU/NA samples
	geo := filters["geolocation"]
	vals, ok := geo["in"].([]string)
	if !ok || len(vals) == 0 {
		t.Fatalf("geolocation 'in' missing or empty: %#v", geo)
	}
	contains := func(x string) bool {
		for _, v := range vals {
			if v == x {
				return true
			}
		}
		return false
	}
	if !(contains("DE") || contains("FR") || contains("US") || contains("CA")) {
		t.Fatalf("expected sample countries in geolocation: %v", vals)
	}
}

func TestBuildInstanceFiltersOptionalFieldsOmitted(t *testing.T) {
	spec := virtualpod.MachineSpecification{}
	filters := buildInstanceFilters(spec)
	if _, ok := filters["gpu_ram"]; ok {
		t.Fatalf("gpu_ram should be omitted")
	}
	if _, ok := filters["cuda_max_good"]; ok {
		t.Fatalf("cuda_max_good should be omitted")
	}
	if _, ok := filters["cpu_cores"]; ok {
		t.Fatalf("cpu_cores should be omitted")
	}
	if _, ok := filters["cpu_ram"]; ok {
		t.Fatalf("cpu_ram should be omitted")
	}
	if _, ok := filters["disk_space"]; ok {
		t.Fatalf("disk_space should be omitted")
	}
	if _, ok := filters["dph_total"]; ok {
		t.Fatalf("dph_total should be omitted")
	}
	// geolocation omitted when no regions
	if _, ok := filters["geolocation"]; ok {
		t.Fatalf("geolocation should be omitted")
	}
}
