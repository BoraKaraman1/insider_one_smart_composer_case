// Paket gelen istekleri karşılar.
package httpapi

import (
	"errors"
	"strings"
)

type Config struct {
	AdminToken string
	// Dolu anahtar varken doğrulamasız çalışma yok sayılır.
	AllowUnauthenticated bool
	ListenAddr           string
	// Boşsa kampanya oluşturulamaz.
	CampaignURLHosts []string
}

var ErrNoToken = errors.New("ADMIN_TOKEN is not set; set it, or set ALLOW_UNAUTHENTICATED=true for local development only")

// Anahtar da kaçış da yoksa servis açık kalmaz.
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
