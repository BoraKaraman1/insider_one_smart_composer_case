package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/campaign"
	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/store"
)

const ReconcileEvery = 30 * time.Second

// Reconcile tükenen işlerin yarım bıraktığı mesaj ve kampanyaları kapatır.
// Her adım koşulludur, bu yüzden tekrar çalışması ve birden çok işçide çalışması zararsızdır.
func Reconcile(ctx context.Context, db DB, log *slog.Logger, met Metrics) error {
	var errs []error
	channels, err := store.ExpireDiscardedSends(ctx, db)
	errs = append(errs, err)
	for _, ch := range channels {
		met.Message(ch, "dead")
	}
	if len(channels) > 0 {
		log.Warn("reconcile: messages of discarded send jobs marked dead", "count", len(channels))
	}

	// Mesajı biten ama tamamlanma denetimi kaçmış kampanya da burada kapanır.
	done, err := store.FinishedSendingCampaigns(ctx, db)
	errs = append(errs, err)
	for _, id := range done {
		errs = append(errs, campaign.CompleteIfDone(ctx, db, id))
	}

	gens, err := store.StrandedGenerations(ctx, db)
	errs = append(errs, err)
	for _, g := range gens {
		log.Warn("reconcile: generation job discarded", "campaign_id", g.CampaignID)
		errs = append(errs, campaign.FailStrandedGeneration(ctx, db, g.CampaignID, g.Version))
	}

	fanouts, err := store.StrandedFanouts(ctx, db)
	errs = append(errs, err)
	for _, f := range fanouts {
		log.Warn("reconcile: fan-out job ended without sending", "campaign_id", f.CampaignID)
		errs = append(errs, campaign.CancelStrandedFanout(ctx, db, f.CampaignID, f.Version))
	}
	return errors.Join(errs...)
}

// Veritabanı o an yoksa sonraki tur yeniden dener.
func ReconcileLoop(ctx context.Context, db DB, log *slog.Logger, met Metrics, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := Reconcile(ctx, db, log, met); err != nil && ctx.Err() == nil {
				log.Error("reconcile failed", "error", err)
			}
		}
	}
}
