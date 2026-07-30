package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/claw-works/agentmate/internal/llm"
	"github.com/claw-works/agentmate/internal/retrieval"
)

// Wiki retrieval is where a rollback can quietly stop meaning anything: builds are
// immutable and retained, so an index covering more than the active build would serve
// pages from a wiki that was rolled back while the read API served the restored one.
// These tests pin that the index follows the pointer, and that a lag is visible rather
// than looking like a wiki with nothing to say.

func newWikiSearchService(t *testing.T, ctx context.Context, client llm.Client) (*Service, *Worker) {
	t.Helper()
	pool := integrationPool(t, ctx)
	repo := NewRepo(pool)
	// A stub embedder and vector store: what is under test is which build a hit may come
	// from, not embedding quality. Real embeddings would make these tests slow and
	// dependent on a provider being reachable.
	retrievalSvc := retrieval.NewService(
		retrieval.NewRepo(pool), &knowledgeTestVectorStore{}, knowledgeTestEmbedder{})
	service := NewService(repo, retrievalSvc)
	service.WithLLM(LLMSetup{Compiler: client, Independence: llm.IndependenceUnavailable})
	worker := NewWorker(service, repo, WorkerConfig{
		Concurrency: 1, LeaseFor: time.Minute, HeartbeatInterval: time.Hour,
	})
	return service, worker
}

// TestWikiSearchFindsPagesAndCarriesCitations covers the two-level query. Level one is
// the synthesised page; level two is the citation that makes its claims checkable, which
// is why citations travel with the hit rather than needing a second round trip.
func TestWikiSearchFindsPagesAndCarriesCitations(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, worker := newWikiSearchService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-search")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "wiki-search-kb")
	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("compile failed: %s / %s", build.Error, build.CheckFailures)
	}

	// Nothing indexed yet: the wiki is active but not searchable, and that must read as a
	// lag rather than as an empty knowledge base.
	statuses, err := service.WikiIndexStatuses(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("index status: %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Stale {
		t.Fatalf("an active but unindexed wiki must report stale, got %+v", statuses)
	}

	indexed, err := service.IndexActiveWikiBuilds(ctx, owner, source.ID)
	if err != nil {
		t.Fatalf("index wiki: %v", err)
	}
	if len(indexed.Indexed) != 1 {
		t.Fatalf("expected one source indexed, got %+v", indexed)
	}
	result := indexed.Indexed[0]
	if result.BuildID != build.ID {
		t.Fatalf("expected the active build indexed, got %s", result.BuildID)
	}
	if result.ChunksIndexed == 0 {
		t.Fatalf("nothing was indexed: %+v", result)
	}
	// The build log is a transcript of compiling, not knowledge about the domain. Indexing
	// it would let an agent cite "page_written wiki/x.md" as a fact.
	if result.PagesSkipped != 1 {
		t.Fatalf("expected the log page to be skipped, got %d skipped", result.PagesSkipped)
	}

	statuses, err = service.WikiIndexStatuses(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("index status: %v", err)
	}
	if statuses[0].Stale {
		t.Fatalf("after indexing the active build the status must be current, got %+v", statuses[0])
	}

	found, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "retention", TopK: 5})
	if err != nil {
		t.Fatalf("search wiki: %v", err)
	}
	if len(found.Items) == 0 {
		t.Fatalf("expected a hit, got none (note=%q)", found.Note)
	}
	hit := found.Items[0]
	if hit.BuildID != build.ID {
		t.Fatalf("a hit must come from the active build, got %s", hit.BuildID)
	}
	if hit.Path == "" || hit.Kind == "" {
		t.Fatalf("a hit must identify the page it came from: %+v", hit)
	}
	// Citations are the path down to evidence. A hit without them is a plausible paragraph
	// with no way to check it.
	if len(hit.Citations) == 0 {
		t.Fatalf("expected citations on the hit, got %+v", hit)
	}
	for _, citation := range hit.Citations {
		if citation.DocumentPath == "" {
			t.Fatalf("a citation must name the document it points at: %+v", citation)
		}
	}
	// The log page must not be reachable through search.
	for _, item := range found.Items {
		if item.Kind == PageKindLog {
			t.Fatalf("the build log must not be searchable: %+v", item)
		}
	}
	// Page bodies are withheld unless asked for: several full pages defeat the point of
	// retrieving.
	if hit.Content != "" {
		t.Fatalf("content must be opt-in, got %q", hit.Content)
	}
	withContent, err := service.SearchWiki(ctx, owner, SearchWikiRequest{
		Query: "retention", TopK: 5, IncludeContent: true})
	if err != nil {
		t.Fatalf("search with content: %v", err)
	}
	if len(withContent.Items) == 0 || withContent.Items[0].Content == "" {
		t.Fatalf("include_content must return the body, got %+v", withContent.Items)
	}
}

// TestWikiSearchFollowsTheActivePointer is the reason search filters by build instead of
// trusting the index. A rollback moves the pointer; the index still holds the newer
// build's pages, and serving those would contradict every read API.
func TestWikiSearchFollowsTheActivePointer(t *testing.T) {
	ctx := context.Background()
	second := wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Rewritten overview about quotas.",
			[]string{"raw/overview.md"}, nil),
		wikiPage("wiki/quota.md", PageKindConcept, "Quota", "The quota is 42 per minute.",
			[]string{"raw/retention.md"}, nil),
		wikiPage("wiki/glossary.md", PageKindEntity, "Glossary", "Terms used here.",
			[]string{"raw/glossary.md"}, nil),
	)
	client := &scriptedClient{replies: []string{baseWikiReply(), second}}
	service, worker := newWikiSearchService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-rollback")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "wiki-rollback-kb")
	first := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	newer := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID, Force: true})
	if newer.Status != BuildStatusSucceeded {
		t.Fatalf("second compile failed: %s / %s", newer.Error, newer.CheckFailures)
	}
	if _, err := service.IndexActiveWikiBuilds(ctx, owner, source.ID); err != nil {
		t.Fatalf("index newer build: %v", err)
	}

	// The newer build's page is findable.
	before, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "quota", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(before.Items) == 0 {
		t.Fatalf("expected the newer build's page, got none (note=%q)", before.Note)
	}

	// Roll back. The index still holds the newer build, but it is no longer the wiki.
	if _, err := service.ActivateBuild(ctx, owner, first.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	after, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "quota", TopK: 5})
	if err != nil {
		t.Fatalf("search after rollback: %v", err)
	}
	for _, item := range after.Items {
		if item.BuildID == newer.ID {
			t.Fatalf("search served a rolled-back build: %+v", item)
		}
	}
	// And the lag is visible instead of being mistaken for an empty wiki.
	statuses, err := service.WikiIndexStatuses(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("index status: %v", err)
	}
	if !statuses[0].Stale {
		t.Fatalf("after a rollback the index must report stale, got %+v", statuses[0])
	}

	// Reindexing brings the restored wiki back into search, and drops the rolled-back
	// build's rows rather than leaving them to be filtered forever.
	if _, err := service.IndexActiveWikiBuilds(ctx, owner, source.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	restored, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "retention", TopK: 5})
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(restored.Items) == 0 {
		t.Fatalf("expected the restored wiki to be searchable, got none (note=%q)", restored.Note)
	}
	for _, item := range restored.Items {
		if item.BuildID != first.ID {
			t.Fatalf("expected only the restored build, got %+v", item)
		}
	}
}

// TestWikiSearchWithoutActiveBuildExplainsItself keeps a normal state from reading as a
// fault. A knowledge base that has been synced but never compiled has no wiki, and that
// is not an error the caller should handle as one.
func TestWikiSearchWithoutActiveBuildExplainsItself(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, _ := newWikiSearchService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-noactive")
	defer cleanup()

	seedIncrementalSource(t, ctx, service, owner, "wiki-noactive-kb")

	response, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "anything", TopK: 5})
	if err != nil {
		t.Fatalf("search must not fail when nothing is compiled: %v", err)
	}
	if len(response.Items) != 0 {
		t.Fatalf("expected no hits, got %d", len(response.Items))
	}
	if !strings.Contains(response.Note, "compile") {
		t.Fatalf("expected an explanation pointing at compilation, got %q", response.Note)
	}
}

// TestWikiSearchCollapsesChunksToPages guards the result budget. A page is chunked for
// embedding, so a long page occupies several of the top slots; reporting each chunk would
// make one page look like several answers and crowd the rest out.
func TestWikiSearchCollapsesChunksToPages(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("Retention policy details and rationale. ", 200)
	reply := wikiReply(
		wikiPage("wiki/retention.md", PageKindConcept, "Retention", long,
			[]string{"raw/retention.md"}, nil),
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Short overview of retention.",
			[]string{"raw/overview.md"}, nil),
		wikiPage("wiki/glossary.md", PageKindEntity, "Glossary", "Terms.",
			[]string{"raw/glossary.md"}, nil),
	)
	client := &scriptedClient{replies: []string{reply}}
	service, worker := newWikiSearchService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-collapse")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "wiki-collapse-kb")
	if build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); build.Status != BuildStatusSucceeded {
		t.Fatalf("compile failed: %s / %s", build.Error, build.CheckFailures)
	}
	indexed, err := service.IndexActiveWikiBuilds(ctx, owner, source.ID)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if indexed.Indexed[0].ChunksIndexed < 3 {
		t.Fatalf("expected the long page to produce several chunks, got %d total", indexed.Indexed[0].ChunksIndexed)
	}

	found, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "retention policy", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	seen := make(map[string]int)
	for _, item := range found.Items {
		seen[item.Path]++
	}
	for path, count := range seen {
		if count > 1 {
			t.Fatalf("%s appeared %d times; chunks must collapse to one page per hit", path, count)
		}
	}
	// MatchedChunks is not asserted above 1 here. How many chunks of a page rank highly
	// depends on the embedding model, and these tests run against a stub — asserting it
	// would be testing the fixture. What matters and is testable is that however many
	// chunks match, the page appears once.
	if len(found.Items) == 0 {
		t.Fatal("expected hits")
	}
	for _, item := range found.Items {
		if item.MatchedChunks < 1 {
			t.Fatalf("every hit must account for at least one matching chunk: %+v", item)
		}
	}
}

// TestWikiIndexIsIsolatedFromRawNamespace pins the separation. A synthesis and the
// documents it was synthesised from must not compete in one ranking, or the synthesis wins
// and the evidence it rests on disappears from the results.
func TestWikiIndexIsIsolatedFromRawNamespace(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{baseWikiReply()}}
	service, worker := newWikiSearchService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "wiki-namespace")
	defer cleanup()

	source := seedIncrementalSource(t, ctx, service, owner, "wiki-namespace-kb")
	if build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); build.Status != BuildStatusSucceeded {
		t.Fatalf("compile failed: %s", build.Error)
	}
	if _, err := service.IndexActiveRevisions(ctx, owner, source.ID); err != nil {
		t.Fatalf("index raw: %v", err)
	}
	if _, err := service.IndexActiveWikiBuilds(ctx, owner, source.ID); err != nil {
		t.Fatalf("index wiki: %v", err)
	}

	var rawCount, wikiCount int
	if err := pool.QueryRow(ctx,
		`SELECT
		   count(*) FILTER (WHERE namespace = $2),
		   count(*) FILTER (WHERE namespace = $3)
		 FROM retrieval_documents WHERE account_id = $1`,
		owner.Account(), retrieval.NamespaceKnowledge, retrieval.NamespaceKnowledgeWiki,
	).Scan(&rawCount, &wikiCount); err != nil {
		t.Fatalf("count namespaces: %v", err)
	}
	if rawCount == 0 || wikiCount == 0 {
		t.Fatalf("both namespaces must be populated, got raw=%d wiki=%d", rawCount, wikiCount)
	}

	// Raw search must not surface wiki pages, and wiki search must not surface raw chunks.
	raw, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "retention", TopK: 10})
	if err != nil {
		t.Fatalf("raw search: %v", err)
	}
	for _, hit := range raw.Items {
		if strings.HasPrefix(hit.Path, "wiki/") {
			t.Fatalf("a wiki page leaked into raw search: %+v", hit)
		}
	}
	wiki, err := service.SearchWiki(ctx, owner, SearchWikiRequest{Query: "retention", TopK: 10})
	if err != nil {
		t.Fatalf("wiki search: %v", err)
	}
	for _, hit := range wiki.Items {
		if !strings.HasPrefix(hit.Path, "wiki/") {
			t.Fatalf("a raw chunk leaked into wiki search: %+v", hit)
		}
	}
}
