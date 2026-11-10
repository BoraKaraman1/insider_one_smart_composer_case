# Smart Campaign Composer

[![ci](https://github.com/BoraKaraman1/insider_one_smart_composer_case/actions/workflows/ci.yml/badge.svg)](https://github.com/BoraKaraman1/insider_one_smart_composer_case/actions/workflows/ci.yml)

A marketer writes a brief. The app proposes a segment and SMS and email copy. A person reviews and approves. Messages go to a mock provider. No real recipient is contacted.

The design is `docs/ARCHITECTURE.md`. This file is the short start. The long README is still to come.

## Quick start

```sh
make up
```

Open http://127.0.0.1:8081 and enter the dev token `dev-admin-token`.

The stack runs in mock mode and does not need an API key. `docker compose up` migrates, seeds about 5,000 users, and starts the api, the worker, both mocks and the web container.

| What | URL |
|---|---|
| Web UI | http://127.0.0.1:8081 |
| API | http://127.0.0.1:8080 |
| API readiness | http://127.0.0.1:8080/readyz |
| Worker metrics | http://127.0.0.1:9090/metrics |
| Worker readiness | http://127.0.0.1:9090/readyz |
| Mock provider stats | http://127.0.0.1:8091/v1/stats |

## Tests

`make test` runs the Go unit and integration tests against a throwaway Postgres. It unsets the API key.

`make web-test` typechecks the frontend and runs its unit tests. It needs Node 22.

`make e2e` runs the nginx scenarios against the stack on port 8081. Start the stack with `make up` first. Mock mode, no API key.

## Eval

`make eval` runs the golden briefs in mock mode and writes `evals/report-mock.md`. Those numbers show that the harness works. They do not describe a model.

To measure the model, in your own terminal:

```sh
LLM_MODE=anthropic make eval
```

That writes `evals/report-anthropic.md`. Keep the two reports apart. The report has no key and no recipient data.

## What to alert on

Counters and histograms are process activity. They reset when that process restarts, and they are not the dashboard totals.

Gauges are the persisted state, scraped from the worker at http://127.0.0.1:9090/metrics:

- `queue_oldest_job_age_seconds` growing means jobs are waiting and nothing is taking them.
- `queue_jobs` is how many jobs sit in each queue and state.
- `worker_last_processed_timestamp_seconds` stops moving when the worker stops finishing jobs.
- The dashboard reason `not_started` appears when a queued message has had no attempt for 2 minutes.

API `/readyz` checks the database and that migrations are applied. Worker `/readyz` checks that River is running and the database answers. Neither one means jobs are being consumed. Use the series above for that.
