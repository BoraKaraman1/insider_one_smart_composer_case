# adverserial review 3

> Review this codebase as a senior engineer preparing it for production. Also this chat will be exported as during the case submission.
>
> Focus on four areas:
>
> 1. Bugs and correctness
> 2. Readability and maintainability
> 3. Architecture and design
> 4. Production readiness\
>    Please inspect the repository carefully before making claims. Prioritize concrete findings over general advice. For each issue, include:
>
> - Severity: critical, high, medium, or low
> - File and line reference where possible
> - What is wrong
> - Why it matters
> - A recommended fix\
>   Pay special attention to:
> - Runtime errors, edge cases, race conditions, and incorrect assumptions
> - Error handling and failure modes
> - Data validation and security-sensitive paths
> - API boundaries and module responsibilities
> - Duplication, naming, complexity, and unclear control flow
> - Test coverage gaps, especially around risky behavior
> - Configuration, logging, observability, deployment, and environment handling
> - Dependency risks and outdated or unnecessary packages\
>   Do not rewrite the whole codebase. Start with a review report only. If you suggest refactors, keep them scoped and explain the tradeoff.
>
> Structure the response as:
>
> 1. Executive summary
> 2. Highest-risk findings
> 3. Bugs and correctness
> 4. Readability and maintainability
> 5. Architecture and design
>
> If there are no findings in a category, say so explicitly. Distinguish confirmed issues from assumptions or recommendations.

<details><summary>10 previous messages</summary>

> I’ll read the repository guidance, trace the main request and data flows, then run the relevant checks in mock mode. I’ll keep this to a review report and distinguish confirmed defects from deployment assumptions and recommendations.
>
> <details><summary>Explored 2 files, ran 11 commands</summary>
>
> <details><summary>Explored a file</summary>
>
> - Read `./CLAUDE.md`
>
> </details>
>
> - Ran `cat docs/ARCHITECTURE.md DECISIONS.md docs/segment-rules.md docs/seed-data.md`
>
> - Ran `git status --short && rg --files -g '!package-lock.json' -g '!go.sum' -g '!*.env*' -g '!node_modules' -g '!dist' -g '!vendor' -g '!DECISIONS.md' -g '!CLAUDE.md' -g '!AGENTS.md'`
>
> - Ran `cat internal/campaign/state.go internal/campaign/ops.go internal/campaign/approve.go internal/campaign/validate.go`
>
> - Ran `cat internal/delivery/fanout.go internal/delivery/send.go internal/delivery/limiter.go`
>
> - Ran `cat Makefile go.mod web/package.json Dockerfile docker-compose.yml web/nginx.conf .github/workflows/ci.yml README.md`
>
> <details><summary>Explored a file</summary>
>
> - Read `./internal/store/delivery.go`
>
> </details>
>
> - Ran `cat internal/httpapi/server.go internal/httpapi/config.go internal/httpapi/campaigns.go`
>
> - Ran `cat internal/campaign/ops.go internal/campaign/state.go internal/campaign/approve.go`
>
> - Ran `cat cmd/app/main.go cmd/app/worker.go internal/jobs/jobs.go`
>
> - Ran `LLM_MODE=mock go test ./...`
>
> - Ran `cat web/src/composables/useCampaign.ts web/src/composables/useDraft.ts web/src/composables/useGeneration.ts web/src/composables/useDeliveryPolling.ts`
>
> - Ran `cat web/src/views/CampaignView.vue web/src/views/ComposeView.vue web/src/api/client.ts web/src/api/sse.ts web/src/session.ts web/src/App.vue web/src/main.ts`
>
> </details>
>
> The main safety boundaries are present: parameterized segment queries, shared validation for model output and human edits, transactional job insertion, and provider idempotency. I’m now checking failure recovery and UI state, where those guarantees can still leave campaigns stuck or show reviewers stale copy.
>
> <details><summary>Explored 3 files, a search, ran 15 commands</summary>
>
> <details><summary>Explored a file</summary>
>
> - Read `./internal/campaign/generate.go`
>
> </details>
>
> - Ran `cat internal/httpapi/events.go internal/store/generation.go`
>
> - Ran `cat internal/llm/client.go internal/retry/retry.go`
>
> - Ran `cat internal/httpapi/campaigns.go internal/store/campaigns.go migrations/00002_campaigns_and_messages.sql migrations/00004_delivery.sql`
>
> - Ran `cat internal/content/validate.go internal/content/template.go internal/content/render.go internal/content/sms.go`
>
> - Ran `cat internal/content/validate.go internal/content/template.go internal/content/render.go internal/content/sms.go`
>
> - Ran `docker version --format '{{.Server.Version}}'`
>
> - Ran `LLM_MODE=mock go test ./...`
>
> - Ran `npm --prefix web run typecheck && npm --prefix web test && npm --prefix web run build`
>
> - Ran `cat internal/testdb/testdb.go internal/metrics/db.go internal/segment/compile.go .dockerignore web/.dockerignore README.md`
>
> - Ran `docker version --format '{{.Server.Version}}'`
>
> - Ran `cat internal/delivery/send_db_test.go internal/delivery/idempotency_db_test.go`
>
> - Ran `cat web/src/composables/useDraft.test.ts web/src/components/CopyEditor.vue web/src/components/ApproveBar.vue web/e2e/proxy.e2e.test.ts`
>
> - Ran `LLM_MODE=mock go vet ./...`
>
> - Ran `cat internal/eval/run.go internal/eval/score.go internal/eval/aggregate.go`
>
> <details><summary>Explored 2 files, a search</summary>
>
> - Read `./internal/mockprovider/mockprovider.go`
> - Read `./internal/delivery/helpers_test.go`
> - Searched for `ErrorHandler|OnDiscard|MaxAttempts|discarded|Watchdog|reconcil|rescue|retry_after|Retry-After` in `internal`
>
> </details>
>
> - Ran `docker run -d --rm --name codex-insider-review-20261110 -e POSTGRES_PASSWORD=test -p 127.0.0.1:55433:5432 postgres:18-alpine`
>
> </details>
>
> The Go tests pass with database tests skipped, and the frontend typecheck, 51 unit tests, and production build pass. I found a potentially serious review issue: after an approval conflict, the UI can retain old copy while the next approval targets the newer revision. I’m reproducing that behavior and checking delivery recovery against an isolated database.
>
> <details><summary>Explored 8 files, a search, ran 14 commands</summary>
>
> - Ran `docker exec codex-insider-review-20261110 psql -U postgres -q -c 'CREATE DATABASE app_template'`
>
> <details><summary>Explored a search</summary>
>
> - Searched for `DefaultMaxAttempts|JobStateDiscarded|MaxAttempts` in `client.go`
>
> </details>
>
> - Ran `cat web/vite.config.ts internal/content/content_test.go internal/segment/validate.go internal/schema/schema.go`
>
> - Ran `LLM_MODE=mock DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/app_template?sslmode=disable' go run ./cmd/app migrate && LLM_MODE=mock DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/app_template?sslmode=disable' go run ./cmd/app seed -as-of 2026-01-01T00:00:00Z -seed 42 -users 5000`
>
> - Ran `command -v govulncheck; command -v staticcheck; node --version; npm --version`
>
> <details><summary>Explored a file</summary>
>
> - Read `./docs/ARCHITECTURE.md`
>
> </details>
>
> - Ran `LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -race -count=1 -timeout 5m ./...`
>
> - Ran `LLM_MODE=mock staticcheck ./...`
>
> - Ran `npm --prefix web audit --json`
>
> - Ran `LLM_MODE=mock govulncheck ./...`
>
> - Ran `staticcheck -version; go version; cat web/src/stream/reducer.ts internal/campaign/generate_db_test.go | head -180`
>
> <details><summary>Explored 7 files</summary>
>
> - Read `./web/src/views/CampaignView.vue`
> - Read `./web/src/composables/useCampaign.ts`
> - Read `./web/src/composables/useDraft.ts`
> - Read `~/go/pkg/mod/github.com/riverqueue/river@v0.51.2/internal/jobexecutor/job_executor.go`
> - Read `./internal/delivery/fanout_db_test.go`
> - Read `./internal/httpapi/campaigns.go`
> - Read `./internal/campaign/state.go`
>
> </details>
>
> - Ran ``python3 - <<'PY'
> from pathlib import Path
> import json
> p=Path('/tmp/insider-review-probes'); p.mkdir(exist_ok=True)
> (p/'content_probe_test.go').write_text('''package content
> import (
>  "testing"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
> )
> func TestReviewURLBypass(t *testing.T) {
>  set, err := schema.Load(); if err != nil { t.Fatal(err) }
>  for _, s := range []string{"Visit evil.click", "Visit evil.top", "Visit evil.zip?next=login"} {
>   _, res := ValidateJSON(set, contentJSON(s, "subject", "body"))
>   if !res.OK() { t.Fatalf("probe changed: %q rejected: %v", s, res.Errors) }
>   t.Logf("model/human content accepted literal destination: %q", s)
>  }
> }
> ''')
> (p/'delivery_probe_test.go').write_text('''package delivery
> import (
>  "context"
>  "testing"
>  "time"
>  "log/slog"
>  "github.com/riverqueue/river"
>  "github.com/riverqueue/river/riverdriver/riverpgxv5"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
> )
> func TestReviewMutablePayload(t *testing.T) {
>  e:=newSendEnv(t,mockprovider.Config{}); c:=insertSendingCampaign(t,e.pool)
>  user:=consentingUsers(t,e.pool,1)[0]; id:=insertMessage(t,e.pool,c,user,"sms","")
>  e.prov.force(id,"drop","ok")
>  if _,ok:=snoozed(e.work(id)); !ok { t.Fatal("expected ambiguous first call") }
>  if _,err:=e.pool.Exec(context.Background(),`UPDATE users SET first_name = 'Changed' WHERE id = $1`,user); err!=nil {t.Fatal(err)}
>  if !cancelled(e.work(id)) {t.Fatal("expected permanent 422")}
>  m:=loadMessage(t,e.pool,id); s:=e.prov.stats(t)
>  if m.State!="failed" || s.UniqueSends!=1 {t.Fatalf("unexpected %s sends %d",m.State,s.UniqueSends)}
>  t.Log("drop then profile update: provider sent once, retry returned 422, app marked failed")
> }
> func TestReviewBudgetBeforeCall(t *testing.T) {
>  for _,which:=range []string{"attempts","age"} { t.Run(which,func(t *testing.T){
>  e:=newSendEnv(t,mockprovider.Config{});c:=insertSendingCampaign(t,e.pool)
>  id:=insertMessage(t,e.pool,c,consentingUsers(t,e.pool,1)[0],"sms","")
>  a:=1; first:=e.now.Add(-16*time.Minute); if which=="attempts" {a=MaxAttempts;first=e.now}
>  if _,err:=e.pool.Exec(context.Background(),`UPDATE messages SET state='sending', attempts=$2, first_attempt_at=$3 WHERE id=$1`,id,a,first);err!=nil {t.Fatal(err)}
>  if err:=e.work(id);err!=nil {t.Fatal(err)}
>  m:=loadMessage(t,e.pool,id)
>  if m.State!="sent" || len(e.prov.callsFor(id))!=1 {t.Fatalf("unexpected probe result: %+v",m)}
>  t.Logf("exhausted %s budget still called provider; attempts=%d state=%s",which,m.Attempts,m.State)
>  }) }
> }
> func TestReviewDiscardOrphan(t *testing.T) {
>  e:=newSendEnv(t,mockprovider.Config{});c:=insertSendingCampaign(t,e.pool)
>  id:=insertMessage(t,e.pool,c,consentingUsers(t,e.pool,1)[0],"sms","")
>  bad:=&flakyDB{Pool:e.pool};bad.down.Store(true);e.worker.DB=bad
>  workers:=river.NewWorkers();river.AddWorker(workers,e.worker)
>  cfg:=jobs.ClientConfig(workers,slog.New(slog.DiscardHandler));cfg.TestOnly=true;cfg.MaxAttempts=1
>  cfg.FetchCooldown=time.Millisecond;cfg.FetchPollInterval=10*time.Millisecond
>  rc,err:=river.NewClient(riverpgxv5.New(e.pool),cfg);if err!=nil {t.Fatal(err)}
>  ctx:=context.Background();if err:=rc.Start(ctx);err!=nil {t.Fatal(err)}
>  defer func(){stop,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel();if err:=rc.Stop(stop);err!=nil {t.Error(err)}}()
>  job,err:=rc.Insert(ctx,jobs.SendArgs{MessageID:id},&river.InsertOpts{Queue:jobs.QueueSMS,MaxAttempts:1});if err!=nil {t.Fatal(err)}
>  deadline:=time.Now().Add(5*time.Second)
>  for {var state string;if err:=e.pool.QueryRow(ctx,`SELECT state::text FROM river_job WHERE id=$1`,job.Job.ID).Scan(&state);err!=nil {t.Fatal(err)}
>  if state=="discarded" {break};if time.Now().After(deadline){t.Fatalf("job remained %s",state)};time.Sleep(10*time.Millisecond)}
>  m:=loadMessage(t,e.pool,id);state,_:=campaignState(t,e.pool,c.ID)
>  if m.State!="queued" || state!="sending" {t.Fatalf("unexpected probe result: message=%s campaign=%s",m.State,state)}
>  t.Log("after final River failure: job discarded, message queued, campaign sending, no provider calls")
> }
> ''')
> root=Path('/Users/borakaraman/Desktop/insider_one_case')
> (p/'overlay.json').write_text(json.dumps({'Replace':{str(root/'internal/content/review_probe_test.go'):str(p/'content_probe_test.go'),str(root/'internal/delivery/review_probe_test.go'):str(p/'delivery_probe_test.go')}}))
> PY``
>
> - Ran `LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -overlay /tmp/insider-review-probes/overlay.json -run '^TestReview' -v ./internal/content ./internal/delivery`
>
> - Ran `LLM_MODE=mock staticcheck ./...`
>
> - Ran `LLM_MODE=mock govulncheck ./...`
>
> - Ran `npm --prefix web audit --json`
>
> </details>
>
> The full race-enabled Go suite passes with the database tests enabled. Focused probes also confirmed that literal domains such as `evil.click` pass validation. I’m finishing checks around retry recovery and the approval mismatch so the report can include concrete failure sequences and targeted regression tests.
>
> <details><summary>Explored 15 files, 2 searches, ran 4 commands</summary>
>
> - Ran `cat > /tmp/insider-review-probes/ui-probe.cjs <<'JS'
> const fs = require('node:fs');
> const { pathToFileURL } = require('node:url');
> const assert = require('node:assert/strict');
> const root = '/Users/borakaraman/Desktop/insider_one_case/web';
> const ts = require(root + '/node_modules/typescript');
> const vueURL = pathToFileURL(root + '/node_modules/vue/dist/vue.runtime.esm-bundler.js').href;
> const modules = new Map();
> function moduleURL(file) {
>  if (modules.has(file)) return modules.get(file);
>  let js = ts.transpileModule(fs.readFileSync(root + '/src/' + file, 'utf8'), {compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText;
>  js = js.replace(/from 'vue'/g, 'from '+JSON.stringify(vueURL));
>  const mapping = {'../api/client':'api/client.ts','./issues':'api/issues.ts','../api/issues':'api/issues.ts'};
>  js = js.replace(/from '([^']+)'/g, (match, name) => mapping[name] ? 'from '+JSON.stringify(moduleURL(mapping[name])) : match);
>  const url = 'data:text/javascript;base64,' + Buffer.from(js).toString('base64');
>  modules.set(file,url); return url;
> }
> (async()=>{
>  const {ref,watch,nextTick}=await import(vueURL);
>  const {useDraft}=await import(moduleURL('composables/useDraft.ts'));
>  const {useCampaign}=await import(moduleURL('composables/useCampaign.ts'));
>  const {ApiError}=await import(moduleURL('api/client.ts'));
>  function view(id, version, sms) {return {campaign:{id:'c1',state:'in_review',version,current_revision_id:id},revision:{id,segment:{},sms_body:sms,email_subject:'subject',email_body:'body'},events:[],generation:null};}
>  const old = view('r1',2,'Copy reviewed by the user');
>  const latest = view('r2',3,'Different copy saved by another user');
>  const c=useCampaign(ref('c1'),{getCampaign:async()=>latest}); const d=useDraft();
>  watch(c.view,v=>{if(!v)return;if(d.draft.value===null)d.start(v);else d.observe(v);});
>  c.view.value=old;await nextTick();
>  await c.act('Approve',async()=>{throw new ApiError(409,[{path:'',code:'version_conflict',message:'changed'}]);});await nextTick();
>  assert.equal(d.draft.value.sms, old.revision.sms_body);assert.equal(d.dirty.value,false);assert.equal(d.conflict.value,false);assert.equal(c.view.value.campaign.current_revision_id,'r2');
>  let submitted;
>  await c.act('Approve',async()=>{submitted=c.view.value.campaign.current_revision_id;return latest;});
>  assert.equal(submitted,'r2');
>  console.log(JSON.stringify({visibleCopy:d.draft.value.sms,actualApprovedCopy:latest.revision.sms_body,submittedRevision:submitted,dirty:d.dirty.value,conflict:d.conflict.value},null,2));
> })();
> JS
> node /tmp/insider-review-probes/ui-probe.cjs`
>
> <details><summary>Explored 10 files</summary>
>
> - Read `./internal/delivery/send.go`
> - Read `./internal/store/delivery.go`
> - Read `./internal/content/validate.go`
> - Read `./internal/jobs/jobs.go`
> - Read `./internal/campaign/generate.go`
> - Read `./internal/delivery/limiter.go`
> - Read `./cmd/app/worker.go`
> - Read `./internal/httpapi/events.go`
> - Read `./.github/workflows/ci.yml`
> - Read `./docker-compose.yml`
>
> </details>
>
> - Ran `cat internal/httpapi/delivery.go internal/httpapi/generate.go internal/eval/score.go internal/llm/prompt.go`
>
> - Ran `git diff --stat && git status --short && rg --files -g 'AGENTS.md' -g 'CLAUDE.md' -g '!node_modules' -g '!web/dist' -g '!*.env*'`
>
> <details><summary>Explored 3 files, a search</summary>
>
> - Searched for `refused|InjectionRefused|fail.closed|transport` in `score_spec_test.go`
> - Read `./web/src/components/ApproveBar.vue`
> - Read `./web/src/composables/useGeneration.ts`
> - Read `./internal/eval/score.go`
>
> </details>
>
> - Ran ``python3 - <<'PY'
> from pathlib import Path
> p=Path('/tmp/insider-review-probes/delivery_probe_test.go')
> s=p.read_text().replace(' "log/slog"',' "log/slog"\n "sync"\n "sync/atomic"\n "net/http"\n "io"\n "strings"\n "github.com/jackc/pgx/v5"\n "github.com/jackc/pgx/v5/pgxpool"')
> s+='''
> type reviewSlowClaims struct { *pgxpool.Pool; n atomic.Int32; release chan struct{} }
> func(d *reviewSlowClaims) QueryRow(ctx context.Context,q string,args ...any) pgx.Row {
>  if strings.Contains(q,"UPDATE messages m SET state = 'sending'") {
>   n:=d.n.Add(1);if n<=20 {if n==20 {close(d.release)};select {case <-d.release:case <-ctx.Done():}}
>  }
>  return d.Pool.QueryRow(ctx,q,args...)
> }
> type reviewTransport func(*http.Request)(*http.Response,error)
> func(f reviewTransport)RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
> func TestReviewLimiterDBStall(t *testing.T){
>  e:=newSendEnv(t,mockprovider.Config{});c:=insertSendingCampaign(t,e.pool)
>  var mu sync.Mutex;var times []time.Time
>  e.worker.Limiters=NewLimiters()
>  e.worker.DB=&reviewSlowClaims{Pool:e.pool,release:make(chan struct{})}
>  e.worker.Client=&http.Client{Transport:reviewTransport(func(*http.Request)(*http.Response,error){
>   mu.Lock();times=append(times,time.Now());mu.Unlock()
>   return &http.Response{StatusCode:202,Body:io.NopCloser(strings.NewReader(`{"id":"p"}`)),Header:http.Header{}},nil
>  })}
>  ids:=make(chan string,160)
>  for _,user:=range consentingUsers(t,e.pool,160){ids<-insertMessage(t,e.pool,c,user,"sms","")};close(ids)
>  var wg sync.WaitGroup
>  for range 20 {wg.Go(func(){for id:=range ids {if err:=e.work(id);err!=nil {t.Error(err)}}})};wg.Wait()
>  max:=maxInWindow(times,time.Second)
>  if max<=100 {t.Fatalf("did not reproduce rate violation; maximum=%d",max)}
>  t.Logf("one worker, 20 SMS slots, delayed claims: %d provider calls in a sliding second",max)
> }
> '''
> p.write_text(s)
> PY
> LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -overlay /tmp/insider-review-probes/overlay.json -run '^TestReviewLimiterDBStall$' -v ./internal/delivery``
>
> <details><summary>Explored 3 files, a search</summary>
>
> - Searched for `func maxInWindow` in `limiter_test.go`
> - Read `./internal/mockprovider/mockprovider.go`
> - Read `./internal/delivery/send.go`
> - Read `./internal/store/metrics.go`
>
> </details>
>
> </details>
>
> The focused probes confirmed four high-risk issues: approval can target copy the UI is not showing; link validation accepts unlisted domains; exhausted River jobs can leave campaigns unfinished; and delayed database claims let one worker make 118 provider calls within a second despite the 100/s limit. Go and npm vulnerability checks reported no known vulnerabilities. I’m collecting the final references and removing the disposable database.
>
> <details><summary>Ran 2 commands</summary>
>
> - Ran `docker stop codex-insider-review-20261110`
> - Ran `nl -ba web/src/composables/useDraft.test.ts | sed -n '52,73p'; nl -ba internal/delivery/send_db_test.go | sed -n '225,265p'; nl -ba internal/content/content_test.go | sed -n '225,238p'; nl -ba web/src/composables/useCampaign.ts | sed -n '12,36p'; git diff --stat; git status --short`
>
> </details>
</details>

## 1. Executive summary

**The codebase has a solid foundation, but I would resolve the four high-severity findings before production deployment.** The strongest parts are parameterized segment queries, shared validation, transactional approval and queue insertion, guarded state transitions, and provider idempotency.

I found **eight confirmed issues: four high and four medium**. No critical issue was confirmed. Findings below distinguish reproduced behavior from conclusions based on code inspection.

Verification completed:

- Full Go suite with database integration tests and `-race`: passed.
- `go vet` and `staticcheck`: passed.
- Frontend typecheck, 51 unit tests, and production build: passed.
- `govulncheck` and `npm audit`: reported no known vulnerabilities.
- Additional temporary probes reproduced the approval, rate-limit, validation, retry-budget, idempotency, and discarded-job issues.

No tracked source files were changed. Environment files and the real API key were not accessed. Browser/proxy end-to-end tests and real external services were not exercised.

## 2. Highest-risk findings

### F1. Approval can target copy that the reviewer is not seeing

**Severity: high · Confirmed by reproduction**

References: [CampaignView.vue:26](./web/src/views/CampaignView.vue:26), [CampaignView.vue:82](./web/src/views/CampaignView.vue:82), [useDraft.ts:64](./web/src/composables/useDraft.ts:64).

After an approval receives a `409`, `useCampaign.act()` reloads the latest campaign. The watcher updates the draft’s `latest` reference but preserves its displayed copy and base revision. Approval then reads the revision ID from the newly loaded campaign.

Reproduced sequence:

1. The reviewer sees revision R1.
2. Another session saves different copy as R2.
3. Approving R1 receives a conflict and reloads R2.
4. The editor still displays R1, with `dirty=false` and `conflict=false`.
5. Clicking Approve again submits R2.

**Why it matters:** The backend correctly protects against stale requests, but the UI allows approval of content the person has not reviewed.

**Recommended fix:** Bind approval to the displayed revision. When a newer revision arrives, refresh a clean draft or require explicit conflict resolution before enabling approval. Preserve genuinely unsaved edits.

**Coverage needed:** Test the complete conflict → reload → second approval sequence, including displayed copy and submitted revision ID.

### F2. Database delays can defeat the provider rate limit

**Severity: high · Confirmed by reproduction**

Reference: [send.go:106](./internal/delivery/send.go:106).

The worker obtains a limiter token **before** claiming the message in PostgreSQL and rendering its payload. Concurrent workers can consume tokens at correctly spaced intervals, stall on their database claims, then reach the provider together.

A probe using one worker process, the configured 20 SMS workers, and delayed claims produced **118 provider calls within one sliding second**, exceeding the required 100/s.

**Why it matters:** The existing limiter test validates token issuance, while the requirement concerns actual provider calls. Database contention can create bursts even without multiple replicas.

**Recommended fix:** Perform database preparation before admission to the provider call, and place rate limiting immediately around request dispatch. Preserve cancellation and consent checks. If a strict dispatch guarantee is required, use a small per-channel dispatcher that owns call timing.

**Coverage needed:** Measure provider-call timestamps while injecting claim delays and connection-pool contention.

### F3. Literal domains bypass the link policy

**Severity: high · Confirmed by reproduction**

Reference: [validate.go:28](./internal/content/validate.go:28).

`domainRe` recognizes a fixed list of top-level domains. The fallback for other domains requires a trailing `/`.

The shared validator accepted all of these literal destinations:

```text
Visit evil.click
Visit evil.top
Visit evil.zip?next=login
```

**Why it matters:** Both model output and human edits can bypass the explicit rule that links come exclusively from `{{campaign_url}}`. Escaping HTML does not enforce that destination policy.

**Recommended fix:** Replace the fixed TLD list with domain recognition that covers bare hosts and hosts followed by queries, including internationalized names. Apply it to literal template content while retaining the controlled URL variable.

**Tradeoff:** A broader detector can reject benign dotted text. Define those exceptions explicitly and test them.

**Coverage needed:** Add bare unlisted domains, query-only destinations, and IDNs to the existing hostile-content fixtures.

### F4. Exhausted River jobs leave application state unfinished

**Severity: high · Confirmed by reproduction**

References: [jobs.go:64](./internal/jobs/jobs.go:64), [send.go:91](./internal/delivery/send.go:91), [generate.go:95](./internal/campaign/generate.go:95).

Infrastructure errors are returned to River, but there is no terminal-job reconciliation that updates the corresponding message or campaign when River exhausts retries.

I exercised the final-attempt path by reducing the probe job’s attempt limit to one and injecting a database-access failure. River marked the job `discarded`; the message remained `queued`, and its campaign remained `sending`.

**Why it matters:** The campaign can never complete automatically. The attention panel exposes a symptom, but River’s stuck-job rescuer does not revive a discarded job.

**Recommended fix:** Add idempotent reconciliation between terminal River jobs and application state. Include a periodic recovery pass because the database may also be unavailable when terminal error handling runs. Guard generation updates by campaign version.

**Coverage needed:** Test exhausted infrastructure failures for generation, fan-out, and sending, including failures after the provider has accepted a message.

## 3. Bugs and correctness

### F5. Retry budgets are checked after another provider call

**Severity: medium · Confirmed by reproduction**

References: [send.go:112](./internal/delivery/send.go:112), [send.go:163](./internal/delivery/send.go:163), [delivery.go:97](./internal/store/delivery.go:97).

`ClaimMessage()` increments attempts without checking the maximum attempts or age. The worker checks those limits only after calling the provider, and only after ruling out success and permanent failure.

Probes confirmed that:

- A reclaimed message already at six attempts made a seventh call.
- A message already 16 minutes past its first attempt was still sent successfully.

**Why it matters:** Recovery after a crash or persistence failure can exceed the documented six-attempt/15-minute delivery policy.

**Recommended fix:** Enforce retry eligibility atomically before claiming another dispatch. For an ambiguous previous outcome, preserve uncertainty or reconcile provider status instead of silently exceeding the sending budget.

**Coverage needed:** Assert zero new dispatches for exhausted messages, including successful provider responses. The existing age test checks a subsequent `503`, which misses this case.

### F6. Refreshing a failed generation loses its detailed errors

**Severity: medium · Confirmed by code inspection**

References: [CampaignView.vue:51](./web/src/views/CampaignView.vue:51), [CampaignView.vue:144](./web/src/views/CampaignView.vue:144), [campaigns.go:116](./internal/httpapi/campaigns.go:116).

Detailed generation errors live in the stream reducer. On mount, the page reopens the stream only when the campaign is `generating`. A campaign already in `generation_failed` therefore renders the reducer’s initially empty error list. The campaign-view response does not include the terminal generation errors.

**Why it matters:** Failure details are visible during the original session but disappear after refresh or when another reviewer opens the campaign.

**Recommended fix:** Include the latest generation outcome and issues in the campaign view, or replay the latest generation’s events when loading a failed campaign.

**Coverage needed:** Load a failed campaign directly and verify its persisted errors are displayed without starting another generation.

## 4. Readability and maintainability

### F7. Duplicated action rules have already drifted

**Severity: medium · Confirmed by code inspection**

References: [ApproveBar.vue:21](./web/src/components/ApproveBar.vue:21), [state.go:30](./internal/campaign/state.go:30).

The frontend displays **Cancel campaign** for `draft`, but the backend transition table permits only `draft → generating`. Cancelling a draft therefore always receives an invalid-transition conflict.

This is reachable when campaign creation succeeds but initial generation fails, leaving the user on a draft campaign page.

**Why it matters:** The UI offers an impossible action. Independent copies of state-machine rules make future drift likely.

**Recommended fix:** Align the intended draft-cancellation behavior, then add a contract test covering the actions offered for every state. A small backend-provided `allowed_actions` field is another option.

**Tradeoff:** A contract test is the smaller change. Returning capabilities reduces duplication but expands the API contract.

Beyond this concrete drift, I found no readability concern that justifies a broad rewrite. The package boundaries and small interfaces are generally clear. The most valuable testing improvement is coverage across those boundaries, particularly the approval workflow in F1.

## 5. Architecture and design

### F8. An idempotency key does not identify an immutable payload

**Severity: medium · Confirmed by reproduction; triggered by recipient-data changes**

References: [delivery.go:68](./internal/store/delivery.go:68), [send.go:229](./internal/delivery/send.go:229).

Each retry reloads current recipient names, city, and contact details, then renders the payload again. The idempotency key remains the message ID, while the provider requires the same key to carry the same payload.

Reproduced sequence:

1. The provider sends the message and drops the connection.
2. The recipient’s first name changes.
3. The retry renders different text under the same key.
4. The provider returns `422`, and the application marks the already-delivered message `failed`.

**Why it matters:** Idempotent recovery depends on stable request content as well as a stable key. Mutable recipient data breaks that contract.

**Recommended fix:** Persist the destination and rendered payload atomically before the first dispatch, then reuse them for every retry. Continue checking current consent separately.

**Tradeoff:** Persisting payloads adds contact-data storage and retention responsibilities, but makes delivery recovery reproducible and auditable.

**Additional deployment constraint, not a new defect:** [limiter.go:22](./internal/delivery/limiter.go:22) creates independent limiters per worker process. The architecture documentation acknowledges this single-worker limitation. Before deploying multiple workers or overlapping worker instances during rollout, enforce a shared channel limit or route sending through one rate-controlled dispatcher.

The existing Go/PostgreSQL/River architecture can accommodate these fixes without splitting services or replacing the queue.
