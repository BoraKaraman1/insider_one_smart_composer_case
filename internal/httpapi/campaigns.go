package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

// HTTP-level error codes of the campaign routes.
const (
	codeNotFound          = "not_found"
	codeVersionConflict   = "version_conflict"
	codeInvalidTransition = "invalid_transition"
)

// actor is who the audit trail names. There is one admin token, so there is
// one actor.
const actor = "admin"

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// campaignRoutes registers the stage 0 campaign routes. Generate, approve
// and the read APIs of later stages go in their own files.
func (s *Server) campaignRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/campaigns", s.createCampaign)
	api.HandleFunc("GET /api/campaigns/{id}", s.withCampaignID(s.getCampaign))
	api.HandleFunc("POST /api/campaigns/{id}/revisions", s.withCampaignID(s.editCampaign))
	api.HandleFunc("POST /api/campaigns/{id}/reject", s.withCampaignID(s.versionAction(campaign.Reject)))
	api.HandleFunc("POST /api/campaigns/{id}/cancel", s.withCampaignID(s.versionAction(s.cancel)))
}

// withCampaignID answers 404 for an id that is not a UUID, so it never
// reaches a query.
func (s *Server) withCampaignID(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !uuidPattern.MatchString(id) {
			writeError(w, http.StatusNotFound, codeNotFound, "campaign not found")
			return
		}
		h(w, r, id)
	}
}

// decodeBody reads one JSON value. On failure it has written the response.
func decodeBody(w http.ResponseWriter, r *http.Request) (any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	doc, err := schema.Decode(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, codeBodyTooLarge, "body is larger than 64 KiB")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, validation.InvalidJSON, err.Error())
		return nil, false
	}
	return doc, true
}

// fields checks that doc is an object with exactly the required keys.
func fields(doc any, res *validation.Result, keys ...string) map[string]any {
	m, ok := doc.(map[string]any)
	if !ok {
		res.Error("", validation.InvalidType, "expected an object")
		return nil
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
		if _, ok := m[k]; !ok {
			res.Error("/"+k, validation.Required, fmt.Sprintf("missing required key %q", k))
		}
	}
	for k := range m {
		if !allowed[k] {
			res.Error("/"+k, validation.UnknownProperty, fmt.Sprintf("key %q is not allowed", k))
		}
	}
	return m
}

// version reads the campaign version a client sends back.
func version(m map[string]any, res *validation.Result) int {
	v, present := m["version"]
	if !present {
		return 0 // reported by fields
	}
	n, ok := v.(json.Number)
	i, err := n.Int64()
	if !ok || err != nil || i < 0 || i > math.MaxInt32 {
		res.Error("/version", validation.InvalidType, "expected a non-negative integer")
		return 0
	}
	return int(i)
}

// campaignView is a campaign with its current revision and audit trail.
type campaignView struct {
	Campaign store.Campaign  `json:"campaign"`
	Revision *store.Revision `json:"revision"`
	Events   []store.Event   `json:"events"`
}

func (s *Server) loadView(ctx context.Context, id string) (campaignView, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var v campaignView
	var err error
	if v.Campaign, err = store.GetCampaign(ctx, s.Pool, id); err != nil {
		return v, err
	}
	if v.Campaign.CurrentRevisionID != nil {
		rev, err := store.GetRevision(ctx, s.Pool, id, *v.Campaign.CurrentRevisionID)
		if err != nil {
			return v, err
		}
		v.Revision = &rev
	}
	if v.Events, err = store.ListCampaignEvents(ctx, s.Pool, id); err != nil {
		return v, err
	}
	if v.Events == nil {
		v.Events = []store.Event{}
	}
	return v, nil
}

// writeView answers with the campaign as it is now.
func (s *Server) writeView(w http.ResponseWriter, r *http.Request, status int, id string) {
	v, err := s.loadView(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

// fail maps a campaign error to its status code.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, campaign.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "campaign not found")
	case errors.Is(err, campaign.ErrConflict):
		writeError(w, http.StatusConflict, codeVersionConflict, "the campaign changed; reload it and try again")
	case errors.Is(err, campaign.ErrInvalidTransition):
		writeError(w, http.StatusConflict, codeInvalidTransition, "the campaign's state does not allow this action")
	default:
		s.Log.Error("campaign request failed", "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, codeInternal, "the request failed")
	}
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	doc, ok := decodeBody(w, r)
	if !ok {
		return
	}
	n, res := campaign.ValidateNew(doc, s.Config.CampaignURLHosts)
	if !res.OK() {
		writeJSON(w, http.StatusUnprocessableEntity, issues(res.Errors))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	c, err := campaign.Create(ctx, s.Pool, n, actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeView(w, r, http.StatusCreated, c.ID)
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request, id string) {
	s.writeView(w, r, http.StatusOK, id)
}

// cancel cancels the campaign and, through River, its generate job.
func (s *Server) cancel(ctx context.Context, db campaign.Beginner, id string, version int, actor string) error {
	var jc campaign.JobCanceller
	if s.River != nil {
		jc = s.River
	}
	return campaign.CancelWithJobs(ctx, db, jc, id, version, actor)
}

// versionAction serves an action whose body is {"version": n}.
func (s *Server) versionAction(action func(context.Context, campaign.Beginner, string, int, string) error) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, id string) {
		doc, ok := decodeBody(w, r)
		if !ok {
			return
		}
		var res validation.Result
		m := fields(doc, &res, "version")
		v := version(m, &res)
		if !res.OK() {
			writeJSON(w, http.StatusUnprocessableEntity, issues(res.Errors))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		if err := action(ctx, s.Pool, id, v, actor); err != nil {
			s.fail(w, r, err)
			return
		}
		s.writeView(w, r, http.StatusOK, id)
	}
}

// editCampaign stores a human edit. It goes through the same validator as
// LLM output (rule 7); an invalid edit gets 422 and is never stored.
func (s *Server) editCampaign(w http.ResponseWriter, r *http.Request, id string) {
	doc, ok := decodeBody(w, r)
	if !ok {
		return
	}
	var res validation.Result
	m := fields(doc, &res, "base_revision_id", "version", "revision")
	v := version(m, &res)
	base, isString := m["base_revision_id"].(string)
	if _, present := m["base_revision_id"]; present && !isString {
		res.Error("/base_revision_id", validation.InvalidType, "expected a string")
	}
	var rev campaign.Revision
	var revRes validation.Result // its paths are relative to the revision, as stored
	if raw, present := m["revision"]; present {
		rev, revRes = campaign.ValidateRevision(s.Schemas, raw)
		for _, is := range revRes.Errors {
			res.Error("/revision"+is.Path, is.Code, is.Message)
		}
	}
	if !res.OK() {
		writeJSON(w, http.StatusUnprocessableEntity, issues(res.Errors))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	_, err := campaign.SaveEdit(ctx, s.Pool, campaign.Edit{
		ID:             id,
		BaseRevisionID: base,
		Version:        v,
		Revision:       rev,
		Warnings:       revRes.Warnings,
		Actor:          actor,
		AsOf:           s.Now().UTC(),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeView(w, r, http.StatusCreated, id)
}
