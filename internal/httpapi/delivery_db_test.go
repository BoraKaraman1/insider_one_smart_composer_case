package httpapi

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func deliveryUsers(t *testing.T, p *pgxpool.Pool, n int) (ids, contacts []string) {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT id::text, phone, email FROM users ORDER BY id LIMIT $1`, n)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, phone, email string
		if err := rows.Scan(&id, &phone, &email); err != nil {
			t.Fatal(err)
		}
		ids, contacts = append(ids, id), append(contacts, phone, email)
	}
	if rows.Err() != nil || len(ids) != n {
		t.Fatalf("need %d seed users, got %d (%v)", n, len(ids), rows.Err())
	}
	return ids, contacts
}

func addMessage(t *testing.T, p *pgxpool.Pool, f fixture, user, channel, set string, now time.Time) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := p.QueryRow(ctx, `INSERT INTO messages (campaign_id, revision_id, user_id, channel) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		f.ID, f.Revision, user, channel).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if set != "" {
		args := []any{id}
		if strings.Contains(set, "$2") {
			set = strings.ReplaceAll(set, "$2", "$2::timestamptz")
			args = append(args, now)
		}
		if _, err := p.Exec(ctx, `UPDATE messages SET `+set+` WHERE id = $1`, args...); err != nil {
			t.Fatalf("%s: %v", set, err)
		}
	}
	return id
}

func TestDeliveryStats(t *testing.T) {
	h, p, now := approveAPI(t)
	f := insertCampaign(t, p, "sending", 1)
	other := insertCampaign(t, p, "sending", 1)
	users, _ := deliveryUsers(t, p, 9)
	add := func(user, channel, set string) { addMessage(t, p, f, user, channel, set, now) }
	add(users[0], "sms", "")
	add(users[1], "sms", "")
	add(users[2], "sms", `attempts = 2, last_error_class = 'server_error'`)
	add(users[3], "sms", `state = 'sending', attempts = 1`)
	add(users[4], "sms", `state = 'sent', attempts = 1, provider_message_id = 'p', sent_at = $2`)
	add(users[5], "sms", `state = 'failed', attempts = 1, last_error_class = 'client_error'`)
	add(users[6], "email", `state = 'dead', attempts = 6, last_error_class = 'server_error'`)
	add(users[7], "email", `state = 'skipped', last_error_class = 'no_consent'`)
	add(users[8], "email", `state = 'sent', attempts = 2, provider_message_id = 'q', sent_at = $2`)
	addMessage(t, p, other, users[0], "sms", "", now)

	rec := do(h, "GET", "/api/campaigns/"+f.ID+"/stats", "", bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Channels map[string]map[string]int `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not a stats body: %s", rec.Body)
	}
	zero := func() map[string]int {
		return map[string]int{"queued": 0, "retrying": 0, "sending": 0, "sent": 0, "failed": 0, "dead": 0, "skipped": 0}
	}
	wantSMS, wantEmail := zero(), zero()
	wantSMS["queued"], wantSMS["retrying"], wantSMS["sending"], wantSMS["sent"], wantSMS["failed"] = 2, 1, 1, 1, 1
	wantEmail["dead"], wantEmail["skipped"], wantEmail["sent"] = 1, 1, 1
	if !maps.Equal(body.Channels["sms"], wantSMS) {
		t.Errorf("sms %v, want %v", body.Channels["sms"], wantSMS)
	}
	if !maps.Equal(body.Channels["email"], wantEmail) {
		t.Errorf("email %v, want %v", body.Channels["email"], wantEmail)
	}
}

func TestDeliveryStatsEmpty(t *testing.T) {
	h, p, _ := approveAPI(t)
	f := insertCampaign(t, p, "approved", 1)
	rec := do(h, "GET", "/api/campaigns/"+f.ID+"/stats", "", bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Channels map[string]map[string]int `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"sms", "email"} {
		if len(body.Channels[ch]) != 7 {
			t.Errorf("%s: %v; want all 7 counts", ch, body.Channels[ch])
		}
		for state, n := range body.Channels[ch] {
			if n != 0 {
				t.Errorf("%s %s = %d", ch, state, n)
			}
		}
	}
}

type attentionItem struct {
	ID             string     `json:"id"`
	Channel        string     `json:"channel"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	Reason         string     `json:"reason"`
	LastErrorClass *string    `json:"last_error_class"`
	LastError      *string    `json:"last_error"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func TestAttentionList(t *testing.T) {
	h, p, now := approveAPI(t)
	f := insertCampaign(t, p, "sending", 1)
	other := insertCampaign(t, p, "sending", 1)
	users, contacts := deliveryUsers(t, p, 13)
	add := func(i int, set string) string { return addMessage(t, p, f, users[i], "sms", set, now) }

	want := map[string]string{
		add(0, `state = 'sending', attempts = 1, updated_at = $2 - interval '11 seconds'`):                                                 "stuck_sending",
		add(1, `attempts = 3, last_error_class = 'server_error', next_attempt_at = $2 + interval '20 seconds', updated_at = $2`):           "many_attempts",
		add(2, `attempts = 1, last_error_class = 'rate_limited', next_attempt_at = $2 - interval '31 seconds', updated_at = $2`):           "overdue",
		add(3, `state = 'failed', attempts = 1, last_error_class = 'client_error', last_error = 'provider answered 400', updated_at = $2`): "failed",
		add(4, `state = 'dead', attempts = 6, last_error_class = 'server_error', last_error = 'provider answered 503', updated_at = $2`):   "dead",
		add(11, `created_at = $2 - interval '121 seconds', updated_at = $2 - interval '121 seconds'`):                                      "not_started",
	}
	add(5, `state = 'sending', attempts = 1, updated_at = $2 - interval '10 seconds'`)
	add(6, `attempts = 2, last_error_class = 'server_error', next_attempt_at = $2 - interval '5 seconds', updated_at = $2`)
	add(7, `updated_at = $2`)
	add(12, `created_at = $2 - interval '119 seconds', updated_at = $2 - interval '119 seconds'`)
	add(8, `state = 'sent', attempts = 1, provider_message_id = 'p', sent_at = $2, updated_at = $2`)
	add(9, `state = 'skipped', last_error_class = 'cancelled', updated_at = $2`)
	addMessage(t, p, other, users[10], "sms", `state = 'failed', attempts = 1, last_error_class = 'client_error'`, now)

	rec := do(h, "GET", "/api/campaigns/"+f.ID+"/messages?filter=attention", "", bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("attention: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Messages []attentionItem `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an attention body: %s", rec.Body)
	}
	got := map[string]string{}
	for _, m := range body.Messages {
		got[m.ID] = m.Reason
		if m.Channel != "sms" || m.State == "" {
			t.Errorf("item %+v lacks channel or state", m)
		}
		switch m.Reason {
		case "failed", "dead":
			if m.LastErrorClass == nil || m.LastError == nil || !strings.HasPrefix(*m.LastError, "provider answered") {
				t.Errorf("%s item without its error: %+v", m.Reason, m)
			}
		case "overdue":
			if m.NextAttemptAt == nil || !m.NextAttemptAt.Equal(now.Add(-31*time.Second)) {
				t.Errorf("overdue item: next_attempt_at %v", m.NextAttemptAt)
			}
		}
	}
	if !maps.Equal(got, want) {
		t.Errorf("attention list %v\nwant %v", got, want)
	}
	for _, c := range contacts {
		if strings.Contains(rec.Body.String(), c) {
			t.Errorf("response contains contact data %q", c)
		}
	}
}

func TestMessagesRequestErrors(t *testing.T) {
	h, p, _ := approveAPI(t)
	f := insertCampaign(t, p, "sending", 1)
	for _, q := range []string{"", "?filter=everything"} {
		if rec := do(h, "GET", "/api/campaigns/"+f.ID+"/messages"+q, "", bearer); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("messages%s: %d; want 422", q, rec.Code)
		}
	}
	for _, path := range []string{"/stats", "/messages?filter=attention"} {
		if rec := do(h, "GET", "/api/campaigns/"+f.ID+path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", path, rec.Code)
		}
		if rec := do(h, "GET", "/api/campaigns/"+unknown+path, "", bearer); rec.Code != http.StatusNotFound {
			t.Errorf("%s of an unknown campaign: %d", path, rec.Code)
		}
	}
}
