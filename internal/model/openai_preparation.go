package model

import (
	"net/url"
	"strconv"
)

const OpenAIOfficialBaseURL = "https://api.openai.com/v1"

// OpenAIPreparationEndpointPolicy classifies metadata only. A loopback result
// still requires constructor ownership before it can be used by the router.
func OpenAIPreparationEndpointPolicy(baseURL string) string {
	if baseURL == OpenAIOfficialBaseURL {
		return "exact_official"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/v1" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "invalid"
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || baseURL != "http://127.0.0.1:"+strconv.Itoa(port)+"/v1" {
		return "invalid"
	}
	return "owned_simulation_required"
}
