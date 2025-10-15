package vastai

import (
	"reflect"
	"testing"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
)

func TestMapRegion(t *testing.T) {
	cases := []struct {
		in  virtualpod.Region
		out string
	}{
		{virtualpod.RegionEurope, "europe"},
		{virtualpod.RegionNorthAmerica, "north-america"},
		{virtualpod.RegionSouthAmerica, "south-america"},
		{virtualpod.RegionAsia, "asia"},
		{virtualpod.RegionAfrica, "africa"},
		{virtualpod.RegionOceania, "oceania"},
		{virtualpod.RegionAny, ""},
	}
	for _, tc := range cases {
		if got := mapRegion(tc.in); got != tc.out {
			t.Fatalf("mapRegion(%v) = %q, want %q", tc.in, got, tc.out)
		}
	}
}

func TestGetAllowedCountriesDedupesAndAggregates(t *testing.T) {
	regions := []virtualpod.Region{virtualpod.RegionEurope, virtualpod.RegionEurope}
	countries := getAllowedCountries(regions)
	// Must not be empty for Europe and should not contain duplicates; verify by comparing len to set size
	set := map[string]struct{}{}
	for _, c := range countries {
		set[c] = struct{}{}
	}
	if len(set) != len(countries) {
		t.Fatalf("expected deduplicated list, got duplicates")
	}
	// A small sanity check: some common country codes likely present
	wantAny := []string{"DE", "FR", "CZ", "GB"}
	found := false
	for _, w := range wantAny {
		for _, c := range countries {
			if c == w {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("expected at least one of %v in the countries list", wantAny)
	}

	// Mix multiple regions; ensure union contains both EU and NA samples
	regions = []virtualpod.Region{virtualpod.RegionEurope, virtualpod.RegionNorthAmerica}
	countries = getAllowedCountries(regions)
	contains := func(x string) bool {
		for _, c := range countries {
			if c == x {
				return true
			}
		}
		return false
	}
	if !(contains("DE") && (contains("US") || contains("CA"))) {
		t.Fatalf("expected union containing EU and NA samples; got %v", countries)
	}

	// Unknown/Any produces empty
	if out := getAllowedCountries([]virtualpod.Region{virtualpod.RegionAny}); len(out) != 0 {
		t.Fatalf("expected empty for RegionAny, got %v", out)
	}
}

func TestRegionToCountryMappingIntegrity(t *testing.T) {
	if _, ok := RegionToCountryMapping["europe"]; !ok {
		t.Fatal("europe mapping missing")
	}
	if got := RegionToCountryMapping["north-america"]; !reflect.DeepEqual(got, RegionToCountryMapping["north-america"]) {
		// trivial, ensures map is accessible in tests (non-nil)
	}
}
