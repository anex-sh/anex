package vastai

import "github.com/anex-sh/anex/virtualpod"

var RegionToCountryMapping = map[string][]string{
	"europe": {
		"AD", "AL", "AT", "BA", "BE", "BG", "CH", "CY", "CZ",
		"DE", "DK", "EE", "ES", "FI", "FR", "GB", "GR", "HR",
		"HU", "IE", "IS", "IT", "LI", "LT", "LU", "LV", "MC",
		"MD", "ME", "MK", "MT", "NL", "NO", "PL", "PT", "RO",
		"RS", "SE", "SI", "SK", "SM", "TR", "VA", "XK",
	},
	"north-america": {"US", "CA", "MX"},
	"asia": {
		"AF", "AM", "AZ", "BD", "BH", "BN", "BT", "CN", "GE", "HK",
		"ID", "IN", "IQ", "IR", "JO", "JP", "KG", "KH", "KP", "KR",
		"KW", "KZ", "LA", "LB", "LK", "MM", "MN", "MO", "MV", "MY",
		"NP", "OM", "PH", "PK", "PS", "QA", "RU", "SA", "SG", "SY",
		"TH", "TJ", "TM", "TW", "UZ", "VN", "YE",
	},
	"africa": {
		"AO", "BF", "BI", "BJ", "BW", "CD", "CF", "CG", "CI", "CM",
		"CV", "DJ", "DZ", "EG", "EH", "ER", "ET", "GA", "GH", "GM",
		"GN", "GQ", "GW", "KE", "KM", "LR", "LS", "LY", "MA", "MG",
		"ML", "MR", "MU", "MW", "MZ", "NA", "NE", "NG", "RW", "SC",
		"SD", "SL", "SN", "SO", "SS", "ST", "SZ", "TD", "TG", "TN",
		"TZ", "UG", "ZA", "ZM", "ZW",
	},
	"south-america": {"AR", "BO", "BR", "CL", "CO", "EC", "FK", "GF", "GY", "PE", "PY", "SR", "UY", "VE"},
	"oceania": {
		"AS", "AU", "CK", "FJ", "FM", "GU", "KI", "MH", "MP", "NC",
		"NF", "NR", "NU", "NZ", "PF", "PG", "PN", "PW", "SB", "TK",
		"TO", "TV", "VU", "WF", "WS",
	},
}

func getAllowedCountries(regions []virtualpod.Region) []string {
	var countries []string
	countriesMap := make(map[string]bool)

	for _, region := range regions {
		mappedRegion := mapRegion(region)
		if mappedRegion != "" {
			for _, country := range RegionToCountryMapping[mappedRegion] {
				if !countriesMap[country] {
					countriesMap[country] = true
					countries = append(countries, country)
				}
			}
		}
	}

	return countries
}

func mapRegion(region virtualpod.Region) string {
	switch region {
	case virtualpod.RegionEurope:
		return "europe"
	case virtualpod.RegionNorthAmerica:
		return "north-america"
	case virtualpod.RegionSouthAmerica:
		return "south-america"
	case virtualpod.RegionAsia:
		return "asia"
	case virtualpod.RegionAfrica:
		return "africa"
	case virtualpod.RegionOceania:
		return "oceania"
	default:
		return ""
	}
}
