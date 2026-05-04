package vastai

import (
	"strings"
	"testing"

	"github.com/anex-sh/anex/virtualpod"
)

func TestBuildMachineLabel(t *testing.T) {
	c := NewClient("http://example", "apikey", "cluster123", "node-a", URLConfig{}, BansConfig{})
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

func TestBuildInstanceFiltersOptionalFieldsOmitted(t *testing.T) {
	spec := virtualpod.MachineSpecification{}
	filters := buildInstanceFilters(spec)
	if _, ok := filters["gpu_ram"]; ok {
		t.Fatalf("gpu_ram should be omitted")
	}
	// These are commented out in buildInstanceFilters, so they should be omitted
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
	// order field should always be present
	if _, ok := filters["order"]; !ok {
		t.Fatalf("order field should always be present")
	}
}
