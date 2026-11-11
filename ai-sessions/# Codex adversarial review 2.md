# adversarial review 2

> Review this codebase as a senior engineer preparing it for production. Also this chat will be exported as during the case submission.
>
> Focus on four areas:
> 1. Bugs and correctness
> 2. Readability and maintainability
> 3. Architecture and design
> 4. Production readiness
> Please inspect the repository carefully before making claims. Prioritize concrete findings over general advice. For each issue, include:
> - Severity: critical, high, medium, or low
> - File and line reference where possible
> - What is wrong
> - Why it matters
> - A recommended fix
> Pay special attention to:
> - Runtime errors, edge cases, race conditions, and incorrect assumptions
> - Error handling and failure modes
> - Data validation and security-sensitive paths
> - API boundaries and module responsibilities
> - Duplication, naming, complexity, and unclear control flow
> - Test coverage gaps, especially around risky behavior
> - Configuration, logging, observability, deployment, and environment handling
> - Dependency risks and outdated or unnecessary packages
> Do not rewrite the whole codebase. Start with a review report only. If you suggest refactors, keep them scoped and explain the tradeoff.
>
> Structure the response as:
> 1. Executive summary
> 2. Highest-risk findings
> 3. Bugs and correctness
> 4. Readability and maintainability
> 5. Architecture and design
>
> If there are no findings in a category, say so explicitly. Distinguish confirmed issues from assumptions or recommendations.

<details><summary>10 previous messages</summary>

> I’ll read `CLAUDE.md` first, then inspect the code, tests, and deployment setup. I’ll produce a review report only, with concrete file references and a clear distinction between confirmed issues and deployment assumptions.
>
> <details><summary>Explored a file, ran 11 commands</summary>
>
> <details><summary>Explored a file</summary>
>
> - Read `./CLAUDE.md`
>
> </details>
>
> - Ran `cat docs/ARCHITECTURE.md DECISIONS.md docs/segment-rules.md docs/seed-data.md`
>
> - Ran `git status --short; rg --files -g '!go.sum' -g '!package-lock.json' -g '!pnpm-lock.yaml' -g '!yarn.lock' -g '!.env*' -g '!node_modules' -g '!web/dist' -g '!dist'`
>
> - Ran `cat Makefile go.mod Dockerfile docker-compose.yml; sed -n '230,340p' docs/ARCHITECTURE.md`
>
> - Ran `wc -l cmd/app/*.go internal/{campaign,store,httpapi,delivery,llm,content,segment,retry,mockprovider}/*.go; cat cmd/app/main.go cmd/app/worker.go cmd/app/mockprovider.go`
>
> - Ran `cat internal/delivery/{send.go,fanout.go,limiter.go} internal/jobs/jobs.go internal/retry/retry.go`
>
> - Ran `cat internal/store/{campaigns.go,generation.go,delivery.go}; cat migrations/0000*.sql`
>
> - Ran `LLM_MODE=mock go test ./...`
>
> - Ran `docker version --format '{{.Server.Version}}'; command -v govulncheck; rg --files --hidden -g '!**/.git/**' -g '!**/.env*' -g '!node_modules/**' -g '!go.sum' -g '!ai-sessions/**' -g '!**/testdata/**'`
>
> - Ran `cat internal/campaign/{state.go,approve.go,ops.go,validate.go}`
>
> - Ran `cat internal/store/delivery.go internal/store/campaigns.go`
>
> - Ran `cat internal/httpapi/{server.go,config.go,campaigns.go,generate.go}`
>
> </details>
>
> The core safeguards are present: parameterized segment queries, guarded state transitions, transactional job insertion, and provider idempotency. I’m now checking failure recovery, concurrent workers, and the streaming endpoints. The working tree has deployment changes already in progress; I’ll review them without modifying them.
>
> <details><summary>Explored 6 files, ran 13 commands</summary>
>
> <details><summary>Explored a file</summary>
>
> - Read `./internal/campaign/generate.go`
>
> </details>
>
> - Ran `docker version --format '{{.Server.Version}}'; docker image ls --format '{{.Repository}}:{{.Tag}}' postgres`
>
> - Ran `LLM_MODE=mock go test -count=1 ./...`
>
> - Ran `cat internal/httpapi/server.go internal/httpapi/campaigns.go internal/httpapi/config.go`
>
> - Ran `cat internal/httpapi/events.go internal/httpapi/delivery.go`
>
> - Ran `docker run --detach --rm --name codex-review-pg-0110 --tmpfs /var/lib/postgresql -e POSTGRES_PASSWORD=test -p 127.0.0.1:55433:5432 postgres:18-alpine`
>
> - Ran `cat internal/llm/client.go internal/llm/prompt.go internal/retry/retry.go`
>
> - Ran `docker exec codex-review-pg-0110 psql -q -U postgres -c 'CREATE DATABASE app_template'`
>
> - Ran `cat internal/content/{validate.go,template.go,render.go,sms.go} internal/segment/compile.go`
>
> <details><summary>Explored a file</summary>
>
> - Read `./internal/mockprovider/mockprovider.go`
>
> </details>
>
> - Ran `cat internal/schema/schema.go internal/schema/issues.go internal/store/generation.go internal/store/migrate.go internal/testdb/testdb.go .dockerignore .gitignore deploy/postgres-init/01-mockprovider.sql`
>
> - Ran `LLM_MODE=mock DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/app_template?sslmode=disable' go run ./cmd/app migrate`
>
> - Ran `cat internal/store/migrate.go internal/store/generation.go internal/schema/schema.go internal/segment/compile.go`
>
> - Ran `LLM_MODE=mock DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/app_template?sslmode=disable' go run ./cmd/app seed -as-of 2026-01-01T00:00:00Z -seed 42 -users 5000`
>
> <details><summary>Explored 4 files</summary>
>
> - Read `./internal/delivery/helpers_test.go`
> - Read `./internal/delivery/send_db_test.go`
> - Read `./internal/content/content_test.go`
> - Read `./internal/campaign/generate_db_test.go`
>
> </details>
>
> - Ran `LLM_MODE=mock go vet ./...; git diff --check; gofmt -l cmd internal schemas migrations; rg -n 'Test|completion|consent|MaxAge|MaxAttempts|reclaim|crash|redirect|changed|payload' internal/delivery/*test.go`
>
> </details>
>
> The mock-mode tests pass once local test ports are allowed. I’ve also prepared an isolated Postgres instance for the integration suite. Two failure paths need closer verification: campaign completion after a terminal message update, and retries that rebuild payloads from mutable recipient data. I’m reproducing those before including them in the report.
>
> <details><summary>Explored 9 files, ran 9 commands</summary>
>
> - Ran `LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -count=1 ./...`
>
> <details><summary>Explored 3 files</summary>
>
> - Read `./internal/delivery/helpers_test.go`
> - Read `./internal/segment/validate.go`
> - Read `./internal/content/content_test.go`
>
> </details>
>
> - Ran `git status --short; rg -n 'watchdog|metrics|last_processed|health|ready|discard|ErrorHandler|MaxAttempts|Rescue|timeout' cmd internal docs/ARCHITECTURE.md -g '*.go' -g '*.md' -g '!**/testdata/**'; go list -m -f '{{.Path}} {{.Dir}}' github.com/riverqueue/river golang.org/x/time github.com/anthropics/anthropic-sdk-go/v2`
>
> - Ran ``mkdir -p /private/tmp/insider-review-probes; cat > /private/tmp/insider-review-probes/delivery_test.go <<'EOF'
> package delivery
>
> import (
>  "context"
>  "testing"
>  "github.com/jackc/pgx/v5"
>  "github.com/jackc/pgx/v5/pgxpool"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/mockprovider"
> )
>
> type reviewBeginFailure struct { *pgxpool.Pool; fail bool }
> func (d *reviewBeginFailure) Begin(ctx context.Context) (pgx.Tx,error) {
>  if d.fail { return nil, errCrash }; return d.Pool.Begin(ctx)
> }
> func TestReviewCompletionRecovery(t *testing.T) {
>  e:=newSendEnv(t,mockprovider.Config{})
>  c:=insertSendingCampaign(t,e.pool)
>  id:=insertMessage(t,e.pool,c,consentingUsers(t,e.pool,1)[0],"sms","")
>  d:=&reviewBeginFailure{Pool:e.pool,fail:true}; e.worker.DB=d
>  if err:=e.work(id); err==nil { t.Fatal("expected completion DB error") }
>  if m:=loadMessage(t,e.pool,id); m.State!="sent" { t.Fatalf("message state %s",m.State) }
>  d.fail=false
>  if err:=e.work(id);err!=nil {t.Fatal(err)}
>  state,_:=campaignState(t,e.pool,c.ID)
>  if state!="completed" { t.Errorf("retry acknowledged, but campaign remains %s despite its only message being sent",state) }
> }
>
> type reviewLimiterFunc func(context.Context) error
> func (f reviewLimiterFunc) Wait(ctx context.Context) error {return f(ctx)}
> func TestReviewConsentDuringLimiterWait(t *testing.T) {
>  e:=newSendEnv(t,mockprovider.Config{})
>  c:=insertSendingCampaign(t,e.pool)
>  user:=consentingUsers(t,e.pool,1)[0]
>  id:=insertMessage(t,e.pool,c,user,"sms","")
>  e.worker.Limiters["sms"]=reviewLimiterFunc(func(ctx context.Context)error {
>   _,err:=e.pool.Exec(ctx,`UPDATE users SET consent_sms=false WHERE id=$1`,user); return err
>  })
>  if err:=e.work(id);err!=nil {t.Fatal(err)}
>  if n:=len(e.prov.callsFor(id));n!=0 {t.Errorf("made %d provider call after consent was revoked during limiter wait",n)}
> }
> func TestReviewPayloadStableAfterDrop(t *testing.T) {
>  e:=newSendEnv(t,mockprovider.Config{})
>  c:=insertSendingCampaign(t,e.pool)
>  user:=consentingUsers(t,e.pool,1)[0]
>  id:=insertMessage(t,e.pool,c,user,"sms","")
>  e.prov.force(id,"drop","ok")
>  if _,ok:=snoozed(e.work(id));!ok {t.Fatal("first call must snooze after send-and-drop")}
>  if _,err:=e.pool.Exec(context.Background(),`UPDATE users SET first_name='ChangedName' WHERE id=$1`,user);err!=nil {t.Fatal(err)}
>  _=e.work(id)
>  if m:=loadMessage(t,e.pool,id);m.State!="sent" {t.Errorf("provider already sent once, but changed recipient data makes replay end in %s (%s)",m.State,str(m.LastErrorClass))}
> }
> EOF
> cat > /private/tmp/insider-review-probes/content_test.go <<'EOF'
> package content
> import (
>  "testing"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/schema"
> )
> func TestReviewBareDomains(t *testing.T) {
>  set,err:=schema.Load(); if err!=nil {t.Fatal(err)}
>  for _,sms:=range []string{"Visit evil.top", "Visit evil.click", "Visit evil.zip"} {
>   _,res:=ValidateJSON(set,contentJSON(sms,"Subject","Body"))
>   if res.OK() {t.Errorf("accepted non-allowlisted bare domain: %s",sms)}
>  }
> }
> EOF
> python3 - <<'PY'
> import json
> root='/Users/borakaraman/Desktop/insider_one_case'
> base='/private/tmp/insider-review-probes'
> with open(base+'/overlay.json','w') as f:
>  json.dump({'Replace':{root+'/internal/delivery/review_probe_test.go':base+'/delivery_test.go',root+'/internal/content/review_probe_test.go':base+'/content_test.go'}},f)
> PY``
>
> - Ran `LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -overlay=/private/tmp/insider-review-probes/overlay.json -run '^TestReview' -v ./internal/delivery ./internal/content`
>
> - Ran `LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -race -count=1 ./...`
>
> - Ran `rg -n 'DefaultMaxAttempts|MaxAttempts:|ErrorHandler|discarded|EventKindJobFailed' /Users/borakaraman/go/pkg/mod/github.com/riverqueue/river@v0.51.2/{client.go,config.go,worker.go,error_handler.go}`
>
> - Ran `cat internal/seed/load.go; sed -n '1,235p' internal/mockllm/mockllm.go; cat schemas/content.schema.json; sed -n '1,110p' internal/schema/issues.go; cat internal/segment/catalog.go internal/segment/describe.go`
>
> - Ran ``python3 - <<'PY'
> p='/private/tmp/insider-review-probes/delivery_test.go'
> s=open(p).read().replace(' "testing"',' "testing"\n "errors"\n "net/http"')
> s+='''
> type reviewRoundTripper func(*http.Request)(*http.Response,error)
> func (f reviewRoundTripper) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
> type reviewBrokenBody struct{}
> func (reviewBrokenBody) Read([]byte)(int,error){return 0,errors.New("connection reset while reading error body")}
> func (reviewBrokenBody) Close()error{return nil}
> func TestReviewPermanentStatusWithUnreadableBody(t *testing.T){
>  w:=&SendWorker{ProviderURL:"http://provider.example",Client:&http.Client{Transport:reviewRoundTripper(func(r *http.Request)(*http.Response,error){
>   return &http.Response{StatusCode:400,Header:http.Header{},Body:reviewBrokenBody{},Request:r},nil
>  })}}
>  _,res,_:=w.post(context.Background(),"test-message",[]byte(`{}`))
>  if res.Class.Retryable(){t.Errorf("retried known HTTP 400 because its body read failed: %s",res.Class)}
> }
> '''
> open(p,'w').write(s)
> PY``
>
> - Ran `LLM_MODE=mock go test -overlay=/private/tmp/insider-review-probes/overlay.json -run '^TestReviewPermanentStatus' -v ./internal/delivery`
>
> <details><summary>Explored 6 files</summary>
>
> - Read `./internal/delivery/send.go`
> - Read `./internal/content/validate.go`
> - Read `./internal/httpapi/events.go`
> - Read `./internal/httpapi/server.go`
> - Read `./internal/jobs/jobs.go`
> - Read `./cmd/app/worker.go`
>
> </details>
>
> </details>
>
> The full integration suite passes. Temporary probes reproduced four gaps in the existing coverage: consent revoked during a limiter wait still allows a send; a failed completion check can leave a campaign stuck; changed recipient data can break an idempotent retry; and bare domains on unlisted suffixes pass link validation. The probes are outside the repository and leave no code changes.
>
> <details><summary>Explored 5 files, ran 8 commands, searched the web</summary>
>
> Searched the web for `https://go.dev/security/vuln/`
>
> - Ran `LLM_MODE=mock go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
>
> - Ran `cat > /private/tmp/insider-review-probes/shutdown_test.go <<'EOF'
> package main
> import (
>  "context"
>  "log/slog"
>  "net"
>  "net/http"
>  "testing"
>  "time"
> )
> func TestReviewGracefulShutdownWaits(t *testing.T) {
>  ln,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
>  addr:=ln.Addr().String();ln.Close()
>  entered:=make(chan struct{});release:=make(chan struct{});defer close(release)
>  ctx,cancel:=context.WithCancel(context.Background());defer cancel()
>  done:=make(chan error,1)
>  hs:=&http.Server{Addr:addr,Handler:http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){close(entered);<-release})}
>  go func(){done<-serve(ctx,slog.New(slog.DiscardHandler),hs,"test")}()
>  requestDone:=make(chan struct{})
>  go func(){
>   defer close(requestDone)
>   for i:=0;i<100;i++ {
>    resp,err:=http.Get("http://"+addr)
>    if err==nil{resp.Body.Close();return};time.Sleep(time.Millisecond*5)
>   }
>  }()
>  select{case <-entered:case <-time.After(time.Second):t.Fatal("server did not start")}
>  cancel()
>  select{case err:=<-done:t.Errorf("serve returned %v while a request was still active",err)
>  case <-time.After(100*time.Millisecond):}
> }
> EOF
> python3 - <<'PY'
> import json
> base='/private/tmp/insider-review-probes'
> p=base+'/overlay.json'
> d=json.load(open(p));d['Replace']['/Users/borakaraman/Desktop/insider_one_case/cmd/app/review_probe_test.go']=base+'/shutdown_test.go'
> json.dump(d,open(p,'w'))
> PY`
>
> - Ran `LLM_MODE=mock go test -overlay=/private/tmp/insider-review-probes/overlay.json -run '^TestReviewGraceful' -v ./cmd/app`
>
> <details><summary>Explored 5 files</summary>
>
> - Read `./cmd/app/main.go`
> - Read `./cmd/app/worker.go`
> - Read `./internal/store/delivery.go`
> - Read `./internal/campaign/generate.go`
> - Read `./internal/delivery/limiter.go`
>
> </details>
>
> - Ran `LLM_MODE=mock go run golang.org/x/vuln/cmd/govulncheck@latest -json ./... > /private/tmp/insider-review-probes/govulncheck.json`
>
> Searched the web for `https://pkg.go.dev/vuln/GO-2026-4918`
>
> - Ran `python3 - <<'PY'
> import json
> s=open('/private/tmp/insider-review-probes/govulncheck.json').read(); d=json.JSONDecoder();p=0;advisories={};findings=[]
> while p<len(s):
>  while p<len(s) and s[p].isspace():p+=1
>  if p>=len(s):break
>  o,n=d.raw_decode(s,p);p=n
>  if 'config' in o: print('SCAN',json.dumps(o['config']))
>  if 'osv' in o:
>   v=o['osv'];advisories[v['id']]=v
>  if 'finding' in o:findings.append(o['finding'])
> for v in advisories.values():
>  fs=[x for x in findings if x['osv']==v['id']]
>  level=max((len(x.get('trace',[])) for x in fs), default=0)
>  packages=sorted({x['trace'][0].get('module','') for x in fs if x.get('trace')})
>  if packages!=['stdlib'] or v['id'] in ['GO-2026-4918','GO-2026-6809']:
>   print(v['id'],v.get('summary'),packages, 'symbol-reachable' if level>1 else 'package/module only', sorted({x.get('fixed','') for x in fs}))
> print('symbol advisories',len({x['osv'] for x in findings if len(x.get('trace',[]))>1}))
> PY`
>
> - Ran `python3 - <<'PY'
> import json
> s=open('/private/tmp/insider-review-probes/govulncheck.json').read();dec=json.JSONDecoder();p=0;osv={};fs=[]
> while p<len(s):
>  while p<len(s) and s[p].isspace():p+=1
>  if p==len(s):break
>  o,p=dec.raw_decode(s,p)
>  if 'osv' in o:osv[o['osv']['id']]=o['osv']
>  if 'finding' in o:fs.append(o['finding'])
> for id in sorted(set(x['osv'] for x in fs)):
>  f=[x for x in fs if x['osv']==id]; modules=sorted(set(x['trace'][0].get('module','') for x in f))
>  reachable=any(len(x.get('trace',[]))>1 for x in f)
>  if modules!=['stdlib'] or id=='GO-2026-6809':
>   print(id,osv[id]['summary'],modules,'symbol-reachable' if reachable else 'package/module only')
>   for a in osv[id]['affected']:
>    print('  ',a['package']['name'],[e for r in a['ranges'] for e in r['events']][-4:])
> PY`
>
> - Ran ``cat > /private/tmp/insider-review-probes/generation_test.go <<'EOF'
> package campaign
> import (
>  "context"
>  "testing"
>  "time"
>  "github.com/riverqueue/river"
>  "github.com/riverqueue/river/riverdriver/riverpgxv5"
>  "github.com/riverqueue/river/rivertype"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/jobs"
>  "github.com/BoraKaraman1/insider_one_smart_composer_case/internal/llm"
> )
> type reviewTimeoutLLM struct{}
> func (reviewTimeoutLLM) Model() string{return "review-mock"}
> func (reviewTimeoutLLM) Call(ctx context.Context,_ *llm.Conversation,_ func(string))(llm.Response,error){<-ctx.Done();return llm.Response{},ctx.Err()}
> type reviewFastGeneration struct{*GenerateWorker}
> func (w *reviewFastGeneration) Timeout(*river.Job[jobs.GenerateArgs])time.Duration{return 30*time.Millisecond}
> func TestReviewDiscardedGeneration(t *testing.T){
>  p,meta:=genPool(t);ctx:=context.Background()
>  id:=insertGenerating(t,p,"Review",1)
>  g:=&Generator{Pool:p,Schemas:schemas(t),Now:func()time.Time{return meta.AsOf},LLM:reviewTimeoutLLM{}}
>  ws:=river.NewWorkers();river.AddWorker(ws,&reviewFastGeneration{NewGenerateWorker(g)})
>  rc,err:=river.NewClient(riverpgxv5.New(p),&river.Config{Queues:map[string]river.QueueConfig{jobs.QueueGenerate:{MaxWorkers:1}},Workers:ws,MaxAttempts:1,TestOnly:true})
>  if err!=nil{t.Fatal(err)}
>  job,err:=rc.Insert(ctx,jobs.GenerateArgs{CampaignID:id,Version:1},nil);if err!=nil{t.Fatal(err)}
>  if err:=rc.Start(ctx);err!=nil{t.Fatal(err)}
>  defer rc.Stop(ctx)
>  deadline:=time.Now().Add(3*time.Second)
>  for time.Now().Before(deadline){
>   row,err:=rc.JobGet(ctx,job.Job.ID);if err!=nil{t.Fatal(err)}
>   if row.State==rivertype.JobStateDiscarded {
>    var state string
>    if err:=p.QueryRow(ctx,`SELECT state::text FROM campaigns WHERE id=$1`,id).Scan(&state);err!=nil{t.Fatal(err)}
>    if state!="generation_failed"{t.Errorf("River discarded the exhausted job but campaign remains %s",state)}
>    return
>   }
>   time.Sleep(10*time.Millisecond)
>  }
>  t.Fatal("job did not reach discarded")
> }
> EOF
> python3 - <<'PY'
> import json
> p='/private/tmp/insider-review-probes/overlay.json';d=json.load(open(p));d['Replace']['/Users/borakaraman/Desktop/insider_one_case/internal/campaign/review_probe_test.go']='/private/tmp/insider-review-probes/generation_test.go';json.dump(d,open(p,'w'))
> PY``
>
> - Ran `LLM_MODE=mock TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55433/postgres?sslmode=disable' REQUIRE_DB=1 go test -overlay=/private/tmp/insider-review-probes/overlay.json -run '^TestReviewDiscardedGeneration' -v ./internal/campaign`
>
> </details>
>
> The race detector and `go vet` pass. Two more probes confirmed that shutdown returns before active requests drain, and that a truncated HTTP 400 response is treated as retryable. The vulnerability scan also flags the local Go 1.26.2 toolchain; I’ll distinguish that result from the Docker image, whose actual build version I haven’t verified.
>
> <details><summary>Ran 2 commands</summary>
>
> - Ran `nl -ba go.mod | sed -n '1,19p'; nl -ba Dockerfile | sed -n '1,15p'; nl -ba internal/store/delivery.go | sed -n '78,96p'; nl -ba internal/jobs/jobs.go | sed -n '27,40p'; git diff --numstat -- go.mod go.sum; git status --short`
> - Ran `docker stop codex-review-pg-0110`
>
> </details>
</details>

## 1. Executive summary

**The backend has a solid foundation, but I would not approve production deployment yet.** The main risks concern failure recovery, consent enforcement, and validation boundaries.

Strengths include parameterized segment SQL, transactional approval and job insertion, guarded campaign transitions, provider idempotency, and substantial integration coverage.

Verification performed:

- All existing Go tests passed against isolated PostgreSQL in mock mode.
- The full suite passed with the race detector.
- `go vet`, formatting, and diff whitespace checks passed.
- Seven temporary regression probes reproduced the issues identified below.
- No repository files were changed by this review. Environment files and credentials were not read.

No critical issue was confirmed. Live Anthropic behavior and the built Docker image were not tested.

## 2. Highest-risk findings

### F1. Consent can be revoked before dispatch and still allow sending

**Severity: high · Confirmed by reproduction**

**Reference:** [send.go:90](./internal/delivery/send.go:90), [send.go:98](./internal/delivery/send.go:98)

Consent is loaded and checked before waiting for a rate-limit token. After the wait, the worker claims and sends the message using that earlier consent value.

A probe revoked SMS consent during `Limiter.Wait`; the worker subsequently called the provider.

**Why it matters:** A recipient can withdraw consent while the message is waiting, yet still receive it.

**Recommended fix:** Check current consent as part of claiming the message after acquiring the token. Define the claim as the dispatch cutoff and coordinate consent updates accordingly. Add a regression test that revokes consent while the limiter is blocked.

### F2. A transient completion failure can leave a campaign permanently `sending`

**Severity: high · Confirmed by reproduction**

**Reference:** [send.go:84](./internal/delivery/send.go:84), [send.go:159](./internal/delivery/send.go:159)

The terminal message update and campaign completion check are separate operations. If the message becomes `sent` but `CompleteIfDone` fails, River retries the job. That retry finds a terminal message and immediately returns success without checking campaign completion again.

The probe left a campaign `sending` even though its only message was `sent`.

**Why it matters:** Delivery succeeds, but the campaign never reaches its final state.

**Recommended fix:** Run the completion check before acknowledging jobs for terminal messages. Also consider making terminal updates and completion transactional, or adding reconciliation for campaigns with no unfinished messages.

### F3. The link restriction accepts unapproved bare domains

**Severity: high · Confirmed by reproduction**

**Reference:** [validate.go:40](./internal/content/validate.go:40)

Bare-domain detection recognizes a fixed list of suffixes. Other suffixes are rejected only when followed by a path.

All of these passed content validation:

- `Visit evil.top`
- `Visit evil.click`
- `Visit evil.zip`

**Why it matters:** Both model output and human edits can bypass the rule that links must come through `{{campaign_url}}`.

**Recommended fix:** Detect domain-shaped text independently of the current suffix list, with explicit handling for numeric expressions. Apply the same policy across all copy fields and add these cases to the existing adversarial validation tests.

### F4. Exhausted queue jobs do not finalize their campaign state

**Severity: high · Confirmed by reproduction**

**Reference:** [generate.go:90](./internal/campaign/generate.go:90), [generate.go:452](./internal/campaign/generate.go:452), [jobs.go:81](./internal/jobs/jobs.go:81)

Generation returns infrastructure and job-context errors for River to retry. Nothing reconciles the campaign when River eventually discards the exhausted job.

Using an accelerated timeout and one allowed attempt, the probe observed a discarded River job while its campaign remained `generating`.

**Why it matters:** The campaign becomes stuck without a terminal generation event. Regeneration is also blocked by its current state.

**Recommended fix:** Add final-attempt handling plus reconciliation between domain states and finalized queue jobs. Reconciliation is necessary when the database outage itself prevents writing the failure state. Test timeout exhaustion and recovery after database failures.

### F5. The audited local toolchain has known security advisories

**Severity: high · Confirmed scanner finding; deployment exposure unverified**

**Reference:** [go.mod:3](./go.mod:3), [Dockerfile:2](./Dockerfile:2)

`govulncheck` identified the local compiler as **Go 1.26.2** and reported **26 symbol-reachable standard-library advisories**. Examples include an HTTP/2 transport infinite loop and certificate-trust fixes. These reports establish affected code paths, not exploitability of every advisory in this application. [HTTP/2 advisory](https://pkg.go.dev/vuln/GO-2026-4918), [certificate advisory](https://pkg.go.dev/vuln/GO-2026-6809).

The floating `golang:1.26-alpine` build tag does not establish which patch a cached deployment build uses.

**Why it matters:** Passing application tests does not address vulnerabilities in the compiler’s bundled standard library.

**Recommended fix:** Upgrade the build and test toolchain to a patched supported release, at least 1.26.10 if remaining on 1.26. Pin and record the build version, then scan the release artifact. No symbol-reachable third-party dependency advisory was reported.

## 3. Bugs and correctness

### F6. Retries can change the payload associated with an idempotency key

**Severity: medium · Reproduced when recipient data changes**

**Reference:** [delivery.go:84](./internal/store/delivery.go:84), [send.go:106](./internal/delivery/send.go:106)

Each attempt reloads recipient names and contact information, then renders the payload again.

The probe simulated “send, then drop connection,” changed the recipient’s first name, and retried. The provider rejected the changed payload with HTTP 422 under the existing idempotency key. The application marked an already-delivered message `failed`.

**Why it matters:** Idempotent replay requires a stable payload. This becomes a production issue when recipient records can change during delivery.

**Recommended fix:** Persist an immutable payload snapshot before the first provider call and reuse it on retries. Continue checking consent separately against current data. This adds storage and retention responsibilities but makes replay deterministic.

### F7. Reading an error body can override a permanent HTTP status

**Severity: medium · Confirmed by reproduction**

**Reference:** [send.go:225](./internal/delivery/send.go:225)

The provider response body is read before the HTTP status is classified. If reading fails, classification receives only the read error.

A probe returned HTTP 400 with a broken body. The result became retryable `conn_reset`.

**Why it matters:** Known permanent failures are retried, contrary to the documented retry policy.

**Recommended fix:** Classify non-success statuses before reading their bodies. Preserve the status classification even if optional body consumption fails. Require successful body parsing only for successful responses.

### F8. HTTP shutdown returns before active requests finish

**Severity: medium · Confirmed by reproduction**

**Reference:** [main.go:122](./cmd/app/main.go:122), [worker.go:131](./cmd/app/worker.go:131)

`Shutdown` runs in a goroutine, while the main path returns as soon as `ListenAndServe` reports `ErrServerClosed`. It never waits for shutdown to finish.

The probe observed `serve` returning while a handler remained active.

**Why it matters:** Deployment termination can interrupt requests despite the apparent ten-second drain period. API database cleanup can also start while handlers still need the pool.

**Recommended fix:** Wait for shutdown completion before returning and closing dependencies. Handle shutdown timeout explicitly. Share this lifecycle helper between the API and mock servers to prevent the duplicated implementations from diverging.

### F9. SSE connections have no bounded write deadline

**Severity: medium · Confirmed configuration gap; resource exhaustion not load-tested**

**Reference:** [events.go:149](./internal/httpapi/events.go:149), [events.go:181](./internal/httpapi/events.go:181)

The stream clears the server write deadline permanently. Subsequent writes and flushes receive no replacement deadline.

**Why it matters:** A connected client that stops reading can retain a blocked handler and connection. Heartbeats do not resolve a blocked socket write.

**Recommended fix:** Set a short write deadline before each event batch and heartbeat, refreshing it after successful writes. Add a slow-reader integration test.

## 4. Readability and maintainability

**No additional standalone findings.** Package responsibilities, naming, comments, and most control flow are clear.

The main maintainability concern is the lifecycle logic behind F2 and F4: queue state and campaign state can diverge without an explicit reconciliation mechanism. Address those specific paths before considering broader refactors.

## 5. Architecture and design

### F10. The channel rate limit applies per process

**Severity: medium · Confirmed design property; risk depends on deployment topology**

**Reference:** [worker.go:69](./cmd/app/worker.go:69), [limiter.go:34](./internal/delivery/limiter.go:34)

Each worker process creates independent 100-message-per-second limiters. Two processes can therefore dispatch approximately 200 messages per second on the same channel.

**Why it matters:** Horizontal scaling or overlapping rollout instances can violate the stated channel-wide limit.

**Recommended fix:** Either enforce and document a singleton dispatcher deployment, including rollout behavior, or coordinate rate limits across processes. Singleton dispatch is simpler; shared coordination supports scaling at greater operational cost.

### F11. A stopped worker can leave old queued messages outside “attention”

**Severity: medium · Confirmed query behavior**

**Reference:** [delivery.go:214](./internal/store/delivery.go:214), [server.go:158](./internal/httpapi/server.go:158)

A newly queued message has zero attempts and no `next_attempt_at`. The attention query excludes it regardless of age. Meanwhile, API readiness checks database connectivity and migrations, so it can remain healthy when delivery workers are stopped.

**Why it matters:** An entire unsent backlog can accumulate without appearing in the attention results or affecting API readiness.

**Recommended fix:** Detect never-attempted messages using queue age, and expose worker heartbeat, oldest-job age, and queue backlog monitoring. Keep API readiness focused on the API, but provide separate worker health and alerting. Add coverage for an aged message with zero attempts.

> How is this plan to continue the project:
>
> User context
>
> Attachments:
> - # Plan: Vue frontend, eval harness, metrics and health, CI ## Context The backe…: `~/.codex/attachments/2c1f11e3-ae82-4dea-a2a7-e2bd06932239/Pasted text.txt`

<details><summary>2 previous messages</summary>

> I’ll read the plan and compare it with the current implementation and review findings, focusing on sequencing, scope, and release blockers.
>
> <details><summary>Explored 11 files, a search, ran 3 commands, searched the web</summary>
>
> <details><summary>Explored 7 files</summary>
>
> - Read `./CLAUDE.md`
> - Read `~/.codex/attachments/2c1f11e3-ae82-4dea-a2a7-e2bd06932239/Pasted text.txt`
> - Read `./DECISIONS.md`
> - Read `./docs/ARCHITECTURE.md`
> - Read `./internal/store/migrate.go`
> - Read `./internal/campaign/ops.go`
> - Read `./internal/httpapi/generate.go`
>
> </details>
>
> - Ran `cat internal/httpapi/{campaigns.go,events.go,delivery.go}; cat internal/store/meta.go; rg -n 'serveOn|not_started|ToolchainPinned|reconcil|Snapshot|payload|MaxAttempts|last_processed' cmd internal -g '*.go' -g '!**/testdata/**'`
>
> - Ran `git status --short; rg --files -g '!go.sum' -g '!.env*' -g '!**/testdata/**'; cat go.mod Dockerfile Makefile`
>
> - Ran `rg -n 'serveOn|not_started|reconcil|Snapshot|Payload|payload|terminal|final attempt|job.Attempt' cmd/app internal/{campaign,delivery,store} -g '*.go' -g '!**/*_test.go'; sed -n '75,172p' internal/delivery/send.go; sed -n '152,218p' cmd/app/worker.go; sed -n '92,118p' internal/store/migrate.go; sed -n '200,232p' internal/store/delivery.go; sed -n '127,158p' cmd/app/worker.go; sed -n '1,120p' internal/campaign/validate.go; sed -n '1,125p' docker-compose.yml`
>
> <details><summary>Explored 6 files, a search</summary>
>
> - Searched for `single|singleton|replica|dispatch cutoff|discard|snapshot|mutable|payload|reconcil` in `ARCHITECTURE.md`
> - Read `./internal/httpapi/campaigns.go`
> - Read `./internal/campaign/validate.go`
> - Read `./internal/campaign/generate.go`
> - Read `./internal/store/migrate.go`
> - Read `./internal/httpapi/generate.go`
> - Read `./internal/delivery/send.go`
>
> </details>
>
> Searched the web for `https://github.com/actions/setup-go/blob/main/docs/advanced-usage.md`
>
> </details>
</details>

**This is a good continuation plan. I would keep the four work streams, but tighten several contracts before implementation.** The frontend choices are appropriately scoped, reusing `Generator.Run` for evaluation is sensible, and mock-only agent/CI execution preserves the credential boundary.

After comparing the plan with the current source, these are the changes I would make:

1. **Replace “backend complete” with an explicit remaining-issues list.**

   Several review fixes are now present, including the toolchain pin, completion retry, consent claim check, shutdown helper, and `not_started` detection. However, two earlier findings remain visible:

   - Exhausted generation jobs can still leave campaigns `generating`: [generate.go:452](./internal/campaign/generate.go:452).
   - Retries still rebuild payloads from mutable recipient data: [send.go:198](./internal/delivery/send.go:198).

   Put these into a small prerequisite step, or explicitly document their deferral. Metrics and a frontend will expose these failures without resolving them.

2. **Fix the dashboard polling contract.**

   Polling only `/stats` and `/messages` cannot discover that the campaign changed from `sending` to `completed`. Neither response contains campaign state. The audit trail will also remain stale.

   Poll the campaign view as well, then refresh stats and attention results. Continue observing cancelled campaigns while messages remain in flight, since cancellation does not immediately finalize an active provider call.

   Specify non-overlapping polls, cancellation on navigation, and backoff after failures.

3. **Write down the exact edit request and error paths.**

   The revisions endpoint requires:

   ```json
   {
     "base_revision_id": "...",
     "version": 3,
     "revision": {
       "segment": {},
       "content": {
         "sms": {"body": "..."},
         "email": {"subject": "...", "body": "..."}
       }
     }
   }
   ```

   The segment must be included even when only copy changes. Edit validation paths are prefixed, such as `/revision/content/sms/body`, rather than `/sms/body`: [campaigns.go:228](./internal/httpapi/campaigns.go:228).

   Also, a 409 should preserve the user’s unsaved edits while offering reload or comparison. Automatically reloading must not silently erase their work.

4. **Define generation recovery across refreshes and regeneration.**

   Using the returned `events_url` is important because it starts at the current generation. Starting from `/events` without that cursor can replay an older generation and stop at its terminal event.

   The plan needs explicit behavior for:

   - Refreshing during a second generation.
   - Reconnecting after a dropped stream.
   - Resetting reducer state when regeneration begins.
   - Reconciling with the campaign view after an unexpected stream end.

   Make the current generation’s starting cursor recoverable, preferably through the campaign response. Test these scenarios through the nginx proxy, not only the standalone parser.

5. **Strengthen the eval’s definition of correctness.**

   Checking that a rule contains expected conditions is insufficient. An incorrect `match: "any"` can contain every expected condition while selecting a much broader audience.

   Likewise, `eligible <= total` does **not** prove consent enforcement. A query that omitted consent entirely could still satisfy it.

   Add:

   - Expected conjunction/disjunction semantics.
   - Known included and excluded fixture recipients.
   - Explicit exclusion of recipients without channel consent.
   - A fixed `Generator.Now` aligned with the seeded evaluation anchor.
   - Separate transport retries and validation attempts.
   - Isolated evaluation databases with cleanup.

   Injection cases should accept safe valid output when appropriate. Refusal is not the only successful defensive response. Keep mock results clearly separate from model-quality results, as already proposed.

6. **Correct the metrics acceptance criteria.**

   Process counters cannot generally sum to the dashboard’s persisted totals. They reset on restart, cover multiple campaigns, and are not transactional with database updates.

   Define counters as observed process activity. Use database-derived gauges when the goal is current persisted message counts.

   For terminal counters, observe whether the guarded database update actually applied before incrementing. Add a duplicate-job test alongside the success/retry/permanent-failure tests.

   Also distinguish generation wall-clock latency from the sum of individual LLM-call latencies. Token usage alone should not be labelled monetary cost.

7. **Adjust the health work to the existing implementation and image.**

   `/readyz` already checks River migration validity through `store.Pending`: [migrate.go:106](./internal/store/migrate.go:106). Another table query would not establish that a worker is consuming jobs.

   Keep these separate:

   - API readiness: database and schema availability.
   - Worker readiness: startup and dependency availability.
   - Operational progress: backlog age, queue depth, and processing activity.

   Specify how Docker healthchecks run in the distroless app image. It has no shell or curl, so an application healthcheck subcommand is a straightforward option.

8. **Create minimal CI early; finish the expanded CI last.**

   A small Go test/vet and frontend build workflow will catch integration drift during development. Add fuzzing, eval artifacts, container smoke tests, and dependency automation afterward.

   The proposed toolchain selection is valid: current `setup-go` documentation says `go-version-file` prefers the `toolchain` directive when present. [Official documentation](https://github.com/actions/setup-go/blob/main/docs/advanced-usage.md).

   For the network-isolation check, allow loopback and test PostgreSQL access. Disable external egress after dependency installation.

I would use this implementation order:

1. Resolve or explicitly defer the remaining backend findings; freeze API examples.
2. Add minimal CI and the README quick start.
3. Implement frontend, eval, and metrics in parallel with named file ownership.
4. Integrate Compose and Makefile changes.
5. Run browser acceptance tests and container smoke tests, then expand CI.

For the case submission, keep `goleak` optional. Prioritize a complete, recoverable marketer workflow, trustworthy eval results, and a reproducible startup path.
