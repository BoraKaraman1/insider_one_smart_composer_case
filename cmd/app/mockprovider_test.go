package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/testdb"
)

func TestMockproviderNeedsItsOwnDatabase(t *testing.T) {
	ctx := context.Background()
	if _, _, err := newMockprovider(ctx, env(nil)); err == nil {
		t.Error("started without MOCKPROVIDER_DATABASE_URL")
	}
	same := env(map[string]string{"DATABASE_URL": "postgres://x/app", "MOCKPROVIDER_DATABASE_URL": "postgres://x/app"})
	if _, _, err := newMockprovider(ctx, same); err == nil {
		t.Error("started on the app database")
	}
	if _, _, err := newMockprovider(ctx, env(map[string]string{"MOCKPROVIDER_RATE_500": "2"})); err == nil {
		t.Error("started with a rate above 1")
	}
}

func TestMockproviderServes(t *testing.T) {
	srv, cleanup, err := newMockprovider(context.Background(), env(map[string]string{"MOCKPROVIDER_DATABASE_URL": testdb.Fresh(t)}))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	hs := httptest.NewServer(srv)
	defer hs.Close()
	req, _ := http.NewRequest("POST", hs.URL+"/v1/messages", strings.NewReader(`{"channel":"sms","to":"+90 000","body":"hi"}`))
	req.Header.Set("Idempotency-Key", "k")
	resp, err := hs.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("send: %d", resp.StatusCode)
	}
}
