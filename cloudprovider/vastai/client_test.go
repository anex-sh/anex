package vastai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestListMachinesInternalPaginatesV1(t *testing.T) {
	page := func(next string, ids ...int) map[string]interface{} {
		var instances []map[string]interface{}
		for _, id := range ids {
			instances = append(instances, map[string]interface{}{
				"id":    id,
				"label": fmt.Sprintf("vk:cluster123:node-a:uid-%d", id),
			})
		}
		// next_token is null on the last page
		body := map[string]interface{}{"success": true, "instances": instances}
		if next != "" {
			body["next_token"] = next
		} else {
			body["next_token"] = nil
		}
		return body
	}

	var requests []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.String())
		if r.URL.Path != "/api/v1/instances/" {
			http.Error(w, `{"success":false,"msg":"Not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("after_token") {
		case "":
			json.NewEncoder(w).Encode(page("tok1", 1, 2))
		case "tok1":
			// foreign machine on the second page must be filtered out
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"instances": []map[string]interface{}{
					{"id": 3, "label": "vk:cluster123:node-a:uid-3"},
					{"id": 4, "label": "vk:other-cluster:node-a:uid-4"},
				},
				"next_token": nil,
			})
		default:
			t.Errorf("unexpected after_token: %s", r.URL.RawQuery)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL+"/api/v0", "apikey", "cluster123", "node-a", URLConfig{}, BansConfig{})
	machines, err := c.listMachinesInternal(context.Background())
	if err != nil {
		t.Fatalf("listMachinesInternal: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("expected 2 paginated requests, got %d: %v", len(requests), requests)
	}
	if len(machines) != 3 {
		t.Fatalf("expected 3 machines after filtering, got %d", len(machines))
	}
	for i, wantID := range []int{1, 2, 3} {
		if machines[i].ID != wantID {
			t.Fatalf("machine %d: expected ID %d, got %d", i, wantID, machines[i].ID)
		}
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
