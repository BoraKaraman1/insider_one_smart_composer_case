package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

const (
	codeNoRecipients  = "no_recipients"
	codeUnknownFilter = "unknown_filter"
)

// deliveryRoutes registers the stage B routes: approve, and the read APIs
// of the dashboard.
func (s *Server) deliveryRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/campaigns/{id}/approve", s.withCampaignID(s.approve))
	api.HandleFunc("GET /api/campaigns/{id}/stats", s.withCampaignID(s.deliveryStats))
	api.HandleFunc("GET /api/campaigns/{id}/messages", s.withCampaignID(s.messages))
}

// approve approves the latest revision and enqueues the fan-out job in one
// transaction. A stale revision or version, or a revision without a
// consenting recipient, gets 409.
func (s *Server) approve(w http.ResponseWriter, r *http.Request, id string) {
	doc, ok := decodeBody(w, r)
	if !ok {
		return
	}
	var res validation.Result
	m := fields(doc, &res, "revision_id", "version")
	v := version(m, &res)
	rev, isString := m["revision_id"].(string)
	if _, present := m["revision_id"]; present && !isString {
		res.Error("/revision_id", validation.InvalidType, "expected a string")
	}
	if !res.OK() {
		writeJSON(w, http.StatusUnprocessableEntity, issues(res.Errors))
		return
	}
	if !uuidPattern.MatchString(rev) {
		writeError(w, http.StatusConflict, codeVersionConflict, "the revision is not the current one; reload and try again")
		return
	}
	if s.River == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "the job queue is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	if err := campaign.Approve(ctx, s.Pool, s.River, id, rev, v, actor); err != nil {
		if errors.Is(err, campaign.ErrNoRecipients) {
			writeError(w, http.StatusConflict, codeNoRecipients, "no recipient has consent on any channel; edit the segment")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.writeView(w, r, http.StatusOK, id)
}

type statsResponse struct {
	Channels map[string]map[string]int `json:"channels"`
}

// deliveryStats counts the campaign's messages per channel and state.
func (s *Server) deliveryStats(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	if _, err := store.GetCampaign(ctx, s.Pool, id); err != nil {
		s.fail(w, r, err)
		return
	}
	stats, err := store.DeliveryStats(ctx, s.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statsResponse{Channels: stats})
}

type messagesResponse struct {
	Messages []store.AttentionItem `json:"messages"`
}

// messages lists messages that need attention (filter=attention, the only
// filter): stuck in sending, overdue, retried many times, failed or dead.
// Contact data is never part of the answer.
func (s *Server) messages(w http.ResponseWriter, r *http.Request, id string) {
	if f := r.URL.Query().Get("filter"); f != "attention" {
		writeJSON(w, http.StatusUnprocessableEntity, issues([]validation.Issue{{Path: "/filter", Code: codeUnknownFilter,
			Message: `filter must be "attention"`}}))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	if _, err := store.GetCampaign(ctx, s.Pool, id); err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := store.Attention(ctx, s.Pool, id, s.Now().UTC())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []store.AttentionItem{}
	}
	writeJSON(w, http.StatusOK, messagesResponse{Messages: items})
}
