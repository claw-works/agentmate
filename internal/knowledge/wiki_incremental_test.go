package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/ownership"
)

// Incremental compilation exists because the full path emits the whole wiki in one
// model reply, which caps corpus size hard: the output budget has already been raised
// twice and there is no further headroom. These tests pin the properties that make
// reuse trustworthy — the impact set is computed rather than guessed, nothing silently
// vanishes, and a request for incremental never turns into a full rebuild.

// incrementalSnapshot has three documents so a change can touch one page while
// leaving others genuinely untouched.
func incrementalSnapshot(overviewBody, retentionBody, glossaryBody string) SubmitSnapshotRequest {
	return SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: incr-kb\nprofile: incr-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\n" + overviewBody},
		{Path: "raw/retention.md", Content: "# Retention\n\n" + retentionBody},
		{Path: "raw/glossary.md", Content: "# Glossary\n\n" + glossaryBody},
	}}
}

// baseWikiReply is the parent build: three pages, one per source document, plus a
// link from overview into retention so the one-hop closure has something to find.
func baseWikiReply() string {
	return wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "The system in brief.",
			[]string{"raw/overview.md"},
			[]map[string]any{{"target_path": "wiki/retention.md", "link_type": LinkElaborates}}),
		wikiPage("wiki/retention.md", PageKindConcept, "Retention", "Retention is 30 days.",
			[]string{"raw/retention.md"}, nil),
		wikiPage("wiki/glossary.md", PageKindEntity, "Glossary", "Terms used here.",
			[]string{"raw/glossary.md"}, nil),
	)
}

func seedIncrementalSource(t *testing.T, ctx context.Context, service *Service, owner ownership.Owner, name string) *KnowledgeSource {
	t.Helper()
	source := createIntegrationSource(t, ctx, service, owner, name)
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID,
		incrementalSnapshot("Original overview.\n", "Retention is 30 days.\n", "Terms.\n")); err != nil {
		t.Fatalf("submit snapshot: %v", err)
	}
	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	return refreshed
}

func planFromEvents(t *testing.T, events []BuildEvent) IncrementalPlan {
	t.Helper()
	for _, event := range events {
		if event.EventType != BuildEventPlanned || len(event.Payload) == 0 {
			continue
		}
		var plan IncrementalPlan
		if err := json.Unmarshal(event.Payload, &plan); err != nil {
			t.Fatalf("decode plan: %v", err)
		}
		return plan
	}
	t.Fatalf("no planned event carried a plan; the reuse decision left no audit trail")
	return IncrementalPlan{}
}

// TestIncrementalRecompilesOnlyImpactedPages is the whole point: change one source
// document and only the pages resting on it get sent to the model.
func TestIncrementalRecompilesOnlyImpactedPages(t *testing.T) {
	ctx := context.Background()
	// Second reply rewrites only wiki/retention.md, which is what the plan should
	// have asked for.
	updated := wikiReply(
		wikiPage("wiki/retention.md", PageKindConcept, "Retention", "Retention is now 90 days.",
			[]string{"raw/retention.md"}, nil),
		// overview is pulled in by the one-hop rule because it links to retention, so
		// the compiler returns it too.
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "The system in brief, updated.",
			[]string{"raw/overview.md"},
			[]map[string]any{{"target_path": "wiki/retention.md", "link_type": LinkElaborates}}),
	)
	client := &scriptedClient{replies: []string{baseWikiReply(), updated}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-impact")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-impact-kb")
	parent := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if parent.Status != BuildStatusSucceeded {
		t.Fatalf("parent build failed: %s / %s", parent.Error, parent.CheckFailures)
	}

	// Change exactly one document.
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID,
		incrementalSnapshot("Original overview.\n", "Retention is 90 days.\n", "Terms.\n")); err != nil {
		t.Fatalf("submit changed snapshot: %v", err)
	}

	build := compileNow(t, ctx, service, worker, owner,
		CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("incremental build failed: %s / %s", build.Error, build.CheckFailures)
	}
	if build.ParentBuildID == nil || *build.ParentBuildID != parent.ID {
		t.Fatalf("incremental build must record its parent, got %v", build.ParentBuildID)
	}
	if build.Mode != BuildModeIncremental {
		t.Fatalf("mode must stay incremental, got %s", build.Mode)
	}

	events, err := service.ListBuildEvents(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	plan := planFromEvents(t, events)
	if got := plan.RevisionDiff.Changed; len(got) != 1 || got[0] != "raw/retention.md" {
		t.Fatalf("expected exactly raw/retention.md changed, got %v", got)
	}
	if len(plan.RevisionDiff.Added) != 0 || len(plan.RevisionDiff.Removed) != 0 {
		t.Fatalf("expected no additions or removals, got %+v", plan.RevisionDiff)
	}
	// The impact closure: the page citing the changed document, plus the one page
	// linking to it. glossary rests on an untouched document and must stay out.
	if !containsString(plan.RecompiledPaths, "wiki/retention.md") {
		t.Fatalf("the page citing the changed document must be recompiled, got %v", plan.RecompiledPaths)
	}
	if !containsString(plan.RecompiledPaths, "wiki/overview.md") {
		t.Fatalf("the page linking to an impacted page must be pulled in, got %v", plan.RecompiledPaths)
	}
	if containsString(plan.RecompiledPaths, "wiki/glossary.md") {
		t.Fatalf("a page resting on untouched sources must not be recompiled, got %v", plan.RecompiledPaths)
	}
	if !containsString(plan.ReusedPaths, "wiki/glossary.md") {
		t.Fatalf("the untouched page must be reused, got %v", plan.ReusedPaths)
	}

	if build.PagesReused != 1 {
		t.Fatalf("expected exactly one reused page, got %d", build.PagesReused)
	}
	// Three content pages plus the regenerated index and log, same as the parent.
	if build.PagesWritten != 5 {
		t.Fatalf("expected 5 pages in the merged build, got %d", build.PagesWritten)
	}

	// The reused page carries its origin, which is the only way to tell later which
	// text this model run actually produced.
	glossary, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/glossary.md")
	if err != nil {
		t.Fatalf("get glossary: %v", err)
	}
	if glossary.DerivedFromBuildID == nil || *glossary.DerivedFromBuildID != parent.ID {
		t.Fatalf("a reused page must record the build it came from, got %v", glossary.DerivedFromBuildID)
	}
	if len(glossary.Citations) != 1 || glossary.Citations[0].DocumentID == nil {
		t.Fatalf("reuse must carry citations forward and keep them resolved, got %+v", glossary.Citations)
	}
	// And the recompiled page really is the new text, not the copy.
	retention, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/retention.md")
	if err != nil {
		t.Fatalf("get retention: %v", err)
	}
	if !strings.Contains(retention.Content, "90 days") {
		t.Fatalf("recompiled page kept stale content: %q", retention.Content)
	}
	if retention.DerivedFromBuildID != nil {
		t.Fatalf("a recompiled page must not claim to be derived from the parent")
	}
	// Links must still close after merging copied pages with fresh ones.
	if build.CheckStatus != CheckStatusPassed {
		t.Fatalf("check must pass on the merged graph, got %s: %s", build.CheckStatus, build.CheckFailures)
	}
}

// TestIncrementalWithNoSourceChangeCallsNoModel covers the cheapest correct outcome.
// Nothing changed, so nothing needs compiling — and that has to be visible rather
// than looking like a build that mysteriously cost nothing.
func TestIncrementalWithNoSourceChangeCallsNoModel(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-nochange")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-nochange-kb")
	parent := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	callsAfterParent := client.calls

	// force is needed to reach this path at all: enqueue short-circuits an unchanged
	// revision. What is under test here is the compile-side empty diff, which is still
	// reachable when content is reverted — the revision ID then differs from the
	// parent's while the documents are identical again.
	build := compileNow(t, ctx, service, worker, owner,
		CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental, Force: true})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected success, got %s: %s", build.Status, build.Error)
	}
	if client.calls != callsAfterParent {
		t.Fatalf("an empty diff must not call the model, calls went %d -> %d", callsAfterParent, client.calls)
	}
	if build.PagesReused != 3 {
		t.Fatalf("expected all three content pages reused, got %d", build.PagesReused)
	}
	if build.OutputTokens != 0 || build.CostMicros != 0 {
		t.Fatalf("a build that called no model must record no spend, got %d tokens / %d micros",
			build.OutputTokens, build.CostMicros)
	}
	// index and log are regenerated even here: the index has to describe this build,
	// and the log has to record that this build reused everything.
	if build.PagesWritten != 5 {
		t.Fatalf("expected 3 reused pages plus a fresh index and log, got %d", build.PagesWritten)
	}
	index, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/index.md")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	if index.DerivedFromBuildID != nil {
		t.Fatalf("the index must be regenerated, not copied from build %s", parent.ID)
	}
	for _, path := range []string{"wiki/overview.md", "wiki/retention.md", "wiki/glossary.md"} {
		if !strings.Contains(index.Content, path) {
			t.Fatalf("the regenerated index must cover reused pages too, missing %s in:\n%s", path, index.Content)
		}
	}
}

// TestIncrementalDeletesPageWhenSourceRemoved covers the case that made an explicit
// delete flag necessary: a page whose only source is gone must go, and a page the
// model merely omits must not.
func TestIncrementalDeletesPageWhenSourceRemoved(t *testing.T) {
	ctx := context.Background()
	// glossary's source is removed. The compiler deletes that page and, because
	// nothing else cited the removed document, returns nothing else.
	afterRemoval := wikiReply(map[string]any{
		"path": "wiki/glossary.md", "kind": PageKindEntity, "title": "Glossary",
		"content": "", "delete": true, "citations": []map[string]any{},
	})
	client := &scriptedClient{replies: []string{baseWikiReply(), afterRemoval}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-delete")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-delete-kb")
	if parent := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); parent.Status != BuildStatusSucceeded {
		t.Fatalf("parent build failed: %s", parent.Error)
	}

	// Drop raw/glossary.md entirely.
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: incr-kb\nprofile: incr-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\nOriginal overview.\n"},
		{Path: "raw/retention.md", Content: "# Retention\n\nRetention is 30 days.\n"},
	}}); err != nil {
		t.Fatalf("submit snapshot without glossary: %v", err)
	}

	build := compileNow(t, ctx, service, worker, owner,
		CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected success, got %s: %s / %s", build.Status, build.Error, build.CheckFailures)
	}

	events, err := service.ListBuildEvents(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	plan := planFromEvents(t, events)
	if got := plan.RevisionDiff.Removed; len(got) != 1 || got[0] != "raw/glossary.md" {
		t.Fatalf("expected raw/glossary.md removed, got %v", got)
	}
	deleted := false
	for _, event := range events {
		if event.EventType == BuildEventPageDeleted && event.PagePath == "wiki/glossary.md" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("a deleted page must leave an event; a silent disappearance is unauditable")
	}

	pages, err := service.ListPages(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	for _, page := range pages.Items {
		if page.Path == "wiki/glossary.md" {
			t.Fatal("the page whose only source was removed must be gone")
		}
	}
	// The pages resting on surviving documents are still here and still reused.
	if build.PagesReused != 2 {
		t.Fatalf("expected the two untouched pages reused, got %d", build.PagesReused)
	}
	index, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/index.md")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	if strings.Contains(index.Content, "wiki/glossary.md") {
		t.Fatalf("the regenerated index still lists the deleted page:\n%s", index.Content)
	}
}

// TestIncrementalKeepsPageTheCompilerDidNotReturn pins the omission rule. A page
// scheduled for rewrite that comes back missing is kept unchanged rather than
// dropped: stale text is recoverable, a hole in the graph is not, and the model
// declining to answer is not evidence that the page should not exist.
func TestIncrementalKeepsPageTheCompilerDidNotReturn(t *testing.T) {
	ctx := context.Background()
	// retention was scheduled for rewrite; the compiler answers about overview only.
	partial := wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Only the overview came back.",
			[]string{"raw/overview.md"},
			[]map[string]any{{"target_path": "wiki/retention.md", "link_type": LinkElaborates}}),
	)
	client := &scriptedClient{replies: []string{baseWikiReply(), partial}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-partial")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-partial-kb")
	if parent := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); parent.Status != BuildStatusSucceeded {
		t.Fatalf("parent build failed: %s", parent.Error)
	}
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID,
		incrementalSnapshot("Original overview.\n", "Retention is 90 days.\n", "Terms.\n")); err != nil {
		t.Fatalf("submit changed snapshot: %v", err)
	}

	build := compileNow(t, ctx, service, worker, owner,
		CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("expected success, got %s: %s / %s", build.Status, build.Error, build.CheckFailures)
	}
	retention, err := service.GetPage(ctx, owner.Account(), build.ID, "wiki/retention.md")
	if err != nil {
		t.Fatalf("the omitted page must survive: %v", err)
	}
	if retention.DerivedFromBuildID == nil {
		t.Fatal("the omitted page must be recorded as carried over, not as freshly compiled")
	}
	// And the link into it still closes, which is why keeping it matters.
	if build.CheckStatus != CheckStatusPassed {
		t.Fatalf("check must pass, got %s: %s", build.CheckStatus, build.CheckFailures)
	}
}

// TestIncrementalWithoutParentIsRefused pins the refusal to downgrade. Silently
// compiling everything would teach the caller that incremental costs as much as full.
func TestIncrementalWithoutParentIsRefused(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, _ := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-noparent")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-noparent-kb")

	_, err := service.EnqueueCompile(ctx, owner, CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental})
	if !errors.Is(err, ErrNoParentBuild) {
		t.Fatalf("expected ErrNoParentBuild, got %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("nothing should have been compiled, got %d model calls", client.calls)
	}
	builds, err := service.ListBuilds(ctx, owner.Account(), source.ID, 20, 0)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if builds.Total != 0 {
		t.Fatalf("a refused request must not queue a build, got %d", builds.Total)
	}
}

// TestIncrementalIsIdempotentWhenSourcesHaveNotMoved is the fix for a chain that would
// otherwise never terminate.
//
// Every incremental build becomes the parent of the next one, so the generic
// input-identity lookup can never match: a caller polling "bring the wiki up to date"
// would mint a fresh build on every call, each reusing everything and compiling
// nothing. Enqueue therefore recognises that an immutable revision already covered by
// the parent leaves nothing to do, and hands the existing build back.
func TestIncrementalIsIdempotentWhenSourcesHaveNotMoved(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-identity")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-identity-kb")
	full := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})

	for attempt := range 3 {
		response := enqueue(t, ctx, service, owner, CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental})
		if !response.Reused || response.Build.ID != full.ID {
			t.Fatalf("attempt %d: expected the existing build %s handed back, got %+v",
				attempt, full.ID, response.Build.ID)
		}
		// And the caller is told why nothing happened, rather than being left to infer
		// it from an unchanged page count.
		if !strings.Contains(strings.Join(response.Warnings, " | "), "have not changed") {
			t.Fatalf("attempt %d: expected an explanation, got %v", attempt, response.Warnings)
		}
	}
	builds, err := service.ListBuilds(ctx, owner.Account(), source.ID, 20, 0)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if builds.Total != 1 {
		t.Fatalf("polling an up-to-date wiki must not accumulate builds, got %d", builds.Total)
	}
	if client.calls != 1 {
		t.Fatalf("expected only the original full compile to call the model, got %d", client.calls)
	}

	// force is the escape hatch when an operator suspects the existing output rather
	// than the sources.
	forced := compileNow(t, ctx, service, worker, owner,
		CompileRequest{SourceID: source.ID, Mode: BuildModeIncremental, Force: true})
	if forced.ID == full.ID {
		t.Fatal("force must produce a new build")
	}
	if forced.Mode != BuildModeIncremental || forced.ParentBuildID == nil || *forced.ParentBuildID != full.ID {
		t.Fatalf("forced build must stay incremental against the full build, got %+v", forced)
	}
}

// TestRevisionDiffAndImpactAreComputedNotGuessed exercises the two deterministic
// primitives directly. They decide what the model is allowed to change, so they must
// not depend on it.
func TestRevisionDiffAndImpactAreComputedNotGuessed(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "incr-primitives")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "incr-primitives-kb")
	parent := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	firstRevision := parent.SourceRevisionID

	// One changed, one added, one removed, one untouched.
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: incr-kb\nprofile: incr-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\nOriginal overview.\n"},
		{Path: "raw/retention.md", Content: "# Retention\n\nRetention is 90 days.\n"},
		{Path: "raw/limits.md", Content: "# Limits\n\nBrand new.\n"},
	}}); err != nil {
		t.Fatalf("submit snapshot: %v", err)
	}
	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}

	diff, err := worker.repo.DiffRevisionDocuments(ctx, owner.Account(), firstRevision, *refreshed.ActiveRevisionID)
	if err != nil {
		t.Fatalf("diff revisions: %v", err)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "raw/limits.md" {
		t.Fatalf("added: %v", diff.Added)
	}
	if len(diff.Changed) != 1 || diff.Changed[0] != "raw/retention.md" {
		t.Fatalf("changed: %v", diff.Changed)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "raw/glossary.md" {
		t.Fatalf("removed: %v", diff.Removed)
	}
	if diff.Unchanged != 1 {
		t.Fatalf("unchanged: %d", diff.Unchanged)
	}
	// Added documents are not "touched": nothing cites a document that did not exist,
	// so no existing page is stale because of it.
	touched := diff.Touched()
	if containsString(touched, "raw/limits.md") {
		t.Fatalf("an added document must not mark existing pages stale, got %v", touched)
	}

	impacted, err := worker.repo.ImpactedPagePaths(ctx, owner.Account(), parent.ID, touched)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	// retention cites the changed doc, glossary cites the removed one, overview links
	// to retention. All three are in; nothing else exists.
	for _, want := range []string{"wiki/retention.md", "wiki/glossary.md", "wiki/overview.md"} {
		if !containsString(impacted, want) {
			t.Fatalf("expected %s in the impact set, got %v", want, impacted)
		}
	}

	// An empty touched set impacts nothing, which is what makes the no-change path
	// free rather than merely cheap.
	empty, err := worker.repo.ImpactedPagePaths(ctx, owner.Account(), parent.ID, nil)
	if err != nil {
		t.Fatalf("impact on empty set: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected no impact, got %v", empty)
	}
}

// TestMergeIncrementalResolvesConflictsTowardRemoval is a unit test on the merge rule:
// fresh output wins over a copy at the same path, and a path both rewritten and
// deleted ends up deleted. Keeping a page the compiler declared unsupported would
// leave a claim standing that nothing backs.
func TestMergeIncrementalResolvesConflictsTowardRemoval(t *testing.T) {
	parent := "parent-build"
	reused := prepareReusedPages(ReuseInput{ParentBuildID: parent, Pages: []WikiPage{
		{Path: "wiki/a.md", Content: "old a"},
		{Path: "wiki/b.md", Content: "old b"},
		{Path: "wiki/c.md", Content: "old c"},
	}})
	compiled := []WikiPage{
		{Path: "wiki/b.md", Content: "new b"},
		{Path: "wiki/d.md", Content: "new d"},
	}

	merged, reusedCount := mergeIncremental(reused, compiled, []string{"wiki/c.md"})
	byPath := make(map[string]WikiPage, len(merged))
	for _, page := range merged {
		byPath[page.Path] = page
	}
	if len(merged) != 3 {
		t.Fatalf("expected a, b, d, got %d pages", len(merged))
	}
	if _, gone := byPath["wiki/c.md"]; gone {
		t.Fatal("a deleted path must not survive the merge")
	}
	if byPath["wiki/b.md"].Content != "new b" {
		t.Fatalf("fresh output must win at a shared path, got %q", byPath["wiki/b.md"].Content)
	}
	if byPath["wiki/b.md"].DerivedFromBuildID != nil {
		t.Fatal("a recompiled page must not be recorded as derived from the parent")
	}
	if byPath["wiki/a.md"].DerivedFromBuildID == nil {
		t.Fatal("an untouched page must record where it came from")
	}
	if reusedCount != 1 {
		t.Fatalf("only wiki/a.md was actually reused, got %d", reusedCount)
	}

	// Rewritten and deleted at once resolves toward deletion.
	merged, _ = mergeIncremental(reused, []WikiPage{{Path: "wiki/c.md", Content: "resurrected"}}, []string{"wiki/c.md"})
	for _, page := range merged {
		if page.Path == "wiki/c.md" {
			t.Fatal("a path both rewritten and deleted must end up deleted")
		}
	}
}
