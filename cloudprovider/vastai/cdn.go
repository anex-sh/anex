package vastai

func resolveCDNURL(custom, defaultURL, token string) string {
	if custom != "" {
		return custom
	}
	if token == "" {
		return defaultURL
	}
	return defaultURL + "?token=" + token
}
