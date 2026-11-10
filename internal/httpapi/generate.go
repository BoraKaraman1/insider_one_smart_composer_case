package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/validation"
)

const codeUnavailable = "unavailable"

// generateRoutes registers the stage A routes: start a generation, and
// stream its events.
func (s *Server) generateRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/campaigns/{id}/generate", s.withCampaignID(s.generate))
	api.HandleFunc("GET /api/campaigns/{id}/events", s.withCampaignID(s.events))
}

type generateResponse struct {
	Version   int    `json:"version"`
	EventsURL string `json:"events_url"`
}

// generate moves the campaign to generating and enqueues the job in one
// transaction. It answers 202 with the URL of this generation's events: the
// stream starts at its started event, so an older generation's done or
// failed is never read as this one's.
func (s *Server) generate(w http.ResponseWriter, r *http.Request, id string) {
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
	if s.River == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "the job queue is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	args, started, err := campaign.BeginGeneration(ctx, s.Pool, s.River, id, v, actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, generateResponse{
		Version:   args.Version,
		EventsURL: "/api/campaigns/" + id + "/events?after=" + strconv.FormatInt(started-1, 10),
	})
}
