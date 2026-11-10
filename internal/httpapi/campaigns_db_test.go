package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

const (
	okURL   = "https://shop.example/kampanya"
	unknown = "00000000-0000-4000-8000-00000000dead"

	editSegment = `{"version":1,"match":"all","conditions":[
		{"kind":"enum_attribute","field":"city","op":"eq","value":"istanbul"},
		{"kind":"event","event":"cart_add","within_days":30,"negate":false},
		{"kind":"event","event":"purchase","within_days":30,"negate":true}]}`
	editContent = `{"sms":{"body":"Selam {{first_name}}, indirim burada: {{campaign_url}}"},"email":{"subject":"Sepetin seni bekliyor","body":"Selam {{first_name}}, indirim burada: {{campaign_url}}"}}`
)

func campaignAPI(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	url, meta := testdb.Seeded(t)
	env := map[string]string{"ADMIN_TOKEN": token, "CAMPAIGN_URL_HOSTS": "shop.example,kampanya.example"}
	cfg, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	p := pool(t, url)
	return handler(t, &Server{Config: cfg, Pool: p, Now: func() time.Time { return meta.AsOf }}), p
}

type fixture struct{ ID, Revision string }

func insertCampaign(t *testing.T, p *pgxpool.Pool, state string, version int) fixture {
	t.Helper()
	ctx := context.Background()
	var f fixture
	if err := p.QueryRow(ctx, `INSERT INTO campaigns (brief, campaign_url) VALUES ('brief', $1) RETURNING id::text`, okURL).Scan(&f.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO campaign_revisions (campaign_id, n, source, segment, sms_body, email_subject, email_body)
		VALUES ($1, 1, 'llm', $2, 'sms', 'subject', 'body') RETURNING id::text`, f.ID, editSegment).Scan(&f.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE campaigns SET state = $2, version = $3, current_revision_id = $4,
		approved_revision_id = $4, approved_by = 'fixture', approved_at = now() WHERE id = $1`, f.ID, state, version, f.Revision); err != nil {
		t.Fatal(err)
	}
	return f
}

type campaignRow struct {
	State     string
	Version   int
	Revisions int
	Events    []string
	Actors    []string
}

func loadCampaign(t *testing.T, p *pgxpool.Pool, id string) campaignRow {
	t.Helper()
	ctx := context.Background()
	var r campaignRow
	err := p.QueryRow(ctx, `SELECT state::text, version, (SELECT count(*) FROM campaign_revisions WHERE campaign_id = c.id)
		FROM campaigns c WHERE id = $1`, id).Scan(&r.State, &r.Version, &r.Revisions)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.Query(ctx, `SELECT coalesce(from_state::text, '') || '>' || to_state::text, actor FROM campaign_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var e, actor string
		if err := rows.Scan(&e, &actor); err != nil {
			t.Fatal(err)
		}
		r.Events, r.Actors = append(r.Events, e), append(r.Actors, actor)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return r
}

type view struct {
	Campaign struct {
		ID          string `json:"id"`
		Brief       string `json:"brief"`
		State       string `json:"state"`
		Version     *int   `json:"version"`
		CampaignURL string `json:"campaign_url"`
	} `json:"campaign"`
	Revision *struct {
		ID     string `json:"id"`
		N      int    `json:"n"`
		Source string `json:"source"`
	} `json:"revision"`
	Events []struct {
		From  *string `json:"from_state"`
		To    string  `json:"to_state"`
		Actor string  `json:"actor"`
	} `json:"events"`
}

func decodeView(t *testing.T, rec *httptest.ResponseRecorder) view {
	t.Helper()
	var v view
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("not a campaign response: %s", rec.Body)
	}
	if v.Campaign.Version == nil {
		t.Fatalf("response has no campaign.version: %s", rec.Body)
	}
	return v
}

func createBody(brief, campaignURL string) string {
	b, _ := json.Marshal(map[string]string{"brief": brief, "campaign_url": campaignURL})
	return string(b)
}

func TestCreateAndGetCampaign(t *testing.T) {
	h, p := campaignAPI(t)
	brief := "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder."
	rec := do(h, "POST", "/api/campaigns", createBody(brief, okURL), bearer)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decodeView(t, rec)
	if created.Campaign.ID == "" || created.Campaign.State != "draft" || *created.Campaign.Version != 0 ||
		created.Campaign.Brief != brief || created.Campaign.CampaignURL != okURL {
		t.Errorf("created %+v", created.Campaign)
	}
	if got := loadCampaign(t, p, created.Campaign.ID); got.State != "draft" || got.Version != 0 || got.Revisions != 0 {
		t.Errorf("stored %+v", got)
	}

	rec = do(h, "GET", "/api/campaigns/"+created.Campaign.ID, "", bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	got := decodeView(t, rec)
	if got.Campaign.ID != created.Campaign.ID || got.Campaign.State != "draft" || *got.Campaign.Version != 0 ||
		got.Campaign.Brief != brief || got.Campaign.CampaignURL != okURL || got.Revision != nil {
		t.Errorf("get %+v (revision %v), want the created campaign with no revision", got.Campaign, got.Revision)
	}

	longest := strings.Repeat("ş", 2000)
	url40 := "https://shop.example/" + strings.Repeat("k", 19)
	if rec := do(h, "POST", "/api/campaigns", createBody(longest, url40), bearer); rec.Code != http.StatusCreated {
		t.Errorf("2,000-character brief with a 40-character URL: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/campaigns", createBody("brief", "https://kampanya.example/x"), bearer); rec.Code != http.StatusCreated {
		t.Errorf("second allowlisted host: %d %s", rec.Code, rec.Body)
	}
}

func TestGetCampaignShowsRevisionAndAuditTrail(t *testing.T) {
	h, p := campaignAPI(t)
	f := insertCampaign(t, p, "in_review", 2)
	if _, err := p.Exec(context.Background(), `INSERT INTO campaign_events (campaign_id, from_state, to_state, actor) VALUES
		($1, 'draft', 'generating', 'marketer'), ($1, 'generating', 'in_review', 'worker')`, f.ID); err != nil {
		t.Fatal(err)
	}
	rec := do(h, "GET", "/api/campaigns/"+f.ID, "", bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	v := decodeView(t, rec)
	if v.Campaign.State != "in_review" || *v.Campaign.Version != 2 {
		t.Errorf("campaign %+v", v.Campaign)
	}
	if v.Revision == nil || v.Revision.ID != f.Revision || v.Revision.N != 1 || v.Revision.Source != "llm" {
		t.Errorf("revision %+v", v.Revision)
	}
	if len(v.Events) != 2 || v.Events[0].To != "generating" || v.Events[1].To != "in_review" || v.Events[1].Actor != "worker" ||
		v.Events[1].From == nil || *v.Events[1].From != "generating" {
		t.Errorf("events %+v, want the two transitions, oldest first", v.Events)
	}
}

func TestCreateCampaignRejects(t *testing.T) {
	h, p := campaignAPI(t)
	cases := []struct {
		name, body string
		status     int
		want       string
	}{
		{"broken JSON", `{`, 400, " invalid_json"},
		{"not an object", `[]`, 422, " invalid_type"},
		{"missing brief", `{"campaign_url":"` + okURL + `"}`, 422, "/brief required"},
		{"empty brief", createBody("", okURL), 422, "/brief too_short"},
		{"brief of 2,001 characters", createBody(strings.Repeat("ş", 2001), okURL), 422, "/brief too_long"},
		{"brief is not a string", `{"brief":5,"campaign_url":"` + okURL + `"}`, 422, "/brief invalid_type"},
		{"missing campaign_url", `{"brief":"b"}`, 422, "/campaign_url required"},
		{"empty campaign_url", createBody("b", ""), 422, "/campaign_url "},
		{"null campaign_url", `{"brief":"b","campaign_url":null}`, 422, "/campaign_url "},
		{"http scheme", createBody("b", "http://shop.example/k"), 422, "/campaign_url url_not_allowed"},
		{"no scheme", createBody("b", "shop.example/k"), 422, "/campaign_url url_not_allowed"},
		{"javascript scheme", createBody("b", "javascript:alert(1)"), 422, "/campaign_url url_not_allowed"},
		{"host outside the allowlist", createBody("b", "https://evil.example/k"), 422, "/campaign_url url_not_allowed"},
		{"allowlisted host as a prefix", createBody("b", "https://shop.example.evil.test/"), 422, "/campaign_url url_not_allowed"},
		{"allowlisted host as userinfo", createBody("b", "https://shop.example@evil.test/"), 422, "/campaign_url url_not_allowed"},
		{"allowlisted host in the path", createBody("b", "https://evil.test/shop.example"), 422, "/campaign_url url_not_allowed"},
		{"41 characters", createBody("b", "https://shop.example/"+strings.Repeat("k", 20)), 422, "/campaign_url too_long"},
		{"extra key", `{"brief":"b","campaign_url":"` + okURL + `","state":"approved"}`, 422, "/state unknown_property"},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/api/campaigns", c.body, bearer)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d: %s", c.name, rec.Code, c.status, rec.Body)
			continue
		}
		got := errorCodes(t, rec)
		if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, c.want) }) {
			t.Errorf("%s: errors %v, want %q", c.name, got, c.want)
		}
	}
	var n int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM campaigns`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d campaigns stored by rejected requests (%v)", n, err)
	}
}

func TestCampaignRoutesNeedAuthAndAnExistingCampaign(t *testing.T) {
	h, _ := campaignAPI(t)
	routes := []struct{ method, path, body string }{
		{"GET", "/api/campaigns/" + unknown, ""},
		{"POST", "/api/campaigns/" + unknown + "/reject", `{"version":0}`},
		{"POST", "/api/campaigns/" + unknown + "/cancel", `{"version":0}`},
		{"POST", "/api/campaigns/" + unknown + "/revisions", editBody(unknown, 0, editSegment, editContent)},
	}
	for _, r := range routes {
		if rec := do(h, r.method, r.path, r.body, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d, want 401", r.method, r.path, rec.Code)
		}
		if rec := do(h, r.method, r.path, r.body, bearer); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s for an unknown campaign: %d, want 404: %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", "/api/campaigns", createBody("b", okURL), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("create without a token: %d, want 401", rec.Code)
	}
}

func versionBody(v int) string { return fmt.Sprintf(`{"version":%d}`, v) }

func TestReject(t *testing.T) {
	h, p := campaignAPI(t)
	f := insertCampaign(t, p, "in_review", 3)

	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/reject", versionBody(2), bearer); rec.Code != http.StatusConflict {
		t.Fatalf("stale version: %d, want 409: %s", rec.Code, rec.Body)
	}
	if got := loadCampaign(t, p, f.ID); got.State != "in_review" || got.Version != 3 || len(got.Events) != 0 {
		t.Fatalf("a stale reject changed the campaign: %+v", got)
	}

	rec := do(h, "POST", "/api/campaigns/"+f.ID+"/reject", versionBody(3), bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body)
	}
	if v := decodeView(t, rec); v.Campaign.State != "rejected" || *v.Campaign.Version != 4 {
		t.Errorf("response %+v", v.Campaign)
	}
	got := loadCampaign(t, p, f.ID)
	if got.State != "rejected" || got.Version != 4 || !slices.Equal(got.Events, []string{"in_review>rejected"}) || got.Actors[0] == "" {
		t.Errorf("stored %+v; want rejected, version 4, one audit row with an actor", got)
	}

	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/reject", versionBody(4), bearer); rec.Code != http.StatusConflict {
		t.Errorf("reject twice: %d, want 409", rec.Code)
	}
	for _, state := range []string{"draft", "generating", "generation_failed", "approved", "sending", "completed", "completed_with_failures", "cancelled"} {
		f := insertCampaign(t, p, state, 0)
		if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/reject", versionBody(0), bearer); rec.Code != http.StatusConflict {
			t.Errorf("reject from %s: %d, want 409", state, rec.Code)
		}
		if got := loadCampaign(t, p, f.ID); got.State != state || got.Version != 0 || len(got.Events) != 0 {
			t.Errorf("reject from %s changed the campaign: %+v", state, got)
		}
	}
}

func TestRejectAndCancelNeedAVersion(t *testing.T) {
	h, p := campaignAPI(t)
	f := insertCampaign(t, p, "in_review", 0)
	for _, action := range []string{"reject", "cancel"} {
		for _, body := range []string{`{}`, `{"version":"0"}`, `{"version":0,"state":"approved"}`} {
			if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/"+action, body, bearer); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s with body %s: %d, want 422", action, body, rec.Code)
			}
		}
	}
	if got := loadCampaign(t, p, f.ID); got.State != "in_review" || got.Version != 0 {
		t.Errorf("a malformed request changed the campaign: %+v", got)
	}
}

func TestCancelByState(t *testing.T) {
	h, p := campaignAPI(t)
	for _, state := range []string{"generating", "generation_failed", "in_review", "approved", "sending"} {
		f := insertCampaign(t, p, state, 5)
		rec := do(h, "POST", "/api/campaigns/"+f.ID+"/cancel", versionBody(5), bearer)
		if rec.Code != http.StatusOK {
			t.Errorf("cancel from %s: %d %s", state, rec.Code, rec.Body)
			continue
		}
		got := loadCampaign(t, p, f.ID)
		if got.State != "cancelled" || got.Version != 6 || !slices.Equal(got.Events, []string{state + ">cancelled"}) {
			t.Errorf("cancel from %s: stored %+v", state, got)
		}
	}
	for _, state := range []string{"draft", "completed", "completed_with_failures", "rejected", "cancelled"} {
		f := insertCampaign(t, p, state, 5)
		if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/cancel", versionBody(5), bearer); rec.Code != http.StatusConflict {
			t.Errorf("cancel from %s: %d, want 409", state, rec.Code)
		}
		if got := loadCampaign(t, p, f.ID); got.State != state || got.Version != 5 || len(got.Events) != 0 {
			t.Errorf("cancel from %s changed the campaign: %+v", state, got)
		}
	}
}

func TestCancelSkipsQueuedMessages(t *testing.T) {
	h, p := campaignAPI(t)
	ctx := context.Background()
	f := insertCampaign(t, p, "sending", 1)
	other := insertCampaign(t, p, "sending", 1)

	rows, err := p.Query(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 4`)
	if err != nil {
		t.Fatal(err)
	}
	var users []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
	}
	if rows.Err() != nil || len(users) != 4 {
		t.Fatalf("need 4 seed users, got %d (%v)", len(users), rows.Err())
	}
	insert := func(camp fixture, user, extra string) string {
		t.Helper()
		var id string
		q := `INSERT INTO messages (campaign_id, revision_id, user_id, channel) VALUES ($1, $2, $3, 'sms') RETURNING id::text`
		if err := p.QueryRow(ctx, q, camp.ID, camp.Revision, user).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if extra != "" {
			if _, err := p.Exec(ctx, `UPDATE messages SET `+extra+` WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	queued := insert(f, users[0], "")
	retrying := insert(f, users[1], `attempts = 2, last_error_class = 'server_error', next_attempt_at = now() + interval '30 seconds'`)
	sending := insert(f, users[2], `state = 'sending', attempts = 1`)
	sent := insert(f, users[3], `state = 'sent', attempts = 1, provider_message_id = 'p-1', sent_at = now()`)
	elsewhere := insert(other, users[0], "")

	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/cancel", versionBody(1), bearer); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	want := map[string]string{
		queued:    "skipped cancelled",
		retrying:  "skipped cancelled",
		sending:   "sending ",
		sent:      "sent ",
		elsewhere: "queued ",
	}
	names := map[string]string{queued: "queued", retrying: "retrying", sending: "sending", sent: "sent", elsewhere: "another campaign's"}
	for id, w := range want {
		var got string
		if err := p.QueryRow(ctx, `SELECT state::text || ' ' || coalesce(last_error_class::text, '') FROM messages WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("%s message is %q after cancel, want %q", names[id], got, w)
		}
	}
}

func TestConcurrentRequestsOneWins(t *testing.T) {
	h, p := campaignAPI(t)
	f := insertCampaign(t, p, "in_review", 0)
	actions := []string{"reject", "cancel", "reject", "cancel", "reject", "cancel", "reject", "cancel"}
	codes := make([]int, len(actions))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, action := range actions {
		wg.Go(func() {
			<-start
			codes[i] = do(h, "POST", "/api/campaigns/"+f.ID+"/"+action, versionBody(0), bearer).Code
		})
	}
	close(start)
	wg.Wait()

	var ok, conflict int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != len(actions)-1 {
		t.Errorf("status codes %v; want one 200 and the rest 409", codes)
	}
	if got := loadCampaign(t, p, f.ID); got.Version != 1 || len(got.Events) != 1 {
		t.Errorf("stored %+v; want version 1 and one audit row", got)
	}
}

func editBody(base string, version int, segment, content string) string {
	return fmt.Sprintf(`{"base_revision_id":%q,"version":%d,"revision":{"segment":%s,"content":%s}}`, base, version, segment, content)
}

func TestEditStoresNormalizedRevision(t *testing.T) {
	h, p := campaignAPI(t)
	ctx := context.Background()
	f := insertCampaign(t, p, "in_review", 1)
	upper := `{"version":1,"match":"ALL","conditions":[
		{"kind":"ENUM_ATTRIBUTE","field":"CITY","op":"EQ","value":"Istanbul"},
		{"kind":"EVENT","event":"CART_ADD","within_days":30,"negate":false},
		{"kind":"event","event":"PURCHASE","within_days":30,"negate":true}]}`

	rec := do(h, "POST", "/api/campaigns/"+f.ID+"/revisions", editBody(f.Revision, 1, upper, editContent), bearer)
	if rec.Code != http.StatusCreated {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	v := decodeView(t, rec)
	if v.Campaign.State != "in_review" || *v.Campaign.Version != 2 {
		t.Errorf("campaign %+v; want in_review at version 2", v.Campaign)
	}
	if v.Revision == nil || v.Revision.ID == f.Revision || v.Revision.N != 2 || v.Revision.Source != "human" {
		t.Fatalf("revision %+v; want a new revision 2 from a human", v.Revision)
	}

	var (
		segment, sms, subject, current string
		total, smsEligible, emailOK    int
		warnings                       int
	)
	err := p.QueryRow(ctx, `SELECT r.segment::text, r.sms_body, r.email_subject, c.current_revision_id::text,
			r.audience_total, r.sms_eligible, r.email_eligible, jsonb_array_length(r.warnings)
		FROM campaign_revisions r JOIN campaigns c ON c.id = r.campaign_id
		WHERE r.campaign_id = $1 AND r.n = 2 AND r.source = 'human'`, f.ID).
		Scan(&segment, &sms, &subject, &current, &total, &smsEligible, &emailOK, &warnings)
	if err != nil {
		t.Fatalf("revision 2 is not stored with its counts: %v", err)
	}
	if segment != strings.ToLower(segment) || !strings.Contains(segment, `"istanbul"`) {
		t.Errorf("stored segment is not normalized: %s", segment)
	}
	if !strings.Contains(sms, "{{campaign_url}}") || subject != "Sepetin seni bekliyor" {
		t.Errorf("stored content: sms %q, subject %q", sms, subject)
	}
	if current != v.Revision.ID {
		t.Errorf("campaign points at revision %s, want the new one %s", current, v.Revision.ID)
	}
	if total != 315 || smsEligible != 210 || emailOK <= 0 || emailOK > total {
		t.Errorf("counts %d total, %d sms, %d email; docs/seed-data.md says 315 and 210", total, smsEligible, emailOK)
	}
	if warnings == 0 {
		t.Error("the sms_multi_segment warning was not stored with the revision")
	}
	got := loadCampaign(t, p, f.ID)
	if got.State != "in_review" || got.Version != 2 || got.Revisions != 2 || !slices.Equal(got.Events, []string{"in_review>in_review"}) {
		t.Errorf("stored %+v; want in_review, version 2, two revisions, one audit row", got)
	}

	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/revisions", editBody(f.Revision, 2, editSegment, editContent), bearer); rec.Code != http.StatusConflict {
		t.Errorf("edit on top of a replaced revision: %d, want 409", rec.Code)
	}
	if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/revisions", editBody(v.Revision.ID, 1, editSegment, editContent), bearer); rec.Code != http.StatusConflict {
		t.Errorf("edit with a stale version: %d, want 409", rec.Code)
	}
	if got := loadCampaign(t, p, f.ID); got.Revisions != 2 || got.Version != 2 {
		t.Errorf("a stale edit was stored: %+v", got)
	}
}

func TestInvalidEditIsNotStored(t *testing.T) {
	h, p := campaignAPI(t)
	f := insertCampaign(t, p, "in_review", 1)
	content := func(sms, subject, body string) string {
		b, _ := json.Marshal(map[string]any{"sms": map[string]string{"body": sms}, "email": map[string]string{"subject": subject, "body": body}})
		return string(b)
	}
	segment := func(cond string) string { return `{"version":1,"match":"all","conditions":[` + cond + `]}` }
	cases := []struct {
		name, segment, content string
		want                   []string
	}{
		{"city outside the catalog", segment(`{"kind":"enum_attribute","field":"city","op":"eq","value":"paris"}`), editContent,
			[]string{"/segment/conditions/0/value unknown_value"}},
		{"consent as a field", segment(`{"kind":"enum_attribute","field":"consent_sms","op":"eq","value":"false"}`), editContent,
			[]string{"/segment/conditions/0/field unknown_value"}},
		{"SQL in a value", segment(`{"kind":"enum_attribute","field":"city","op":"eq","value":"x'; DROP TABLE users;--"}`), editContent,
			[]string{"/segment/conditions/0/value unknown_value"}},
		{"variable outside the allowlist", editSegment, content("Hi {{password}}", "s", "b"),
			[]string{"/content/sms/body unknown_variable"}},
		{"contact data variable", editSegment, content("Hi {{email}}", "s", "b"),
			[]string{"/content/sms/body unknown_variable"}},
		{"link typed by the human", editSegment, content("Hi", "s", "go to https://evil.example now"),
			[]string{"/content/email/body url_not_allowed"}},
		{"161-character SMS", editSegment, content(strings.Repeat("a", 161), "s", "b"),
			[]string{"/content/sms/body too_long"}},
		{"problems on both sides", segment(`{"kind":"enum_attribute","field":"city","op":"eq","value":"paris"}`), content("Hi {{password}}", "s", "b"),
			[]string{"/segment/conditions/0/value unknown_value", "/content/sms/body unknown_variable"}},
	}
	for _, c := range cases {
		rec := do(h, "POST", "/api/campaigns/"+f.ID+"/revisions", editBody(f.Revision, 1, c.segment, c.content), bearer)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422: %s", c.name, rec.Code, rec.Body)
			continue
		}
		got := errorCodes(t, rec)
		for _, w := range c.want {
			n := 0
			for _, g := range got {
				if strings.HasSuffix(g, w) {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s: %q reported %d times in %v, want once", c.name, w, n, got)
			}
		}
	}
	malformed := []struct{ name, body string }{
		{"no revision", fmt.Sprintf(`{"base_revision_id":%q,"version":1}`, f.Revision)},
		{"no base_revision_id", fmt.Sprintf(`{"version":1,"revision":{"segment":%s,"content":%s}}`, editSegment, editContent)},
		{"no version", fmt.Sprintf(`{"base_revision_id":%q,"revision":{"segment":%s,"content":%s}}`, f.Revision, editSegment, editContent)},
		{"extra key in the revision", fmt.Sprintf(`{"base_revision_id":%q,"version":1,"revision":{"segment":%s,"content":%s,"source":"llm"}}`, f.Revision, editSegment, editContent)},
		{"revision without content", fmt.Sprintf(`{"base_revision_id":%q,"version":1,"revision":{"segment":%s}}`, f.Revision, editSegment)},
	}
	for _, c := range malformed {
		if rec := do(h, "POST", "/api/campaigns/"+f.ID+"/revisions", c.body, bearer); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422: %s", c.name, rec.Code, rec.Body)
		}
	}
	if got := loadCampaign(t, p, f.ID); got.State != "in_review" || got.Version != 1 || got.Revisions != 1 || len(got.Events) != 0 {
		t.Errorf("an invalid edit left a trace: %+v", got)
	}
}

func TestEditOutsideReviewConflicts(t *testing.T) {
	h, p := campaignAPI(t)
	for _, state := range []string{"draft", "generating", "generation_failed", "approved", "sending", "completed", "completed_with_failures", "rejected", "cancelled"} {
		f := insertCampaign(t, p, state, 0)
		rec := do(h, "POST", "/api/campaigns/"+f.ID+"/revisions", editBody(f.Revision, 0, editSegment, editContent), bearer)
		if rec.Code != http.StatusConflict {
			t.Errorf("edit in %s: %d, want 409: %s", state, rec.Code, rec.Body)
		}
		if got := loadCampaign(t, p, f.ID); got.State != state || got.Version != 0 || got.Revisions != 1 {
			t.Errorf("edit in %s left a trace: %+v", state, got)
		}
	}
}
