# Product



## Platform

web

## Users

Primary audience: reviewers and evaluators of the Insider One take-home case, who open the UI to judge the product. The in-product persona is a campaign marketer who writes a brief, then reviews, edits and approves AI-proposed segment and copy before anything is sent. A person watching delivery status on the dashboard is a secondary role (not separately confirmed).

## Product Purpose

Smart Campaign Composer. A marketer writes a campaign brief. Claude proposes a target segment and SMS + email copy. A human reviews, edits and approves. Messages go through a Postgres-backed queue (River) to a mock provider, and a dashboard shows delivery status live. No real recipient is ever contacted. Success is a believable, safe path from brief to approved campaign to visible delivery.

## Positioning

The human stays in control of AI output. LLM output is untrusted: it is validated, shown with code-generated rule descriptions, and can only reach recipients through an approval state machine. Consent and links are structurally enforced, not left to the model.

## Operating Context

Single-admin-token app served by nginx on localhost:8081 in a docker compose stack. Flow: compose a brief, watch generation stream in, review segment and copy, edit (edits pass the same validator as LLM output), approve, then watch delivery and a "needs attention" view for stuck and failed messages. Live updates via SSE and polling.

## Capabilities and Constraints

- Views: compose, campaign (review, approval, audit trail, delivery dashboard).
- Personalization variables only `first_name`, `last_name`, `city`, `campaign_url`. No URLs or email addresses from the model or from edits.
- Validation failures, conflicts (version mismatch) and delivery failures must be loud and visible, with stable `{path, code, message}` errors.
- Message states: queued, sending, sent, failed, dead, skipped.
- SMS is limited to 160 characters after variable fill.
- Not done: delivery receipts, and stretch items listed in the README.

## Brand Commitments

UI copy is English (confirmed by the user). No Insider One branding or logo is established.

## Evidence on Hand

Seed data of about 5,000 synthetic users (`docs/seed-data.md`), golden briefs in `evals/`, a mock LLM and mock provider. No real customers, testimonials or benchmarks exist. Do not fabricate any.

## Product Principles

1. Trustworthy and legible: segment rules, validation issues, audit trail and failures are always visible, never hidden.
2. The human decides: nothing is sent without explicit approval of a specific revision.
3. Failure is a first-class state: failed, stuck and conflicting states get as much clarity as success.
4. Show what the code knows, not what the model claims: descriptions and counts come from deterministic code.

## Accessibility & Inclusion

No specific standard was established. Left undecided.
