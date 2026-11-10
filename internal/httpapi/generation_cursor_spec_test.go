package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func viewEventsURL(t *testing.T, view map[string]any) (string, bool) {
	t.Helper()
	g, present := view["generation"]
	if !present {
		t.Fatalf("campaign view has no generation key: %v", view)
	}
	if g == nil {
		return "", false
	}
	u, ok := g.(map[string]any)["events_url"].(string)
	if !ok {
		t.Fatalf("generation has no events_url: %v", g)
	}
	return u, true
}

func TestViewGenerationNullBeforeFirstGeneration(t *testing.T) {
	h, p := campaignAPI(t)
	rec := do(h, "POST", "/api/campaigns", createBody("brief", okURL), bearer)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	v := decodeView(t, rec)
	_, code, body := getView(t, h, v.Campaign.ID)
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if _, ok := viewEventsURL(t, body); ok {
		t.Errorf("never generated, but the view has a cursor: %v", body["generation"])
	}
	f := insertCampaign(t, p, "in_review", 1)
	if _, _, body := getView(t, h, f.ID); body["generation"] != nil {
		t.Errorf("fixture without events: generation %v, want null", body["generation"])
	}
}

func TestViewGenerationMatchesGenerate(t *testing.T) {
	h, p := generateAPI(t)
	f := insertCampaign(t, p, "in_review", 2)

	first := generateURL(t, h, f.ID, 2)
	if got, _, body := getView(t, h, f.ID); got != first {
		t.Errorf("after the first generate: view %q, 202 %q (%v)", got, first, body["generation"])
	}

	if _, err := p.Exec(t.Context(), `UPDATE campaigns SET state = 'in_review', version = 4 WHERE id = $1`, f.ID); err != nil {
		t.Fatal(err)
	}
	addGenEvent(t, p, f.ID, "done", map[string]any{})
	if got, _, _ := getView(t, h, f.ID); got != first {
		t.Errorf("in review after the first generation: view %q, want %q still", got, first)
	}

	second := generateURL(t, h, f.ID, 4)
	if second == first {
		t.Fatalf("second generate returned the first cursor %q", first)
	}
	if got, _, _ := getView(t, h, f.ID); got != second {
		t.Errorf("after the second generate: view %q, 202 %q", got, second)
	}
}

func TestViewGenerationSkipsOlderGeneration(t *testing.T) {
	srv, _, _ := liveStack(t)
	code, created := apiCall(t, srv, "POST", "/api/campaigns", createBody(specBrief, okURL))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["campaign"].(map[string]any)["id"].(string)
	firstRun := sseCollect(t, srv, startGenerate(t, srv, id, 0), bearer)
	firstDone := firstRun[len(firstRun)-1]
	if firstDone.Event != "done" {
		t.Fatalf("first generation ended with %q", firstDone.Event)
	}

	_, view := apiCall(t, srv, "GET", "/api/campaigns/"+id, "")
	version := int(view["campaign"].(map[string]any)["version"].(float64))
	startGenerate(t, srv, id, version)

	_, view = apiCall(t, srv, "GET", "/api/campaigns/"+id, "")
	u, ok := viewEventsURL(t, view)
	if !ok || !isEventsURL(u, id) {
		t.Fatalf("mid-generation view: generation %v", view["generation"])
	}
	events := sseCollect(t, srv, u, bearer)
	if len(events) == 0 || events[0].Event != "started" {
		t.Fatalf("stream from the view starts with %+v, want started", events)
	}
	for _, e := range events {
		if e.ID <= firstDone.ID {
			t.Errorf("event %d (%s) belongs to the first generation", e.ID, e.Event)
		}
	}
	if last := events[len(events)-1]; last.Event != "done" {
		t.Errorf("second stream ended with %q", last.Event)
	}
}

func getView(t *testing.T, h http.Handler, id string) (string, int, map[string]any) {
	t.Helper()
	rec := do(h, "GET", "/api/campaigns/"+id, "", bearer)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("view: %s", rec.Body)
	}
	if rec.Code != http.StatusOK {
		return "", rec.Code, body
	}
	u, _ := viewEventsURL(t, body)
	return u, rec.Code, body
}

func generateURL(t *testing.T, h http.Handler, id string, version int) string {
	t.Helper()
	rec := do(h, "POST", "/api/campaigns/"+id+"/generate", versionBody(version), bearer)
	var body struct {
		EventsURL string `json:"events_url"`
	}
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &body) != nil || !isEventsURL(body.EventsURL, id) {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	return body.EventsURL
}
