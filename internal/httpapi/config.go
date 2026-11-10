// Package httpapi is the HTTP API: auth, request ids, the audience count
// endpoint, and health checks.
package httpapi

import (
	"errors"
	"strings"
)

// Config is read from the environment at startup.
type Config struct {
	// AdminToken is the bearer token every /api request must carry.
	AdminToken string
	// AllowUnauthenticated lets the API run without a token. Dev only, and
	// only when AdminToken is empty; a set token is always enforced.
	AllowUnauthenticated bool
	ListenAddr           string
	// CampaignURLHosts are the hosts a campaign_url may point at. Empty
	// means no campaign can be created.
	CampaignURLHosts []string
}

// ErrNoToken is returned when neither a token nor the dev escape hatch is set.
var ErrNoToken = errors.New("ADMIN_TOKEN is not set; set it, or set ALLOW_UNAUTHENTICATED=true for local development only")

// ConfigFromEnv reads ADMIN_TOKEN, ALLOW_UNAUTHENTICATED, LISTEN_ADDR and
// CAMPAIGN_URL_HOSTS (comma separated). It
// fails closed: no token and no explicit opt-out is an error, not an open API.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		AdminToken:           getenv("ADMIN_TOKEN"),
		AllowUnauthenticated: getenv("ALLOW_UNAUTHENTICATED") == "true",
		ListenAddr:           getenv("LISTEN_ADDR"),
	}
	for _, h := range strings.Split(getenv("CAMPAIGN_URL_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			c.CampaignURLHosts = append(c.CampaignURLHosts, h)
		}
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.AdminToken == "" && !c.AllowUnauthenticated {
		return Config{}, ErrNoToken
	}
	return c, nil
}
