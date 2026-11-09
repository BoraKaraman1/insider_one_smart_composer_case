// Package httpapi is the HTTP API: auth, request ids, the audience count
// endpoint, and health checks.
package httpapi

import "errors"

// Config is read from the environment at startup.
type Config struct {
	// AdminToken is the bearer token every /api request must carry.
	AdminToken string
	// AllowUnauthenticated lets the API run without a token. Dev only, and
	// only when AdminToken is empty; a set token is always enforced.
	AllowUnauthenticated bool
	ListenAddr           string
}

// ErrNoToken is returned when neither a token nor the dev escape hatch is set.
var ErrNoToken = errors.New("ADMIN_TOKEN is not set; set it, or set ALLOW_UNAUTHENTICATED=true for local development only")

// ConfigFromEnv reads ADMIN_TOKEN, ALLOW_UNAUTHENTICATED and LISTEN_ADDR. It
// fails closed: no token and no explicit opt-out is an error, not an open API.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		AdminToken:           getenv("ADMIN_TOKEN"),
		AllowUnauthenticated: getenv("ALLOW_UNAUTHENTICATED") == "true",
		ListenAddr:           getenv("LISTEN_ADDR"),
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.AdminToken == "" && !c.AllowUnauthenticated {
		return Config{}, ErrNoToken
	}
	return c, nil
}
