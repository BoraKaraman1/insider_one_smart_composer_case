package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func pool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestCountExample(t *testing.T) {
	url, meta := testdb.Seeded(t)
	h := handler(t, &Server{Config: Config{AdminToken: token}, Pool: pool(t, url), Now: func() time.Time { return meta.AsOf }})
	rule, err := os.ReadFile("../../docs/examples/istanbul_cart_abandoners.json")
	if err != nil {
		t.Fatal(err)
	}
	rec := do(h, "POST", "/api/audience/count", `{"rule":`+string(rule)+`}`, bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Total         int        `json:"total"`
		SMSEligible   int        `json:"sms_eligible"`
		EmailEligible int        `json:"email_eligible"`
		AsOf          time.Time  `json:"as_of"`
		SeededAt      *time.Time `json:"seeded_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 315 || got.SMSEligible != 210 || got.EmailEligible <= 0 || got.EmailEligible > got.Total {
		t.Errorf("counts %+v; docs/seed-data.md says 315 total, 210 SMS", got)
	}
	if !got.AsOf.Equal(meta.AsOf) || got.SeededAt == nil || !got.SeededAt.Equal(meta.AsOf) {
		t.Errorf("as_of %v, seeded_at %v; want both %v", got.AsOf, got.SeededAt, meta.AsOf)
	}
}

func TestReadyz(t *testing.T) {
	url, _ := testdb.Seeded(t)
	ready := handler(t, &Server{Config: Config{AdminToken: token}, Pool: pool(t, url)})
	if rec := do(ready, "GET", "/readyz", "", nil); rec.Code != http.StatusOK {
		t.Errorf("migrated database: %d %s", rec.Code, rec.Body)
	}
	unmigrated := handler(t, &Server{Config: Config{AdminToken: token}, Pool: pool(t, testdb.Fresh(t))})
	rec := do(unmigrated, "GET", "/readyz", "", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "migrations pending") {
		t.Errorf("unmigrated database: %d %s", rec.Code, rec.Body)
	}
}
