package httpapi

import (
	"context"
	"net/http"
	"testing"
)

func TestCancelCancelsGenerateJob(t *testing.T) {
	h, p := generateAPI(t)
	f := insertCampaign(t, p, "draft", 0)
	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/generate", versionBody(0), bearer); rec.Code != http.StatusAccepted {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/cancel", versionBody(1), bearer); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	var state string
	if err := p.QueryRow(context.Background(), `SELECT state::text FROM river_job WHERE kind = 'generate'
		AND args->>'campaign_id' = $1`, f.ID).Scan(&state); err != nil || state != "cancelled" {
		t.Errorf("job state %q (%v), want cancelled", state, err)
	}
}
