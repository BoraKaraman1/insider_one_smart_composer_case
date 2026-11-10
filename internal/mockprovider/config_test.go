package mockprovider_test

import (
	"testing"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
)

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"MOCKPROVIDER_SEED": "7", "MOCKPROVIDER_LATENCY": "20ms", "MOCKPROVIDER_RETRY_AFTER": "2s",
		"MOCKPROVIDER_RATE_500": "0.1", "MOCKPROVIDER_RATE_503": "0.1", "MOCKPROVIDER_RATE_429": "0.05",
		"MOCKPROVIDER_RATE_429_NO_RETRY_AFTER": "0.01", "MOCKPROVIDER_RATE_HANG": "0.02",
		"MOCKPROVIDER_RATE_DROP": "0.03", "MOCKPROVIDER_RATE_LATE": "0.04",
	}
	c, err := mockprovider.ConfigFromEnv(func(k string) string { return env[k] })
	want := mockprovider.Config{Seed: 7, Latency: 20 * time.Millisecond, RetryAfter: 2 * time.Second,
		Rate500: 0.1, Rate503: 0.1, Rate429: 0.05, Rate429NoRetryAfter: 0.01, RateHang: 0.02, RateDrop: 0.03, RateLate: 0.04}
	if err != nil || c != want {
		t.Errorf("config %+v, %v; want %+v", c, err, want)
	}
	if c.ChaosHeader {
		t.Error("the X-Chaos hook is on without MOCKPROVIDER_CHAOS_HEADER=true")
	}
	for k, v := range map[string]string{
		"MOCKPROVIDER_RATE_500": "1.5", "MOCKPROVIDER_RATE_DROP": "x", "MOCKPROVIDER_LATENCY": "fast", "MOCKPROVIDER_SEED": "-1",
	} {
		if _, err := mockprovider.ConfigFromEnv(func(key string) string { return map[string]string{k: v}[key] }); err == nil {
			t.Errorf("%s=%s accepted", k, v)
		}
	}
	over := map[string]string{"MOCKPROVIDER_RATE_500": "0.6", "MOCKPROVIDER_RATE_503": "0.6"}
	if _, err := mockprovider.ConfigFromEnv(func(k string) string { return over[k] }); err == nil {
		t.Error("rates adding up to 1.2 accepted")
	}
}
