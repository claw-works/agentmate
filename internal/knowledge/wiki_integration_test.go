package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/claw-works/agentmate/internal/llm"
	"github.com/claw-works/agentmate/internal/ownership"
)

// scriptedClient drives compilation from fixed replies, so the tests exercise the
// real orchestration — queueing, leases, retries, check gating, activation, diff,
// rollback — without depending on a model being reachable or on it saying the same
// thing twice.
type scriptedClient struct {
	replies []string
	calls   int
	// err, when set, fails every call. errs, when set, fails specific calls by
	// index, which is how retry behaviour is scripted.
	err  error
	errs map[int]error
}

func (c *scriptedClient) Complete(_ context.Context, _ []llm.Message, _ ...llm.Option) (*llm.Completion, error) {
	call := c.calls
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	if failure, ok := c.errs[call]; ok {
		return nil, failure
	}
	index := call
	if index >= len(c.replies) {
		index = len(c.replies) - 1
	}
	return &llm.Completion{
		Content: c.replies[index],
		Model:   c.Model(),
		Usage:   llm.Usage{PromptTokens: 1000, CompletionTokens: 2000, TotalTokens: 3000},
	}, nil
}

func (c *scriptedClient) Model() string    { return "scripted-compiler" }
func (c *scriptedClient) Configured() bool { return true }

// wikiReply renders a compiler reply.
func wikiReply(pages ...map[string]any) string {
	payload, err := json.Marshal(map[string]any{"pages": pages})
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func wikiPage(path, kind, title, body string, citedPaths []string, links []map[string]any) map[string]any {
	citations := make([]map[string]any, 0, len(citedPaths))
	for _, cited := range citedPaths {
		citations = append(citations, map[string]any{
			"document_path": cited,
			"claim":         "claim from " + cited,
			"excerpt":       "excerpt from " + cited,
		})
	}
	page := map[string]any{
		"path": path, "kind": kind, "title": title, "content": body,
		"citations": citations,
	}
	if links != nil {
		page["links"] = links
	}
	return page
}

// wikiSnapshot has two documents so link and citation rules have something to
// resolve against.
func wikiSnapshot(body string) SubmitSnapshotRequest {
	return SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: wiki-kb\nprofile: wiki-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\n" + body},
		{Path: "raw/details.md", Content: "# Details\n\nThe retention window is 30 days.\n"},
	}}
}

// newWikiService returns a service plus a worker sharing its repo. Tests drive the
// worker explicitly rather than relying on the poll loop: the queue's behaviour is
// what is under test, and a background loop would make every assertion a race.
func newWikiService(t *testing.T, ctx context.Context, client llm.Client) (*Service, *Worker) {
	t.Helper()
	pool := integrationPool(t, ctx)
	repo := NewRepo(pool)
	service := NewService(repo)
	// Reviewer intentionally absent: review never gates activation, so the compile
	// path must be fully exercisable without one.
	service.WithLLM(LLMSetup{
		Compiler:     client,
		Independence: llm.IndependenceUnavailable,
		// A configured price so cost accounting is exercised rather than trivially
		// zero: 1000 input + 2000 output per call at these rates is 14000 micros.
		CompilerPricing: llm.Pricing{InputMicrosPer1KTokens: 2000, OutputMicrosPer1KTokens: 6000},
	})
	worker := NewWorker(service, repo, WorkerConfig{
		Concurrency:       1,
		PollInterval:      time.Millisecond,
		LeaseFor:          time.Minute,
		HeartbeatInterval: time.Hour,
		RetryBackoff:      time.Nanosecond,
		MaxRetryBackoff:   time.Nanosecond,
	})
	return service, worker
}

func seedWikiSource(t *testing.T, ctx context.Context, service *Service, owner ownership.Owner, name, body string) *KnowledgeSource {
	t.Helper()
	source := createIntegrationSource(t, ctx, service, owner, name)
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, wikiSnapshot(body)); err != nil {
		t.Fatalf("submit snapshot: %v", err)
	}
	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	return refreshed
}

// enqueue asserts the receipt shape: nothing is compiled when the call returns.
func enqueue(t *testing.T, ctx context.Context, service *Service, owner ownership.Owner, req CompileRequest) *EnqueueCompileResponse {
	t.Helper()
	response, err := service.EnqueueCompile(ctx, owner, req)
	if err != nil {
		t.Fatalf("enqueue compile: %v", err)
	}
	return response
}

// compileNow enqueues and then drains the queue, so a test reads like the story it
// asserts about while still going through the queue.
func compileNow(t *testing.T, ctx context.Context, service *Service, worker *Worker, owner ownership.Owner, req CompileRequest) *BuildRevision {
	t.Helper()
	response := enqueue(t, ctx, service, owner, req)
	if response.Reused {
		return response.Build
	}
	if response.Build.Status != BuildStatusQueued {
		t.Fatalf("a fresh build must start queued, got %s", response.Build.Status)
	}
	build, err := drainQueue(ctx, worker, owner.Account(), response.Build.ID)
	if err != nil {
		t.Fatalf("drain queue: %v", err)
	}
	return build
}

// drainQueue runs claimed builds until the one under test reaches a terminal
// state, so a retry is followed through instead of asserted on mid-flight.
func drainQueue(ctx context.Context, worker *Worker, accountID, buildID string) (*BuildRevision, error) {
	for range 10 {
		if _, err := worker.RunOnce(ctx); err != nil {
			return nil, err
		}
		build, err := worker.repo.GetBuild(ctx, accountID, buildID)
		if err != nil {
			return nil, err
		}
		switch build.Status {
		case BuildStatusSucceeded, BuildStatusFailed, BuildStatusCancelled:
			return build, nil
		}
	}
	return nil, fmt.Errorf("build %s never reached a terminal state", buildID)
}

func goodReply() string {
	return wikiReply(
		wikiPage("wiki/overview.md", PageKindSummary, "Overview", "Summary of the overview.",
			[]string{"raw/overview.md"},
			[]map[string]any{{"target_path": "wiki/retention.md", "link_type": LinkElaborates}}),
		wikiPage("wiki/retention.md", PageKindConcept, "Retention", "Retention is 30 days.",
			[]string{"raw/details.md"}, nil),
	)
}

// TestCompileSucceedsAndActivates is the happy path: the request only queues, a
// worker compiles, check passes, the wiki is committed whole, index and log are
// generated by the platform, and the active pointer moves without anyone approving
// anything.
func TestCompileSucceedsAndActivates(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{goodReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-happy")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-happy-kb", "Overview body.\n")

	// The receipt must not claim any work was done.
	receipt := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID})
	if receipt.Build.Status != BuildStatusQueued || receipt.Activated || receipt.Reused {
		t.Fatalf("enqueue must only queue, got %+v", receipt)
	}
	if client.calls != 0 {
		t.Fatalf("enqueue must not call the model, got %d calls", client.calls)
	}
	if receipt.Queue == nil || receipt.Queue.Queued < 1 {
		t.Fatalf("the receipt must report queue depth, got %+v", receipt.Queue)
	}

	build, err := drainQueue(ctx, worker, owner.Account(), receipt.Build.ID)
	if err != nil {
		t.Fatalf("drain queue: %v", err)
	}
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected succeeded, got %s (error=%q, checks=%s)",
			build.Status, build.Error, build.CheckFailures)
	}
	if build.CheckStatus != CheckStatusPassed {
		t.Fatalf("expected check passed, got %s", build.CheckStatus)
	}
	if !build.IsActive {
		t.Fatal("a build passing check must be activated automatically")
	}
	if build.Attempt != 1 {
		t.Fatalf("expected one attempt, got %d", build.Attempt)
	}
	if build.LeaseOwner != "" || build.LeaseExpiresAt != nil {
		t.Fatalf("a terminal build must not look claimable: owner=%q expires=%v", build.LeaseOwner, build.LeaseExpiresAt)
	}
	if build.StartedAt == nil || build.FinishedAt == nil {
		t.Fatal("queue wait and compile time must stay distinguishable via started_at/finished_at")
	}
	// Cost is recorded, not guessed: 1000 input at 2000/1k plus 2000 output at
	// 6000/1k.
	if build.CostMicros != 14000 {
		t.Fatalf("expected 14000 micros recorded, got %d", build.CostMicros)
	}
	// Two model pages plus the generated index and log.
	if build.PagesWritten != 4 {
		t.Fatalf("expected 4 pages written, got %d", build.PagesWritten)
	}
	// Provenance must be complete: a build is not reproducible, so anything not
	// recorded here is unrecoverable.
	if build.Model != client.Model() || build.CompilerVersion != CompilerVersion ||
		build.PromptVersion != PromptVersion {
		t.Fatalf("incomplete provenance: %+v", build)
	}
	if build.SourceRevisionID != *source.ActiveRevisionID {
		t.Fatal("build must record the raw revision it compiled")
	}
	if build.RawPackageHash == "" {
		t.Fatal("build must record the raw package hash")
	}

	pages, err := service.ListPages(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	byPath := make(map[string]WikiPage, len(pages.Items))
	for _, page := range pages.Items {
		byPath[page.Path] = page
	}
	for _, required := range []string{"wiki/index.md", "wiki/log.md", "wiki/overview.md", "wiki/retention.md"} {
		if _, ok := byPath[required]; !ok {
			t.Fatalf("missing page %s; got %v", required, pages.Items)
		}
	}

	// The log must stay greppable with plain unix tools: that is how an operator
	// reads it when the API is not at hand.
	logPage, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/log.md")
	if err != nil {
		t.Fatalf("get log page: %v", err)
	}
	headings := 0
	for _, line := range strings.Split(logPage.Content, "\n") {
		if strings.HasPrefix(line, "## [") {
			headings++
		}
	}
	if headings < 3 {
		t.Fatalf("expected a greppable event heading per event, got %d in:\n%s", headings, logPage.Content)
	}

	// Citations must resolve to real documents, otherwise a generated page is
	// unverifiable and the whole layer is untrustworthy.
	overview, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/overview.md")
	if err != nil {
		t.Fatalf("get overview page: %v", err)
	}
	if len(overview.Citations) != 1 || overview.Citations[0].DocumentID == nil {
		t.Fatalf("expected one resolved citation, got %+v", overview.Citations)
	}
	// Both link directions are needed for navigation.
	retention, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/retention.md")
	if err != nil {
		t.Fatalf("get retention page: %v", err)
	}
	inbound := 0
	for _, link := range retention.Links {
		if link.Direction == "in" {
			inbound++
		}
	}
	if inbound == 0 {
		t.Fatalf("expected an inbound link on wiki/retention.md, got %+v", retention.Links)
	}

	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if refreshed.ActiveBuildID == nil || *refreshed.ActiveBuildID != build.ID {
		t.Fatal("source active_build_id must point at the activated build")
	}
}

// TestCompileReusesIdenticalInputIdentity pins the idempotency key: same raw
// revision, profile version, compiler, model and prompt means nothing is even
// queued. Content hashing could not do this — the same inputs produce different
// text every time.
func TestCompileReusesIdenticalInputIdentity(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{goodReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-identity")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-identity-kb", "Overview body.\n")

	first := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	second := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID})
	if !second.Reused || second.Build.ID != first.ID {
		t.Fatalf("expected reuse of build %s, got %+v", first.ID, second)
	}
	if client.calls != 1 {
		t.Fatalf("expected exactly one model call, got %d", client.calls)
	}

	// force must bypass reuse, since the reason to force is a suspicion about the
	// previous output rather than a change in inputs.
	forced := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID, Force: true})
	if forced.ID == first.ID {
		t.Fatal("force must produce a new build")
	}
	if client.calls != 2 {
		t.Fatalf("expected a second model call after force, got %d", client.calls)
	}
}

// TestCompileFailsCheckAndWritesNothing is the reason automatic activation is
// safe. A fabricated citation must fail the build, leave no pages behind, leave the
// active pointer where it was — and, crucially, not be retried: recompiling until
// the dice fall right would be relaxing the gate by repetition.
func TestCompileFailsCheckAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	fabricated := wikiReply(
		wikiPage("wiki/ghost.md", PageKindSummary, "Ghost", "Cites a document that does not exist.",
			[]string{"raw/does-not-exist.md"}, nil),
	)
	client := &scriptedClient{replies: []string{fabricated}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-check")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-check-kb", "Overview body.\n")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusFailed || build.CheckStatus != CheckStatusFailed {
		t.Fatalf("expected failed/failed, got %s/%s", build.Status, build.CheckStatus)
	}
	if build.IsActive {
		t.Fatal("a build failing check must never be activated")
	}
	if client.calls != 1 {
		t.Fatalf("a check failure must not be retried, got %d model calls", client.calls)
	}
	var failures []CheckFailure
	if err := json.Unmarshal(build.CheckFailures, &failures); err != nil {
		t.Fatalf("decode check failures: %v", err)
	}
	found := false
	for _, failure := range failures {
		if failure.Rule == ruleCitationResolvable {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a %s failure, got %+v", ruleCitationResolvable, failures)
	}

	// No half-written wiki: an agent cannot tell that pages are missing, so it
	// would answer from an incomplete graph as if it were complete.
	pages, err := service.ListPages(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages.Items) != 0 {
		t.Fatalf("a failed build must write no pages, got %d", len(pages.Items))
	}

	// Activating it explicitly must be refused too: check is the gate regardless
	// of who asks.
	if _, err := service.ActivateBuild(ctx, owner, build.ID); !errors.Is(err, ErrBuildNotActivatable) {
		t.Fatalf("expected ErrBuildNotActivatable, got %v", err)
	}
}

// TestCompileRetriesTransportFailures covers the case that motivated the queue: a
// provider dropping a long connection must not burn a build.
func TestCompileRetriesTransportFailures(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{
		replies: []string{goodReply()},
		errs: map[int]error{
			0: &llm.Error{Role: llm.RoleCompiler, Message: "unexpected EOF", Retryable: true},
		},
	}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-retry")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-retry-kb", "Overview body.\n")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected the retry to succeed, got %s (%s)", build.Status, build.Error)
	}
	if build.Attempt != 2 {
		t.Fatalf("expected two attempts, got %d", build.Attempt)
	}
	if client.calls != 2 {
		t.Fatalf("expected two model calls, got %d", client.calls)
	}
	// The successful commit clears the error, so a succeeded build does not carry
	// a message implying it failed.
	if build.Error != "" {
		t.Fatalf("a succeeded build must not carry an error, got %q", build.Error)
	}
}

// TestCompileGivesUpAfterMaxAttempts bounds the retry budget. Retrying forever
// turns one broken package into permanent spend.
func TestCompileGivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{
		replies: []string{goodReply()},
		err:     &llm.Error{Role: llm.RoleCompiler, Message: "connection reset", Retryable: true},
	}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-giveup")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-giveup-kb", "Overview body.\n")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusFailed {
		t.Fatalf("expected failed, got %s", build.Status)
	}
	if build.Attempt != build.MaxAttempts {
		t.Fatalf("expected the attempt budget to be spent, got %d of %d", build.Attempt, build.MaxAttempts)
	}
	if client.calls != build.MaxAttempts {
		t.Fatalf("expected %d model calls, got %d", build.MaxAttempts, client.calls)
	}
	if !strings.Contains(build.Error, "gave up") {
		t.Fatalf("expected the give-up to be explicit, got %q", build.Error)
	}
	// Every failed attempt is billed, so the recorded cost must be cumulative.
	// Anything else makes the invoice unexplainable.
	if build.CostMicros != 0 {
		t.Fatalf("failed calls reported no usage, so no cost should be recorded, got %d", build.CostMicros)
	}
}

// TestCompileDoesNotRetryTerminalFailures pins the other half of the retry
// decision: a truncated reply will truncate again on the same budget, so a retry
// only doubles the bill for the same outcome.
func TestCompileDoesNotRetryTerminalFailures(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{
		replies: []string{goodReply()},
		err: &llm.Error{Role: llm.RoleCompiler, Status: 200,
			Message: "hit the token limit; raise max_tokens or split the input", Retryable: false},
	}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-terminal")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-terminal-kb", "Overview body.\n")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusFailed {
		t.Fatalf("expected failed, got %s", build.Status)
	}
	if build.Attempt != 1 || client.calls != 1 {
		t.Fatalf("a terminal failure must not be retried: attempt=%d calls=%d", build.Attempt, client.calls)
	}
	if !strings.Contains(build.Error, "token limit") {
		t.Fatalf("expected an actionable error, got %q", build.Error)
	}
}

// TestAbandonedBuildIsReclaimed simulates a worker dying mid-compile: the lease
// expires, another worker takes over, and the abandoned attempt leaves nothing
// behind. Nothing else in the system would ever release that row.
func TestAbandonedBuildIsReclaimed(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{goodReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-reclaim")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-reclaim-kb", "Overview body.\n")
	receipt := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID})

	// A first worker claims and then vanishes without reporting anything.
	dead := NewWorker(service, worker.repo, WorkerConfig{Concurrency: 1, LeaseFor: time.Minute, HeartbeatInterval: time.Hour})
	claimed, err := worker.repo.ClaimNextBuild(ctx, dead.Owner(), time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v (build=%v)", err, claimed)
	}
	if claimed.Status != BuildStatusRunning || claimed.LeaseOwner != dead.Owner() {
		t.Fatalf("claim must lease and mark running, got %+v", claimed)
	}
	// Nothing else can take it while the lease holds — that is the whole point.
	if other, err := worker.repo.ClaimNextBuild(ctx, worker.Owner(), time.Minute); err != nil || other != nil {
		t.Fatalf("a leased build must not be claimable: %v %+v", err, other)
	}

	// Expire the lease the way time would.
	if _, err := pool.Exec(ctx,
		`UPDATE knowledge_build_revisions SET lease_expires_at = NOW() - interval '1 minute' WHERE id = $1`,
		receipt.Build.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	build, err := drainQueue(ctx, worker, owner.Account(), receipt.Build.ID)
	if err != nil {
		t.Fatalf("drain queue: %v", err)
	}
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected the reclaimed build to succeed, got %s (%s)", build.Status, build.Error)
	}
	// The dead worker's claim counted: attempts count claims, not failures, so a
	// build that silently kills its workers still retires.
	if build.Attempt != 2 {
		t.Fatalf("expected the abandoned claim to count as an attempt, got %d", build.Attempt)
	}
	// Exactly one wiki, not two interleaved ones.
	pages, err := service.ListPages(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages.Items) != 4 {
		t.Fatalf("reclaim must leave exactly one wiki, got %d pages", len(pages.Items))
	}
}

// TestHeartbeatLosesLeaseToReclaimer is how a worker learns to stop: once the
// lease has moved, its output must be discarded rather than raced to the commit.
func TestHeartbeatLosesLeaseToReclaimer(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{goodReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-lease")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-lease-kb", "Overview body.\n")
	receipt := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID})

	first, err := worker.repo.ClaimNextBuild(ctx, "worker-a", time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := worker.repo.HeartbeatBuild(ctx, owner.Account(), first.ID, "worker-a", time.Minute); err != nil {
		t.Fatalf("the holder must be able to extend its lease: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE knowledge_build_revisions SET lease_expires_at = NOW() - interval '1 minute' WHERE id = $1`,
		receipt.Build.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if _, err := worker.repo.ClaimNextBuild(ctx, "worker-b", time.Minute); err != nil {
		t.Fatalf("second claim: %v", err)
	}

	if err := worker.repo.HeartbeatBuild(ctx, owner.Account(), first.ID, "worker-a", time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for the displaced worker, got %v", err)
	}
	// And it must not be able to commit either.
	_, err = worker.repo.CommitBuild(ctx, owner, first.ID, []WikiPage{{
		Path: "wiki/x.md", Kind: PageKindSummary, Content: "x", ContentHash: "x",
	}}, nil, buildUsage{LeaseOwner: "worker-a"})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected the commit to be refused, got %v", err)
	}
	pages, err := service.ListPages(ctx, owner.Account(), first.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages.Items) != 0 {
		t.Fatalf("a refused commit must write nothing, got %d pages", len(pages.Items))
	}
}

// TestYieldRefundsAttempt covers graceful shutdown. A rolling deploy must not
// spend the retry budget of every in-flight build: the attempt never got a fair
// chance to fail.
func TestYieldRefundsAttempt(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{goodReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-yield")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-yield-kb", "Overview body.\n")
	receipt := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID})

	claimed, err := worker.repo.ClaimNextBuild(ctx, worker.Owner(), time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", claimed.Attempt)
	}
	if err := worker.repo.YieldBuild(ctx, owner.Account(), claimed.ID, worker.Owner()); err != nil {
		t.Fatalf("yield: %v", err)
	}
	yielded, err := worker.repo.GetBuild(ctx, owner.Account(), receipt.Build.ID)
	if err != nil {
		t.Fatalf("reload build: %v", err)
	}
	if yielded.Status != BuildStatusQueued || yielded.Attempt != 0 || yielded.LeaseOwner != "" {
		t.Fatalf("a yielded build must be immediately claimable with its attempt refunded, got %+v", yielded)
	}

	// And it still compiles normally afterwards.
	build, err := drainQueue(ctx, worker, owner.Account(), receipt.Build.ID)
	if err != nil {
		t.Fatalf("drain queue: %v", err)
	}
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected succeeded, got %s (%s)", build.Status, build.Error)
	}
}

// TestCompileDiffAndRollback covers the two operations that exist because builds
// are not reproducible: explaining a change without re-running it, and reverting
// by moving a pointer.
func TestCompileDiffAndRollback(t *testing.T) {
	ctx := context.Background()
	changed := wikiReply(
		wikiPage("wiki/overview.md", PageKindSummary, "Overview", "A rewritten overview.",
			[]string{"raw/overview.md"}, nil),
		wikiPage("wiki/glossary.md", PageKindEntity, "Glossary", "Retention window: 30 days.",
			[]string{"raw/details.md"}, nil),
	)
	client := &scriptedClient{replies: []string{goodReply(), changed}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-diff")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-diff-kb", "Overview body.\n")

	first := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	second := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID, Force: true})
	if second.Status != BuildStatusSucceeded {
		t.Fatalf("second build failed: %s / %s", second.Error, second.CheckFailures)
	}

	diff, err := service.DiffBuilds(ctx, owner.Account(), first.ID, second.ID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !containsString(diff.Added, "wiki/glossary.md") {
		t.Fatalf("expected wiki/glossary.md added, got %+v", diff)
	}
	if !containsString(diff.Removed, "wiki/retention.md") {
		t.Fatalf("expected wiki/retention.md removed, got %+v", diff)
	}
	if !containsString(diff.Changed, "wiki/overview.md") {
		t.Fatalf("expected wiki/overview.md changed, got %+v", diff)
	}

	// Omitting the baseline compares against the previous succeeded build, which
	// is the question an operator actually asks.
	defaulted, err := service.DiffBuilds(ctx, owner.Account(), "", second.ID)
	if err != nil {
		t.Fatalf("default diff: %v", err)
	}
	if defaulted.FromBuildID != first.ID {
		t.Fatalf("expected default baseline %s, got %s", first.ID, defaulted.FromBuildID)
	}

	// Rollback is activation of an older build; the response reports the outgoing
	// pointer so the rollback itself can be undone.
	rolled, err := service.ActivateBuild(ctx, owner, first.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolled.PreviousBuildID == nil || *rolled.PreviousBuildID != second.ID {
		t.Fatalf("expected previous build %s, got %+v", second.ID, rolled.PreviousBuildID)
	}
	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if refreshed.ActiveBuildID == nil || *refreshed.ActiveBuildID != first.ID {
		t.Fatal("rollback must move the active pointer back")
	}
}

// TestCompileWithoutCompilerIsUnavailable separates an operator configuration gap
// from a defect in the sources: nothing is queued, because nothing about the
// sources was wrong.
func TestCompileWithoutCompilerIsUnavailable(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	service := NewService(NewRepo(pool))
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-nollm")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-nollm-kb", "Overview body.\n")

	if _, err := service.EnqueueCompile(ctx, owner, CompileRequest{SourceID: source.ID}); !errors.Is(err, ErrCompilerUnavailable) {
		t.Fatalf("expected ErrCompilerUnavailable, got %v", err)
	}
	builds, err := service.ListBuilds(ctx, owner.Account(), source.ID, 20, 0)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if builds.Total != 0 {
		t.Fatalf("an unconfigured compiler must not queue a build, got %d", builds.Total)
	}
}

// TestCompileRejectsReservedPagePaths pins the ownership boundary between model
// and platform. A real qwen3.7-plus build wrote its own wiki/index.md, which
// collided with the generated one and failed the whole build on path_unique — a
// foreseeable model behaviour that must not make the system unusable. The page is
// dropped instead, and the drop is recorded rather than hidden.
func TestCompileRejectsReservedPagePaths(t *testing.T) {
	ctx := context.Background()
	reply := wikiReply(
		wikiPage("wiki/index.md", PageKindIndex, "My own index", "- everything", nil, nil),
		wikiPage("wiki/log.md", PageKindLog, "My own log", "stuff happened", nil, nil),
		wikiPage("wiki/overview.md", PageKindSummary, "Overview", "Summary of the overview.",
			[]string{"raw/overview.md"}, nil),
	)
	client := &scriptedClient{replies: []string{reply}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-reserved")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-reserved-kb", "Overview body.\n")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected succeeded, got %s (%s / %s)", build.Status, build.Error, build.CheckFailures)
	}
	// One model page survived, plus the platform's index and log.
	if build.PagesWritten != 3 {
		t.Fatalf("expected 3 pages, got %d", build.PagesWritten)
	}

	// The generated index must be the platform's, not the model's.
	index, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/index.md")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	if strings.Contains(index.Content, "everything") {
		t.Fatalf("the model's index survived: %s", index.Content)
	}
	if !strings.Contains(index.Content, "wiki/overview.md") {
		t.Fatalf("generated index must link every content page, got:\n%s", index.Content)
	}

	events, err := service.ListBuildEvents(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	rejected := make([]string, 0)
	for _, event := range events {
		if event.EventType == BuildEventPageRejected {
			rejected = append(rejected, event.PagePath)
		}
	}
	if len(rejected) != 2 {
		t.Fatalf("expected both reserved paths recorded as rejected, got %v", rejected)
	}
}

// TestCompileFailsWhenEveryPageIsRejected keeps the rejection from turning into a
// silently empty wiki: if nothing usable survives, that is a failed build.
func TestCompileFailsWhenEveryPageIsRejected(t *testing.T) {
	ctx := context.Background()
	reply := wikiReply(
		wikiPage("wiki/index.md", PageKindIndex, "Only an index", "- nothing", nil, nil),
	)
	client := &scriptedClient{replies: []string{reply}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-allrejected")
	defer cleanup()

	source := seedWikiSource(t, ctx, service, owner, "wiki-allrejected-kb", "Overview body.\n")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusFailed {
		t.Fatalf("expected failed, got %s", build.Status)
	}
	if !strings.Contains(build.Error, "no usable pages") {
		t.Fatalf("expected an explicit reason, got %q", build.Error)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// TestEnqueueAlwaysSaysReviewDidNotRun guards a signal that a configuration
// improvement can silently delete.
//
// The warning about review used to fire only when the reviewer shared the
// compiler's provider. Pointing the reviewer at a second vendor therefore removed
// the only thing telling an operator that nothing had been reviewed — while review
// remained unimplemented and review_status remained skipped. Better configuration
// must not reduce what the caller is told about what was verified.
func TestEnqueueAlwaysSaysReviewDidNotRun(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-reviewwarn")
	defer cleanup()

	for _, independence := range []string{
		llm.IndependenceCrossProvider, llm.IndependenceSameProvider, llm.IndependenceUnavailable,
	} {
		service, _ := newWikiService(t, ctx, &scriptedClient{replies: []string{goodReply()}})
		service.WithLLM(LLMSetup{Compiler: &scriptedClient{replies: []string{goodReply()}}, Independence: independence})
		source := seedWikiSource(t, ctx, service, owner, "wiki-reviewwarn-"+independence, "Overview body.\n")

		response := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID})
		joined := strings.Join(response.Warnings, " | ")
		if !strings.Contains(joined, "review is not implemented") {
			t.Errorf("independence=%s: the caller must be told review did not run, got %q", independence, joined)
		}
		// The independence warning is additional information, not a replacement for
		// the one above.
		wantIndependence := independence == llm.IndependenceSameProvider
		if got := strings.Contains(joined, "reviewer independence is"); got != wantIndependence {
			t.Errorf("independence=%s: independence warning present=%v, want %v (%q)",
				independence, got, wantIndependence, joined)
		}
	}
}
