package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/retrieval"
)

// ─── in-memory retrieval doubles (mirroring the skills catalog test) ───

type knowledgeTestEmbedder struct{ fail bool }

func (e knowledgeTestEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.fail {
		return nil, fmt.Errorf("embedder unavailable")
	}
	vectors := make([][]float32, len(texts))
	for index := range texts {
		vectors[index] = []float32{1, 0, 0}
	}
	return vectors, nil
}

func (knowledgeTestEmbedder) Model() string { return "knowledge-test" }

type knowledgeTestVectorStore struct{}

func (*knowledgeTestVectorStore) EnsureCollection(context.Context, int) error           { return nil }
func (*knowledgeTestVectorStore) Upsert(context.Context, []retrieval.VectorPoint) error { return nil }
func (*knowledgeTestVectorStore) Search(context.Context, retrieval.VectorSearchRequest) ([]retrieval.VectorSearchResult, error) {
	return nil, nil
}
func (*knowledgeTestVectorStore) Collection() string { return "agentmate_retrieval" }
func (*knowledgeTestVectorStore) VectorName() string { return "semantic" }

func newKnowledgeTestService(t *testing.T, ctx context.Context, embedFail bool) (*Service, ownership.Owner, func()) {
	t.Helper()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "k2-"+fmt.Sprintf("%v", embedFail))
	retrievalSvc := retrieval.NewService(
		retrieval.NewRepo(pool),
		&knowledgeTestVectorStore{},
		knowledgeTestEmbedder{fail: embedFail},
	)
	return NewService(NewRepo(pool), retrievalSvc), owner, cleanup
}

func linkedSnapshot() SubmitSnapshotRequest {
	return SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: linked-kb\ndescription: catalog retrieval fixture\nprofile: linked-wiki-v1\nlanguage: zh-CN\ncitation_policy: required\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/guide.md", Content: "# Guide\n\nZebra installation walkthrough content.\n\nSee [the FAQ](faq.md) and [missing](missing.md).\n"},
		{Path: "raw/faq.md", Content: "# FAQ\n\nQuokka troubleshooting answers.\n\nBack to [guide](./guide.md).\n"},
	}}
}

func TestKnowledgeK2LinksCatalogIndexSearch(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	service, owner, cleanup := newKnowledgeTestService(t, ctx, false)
	defer cleanup()

	source := createIntegrationSource(t, ctx, service, owner, "k2-kb")
	ingest, err := service.SubmitSnapshot(ctx, owner, source.ID, linkedSnapshot())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// ── Ingest wrote document links in the same transaction ──
	documentIDs := map[string]string{}
	for _, document := range ingest.Documents {
		documentIDs[document.Path] = document.ID
	}
	guideLinks, err := service.ListDocumentLinks(ctx, owner.Account(), documentIDs["raw/guide.md"], 20, 0)
	if err != nil {
		t.Fatalf("guide links: %v", err)
	}
	// guide: out faq.md (resolved), out missing.md (dangling), in from faq.md.
	if guideLinks.Total != 3 || len(guideLinks.Items) != 3 {
		t.Fatalf("guide links = %#v", guideLinks)
	}
	if guideLinks.Items[0].Direction != "out" || guideLinks.Items[1].Direction != "out" || guideLinks.Items[2].Direction != "in" {
		t.Fatalf("link ordering (out before in) = %#v", guideLinks.Items)
	}
	byPath := map[string]KnowledgeDocumentLinkItem{}
	for _, item := range guideLinks.Items {
		byPath[item.Direction+":"+item.Path] = item
	}
	resolved := byPath["out:raw/faq.md"]
	if resolved.DocumentID == nil || *resolved.DocumentID != documentIDs["raw/faq.md"] {
		t.Fatalf("resolved link = %#v", resolved)
	}
	dangling := byPath["out:raw/missing.md"]
	if dangling.Path != "raw/missing.md" || dangling.DocumentID != nil {
		t.Fatalf("dangling link must keep target_path with NULL id = %#v", dangling)
	}
	incoming := byPath["in:raw/faq.md"]
	if incoming.DocumentID == nil || *incoming.DocumentID != documentIDs["raw/faq.md"] {
		t.Fatalf("incoming link = %#v", incoming)
	}

	// Link pagination is strict and stable.
	pageOne, err := service.ListDocumentLinks(ctx, owner.Account(), documentIDs["raw/guide.md"], 2, 0)
	if err != nil || len(pageOne.Items) != 2 || pageOne.Total != 3 {
		t.Fatalf("links page one = %#v err = %v", pageOne, err)
	}
	pageTwo, err := service.ListDocumentLinks(ctx, owner.Account(), documentIDs["raw/guide.md"], 2, 2)
	if err != nil || len(pageTwo.Items) != 1 || pageTwo.Items[0].Direction != "in" {
		t.Fatalf("links page two = %#v err = %v", pageTwo, err)
	}

	// ── Index active revision ──
	indexResponse, err := service.IndexActiveRevisions(ctx, owner, "")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(indexResponse.Errors) != 0 || len(indexResponse.Indexed) != 1 {
		t.Fatalf("index response = %#v", indexResponse)
	}
	indexed := indexResponse.Indexed[0]
	if indexed.ChunksIndexed == 0 || indexed.ChunksFailed != 0 || indexed.RevisionID != ingest.Revision.ID {
		t.Fatalf("indexed = %#v", indexed)
	}

	// ── Catalog card ──
	catalog, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Limit: 20})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if catalog.Total != 1 || len(catalog.Items) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
	card := catalog.Items[0]
	if card.Name != "linked-kb" || card.Description != "catalog retrieval fixture" ||
		card.Profile != "linked-wiki-v1" || card.Language != "zh-CN" || card.CitationPolicy != "required" {
		t.Fatalf("card manifest fields = %#v", card)
	}
	if card.DocumentCount != 2 || card.ActiveRevisionID != ingest.Revision.ID || card.PackageHash != ingest.Revision.PackageHash {
		t.Fatalf("card identity fields = %#v", card)
	}
	if card.IndexStatus != "indexed" || card.IndexedChunks != indexed.ChunksIndexed {
		t.Fatalf("card index status = %#v", card)
	}
	filtered, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Query: "retrieval fixture", Limit: 20})
	if err != nil || filtered.Total != 1 {
		t.Fatalf("filtered catalog = %#v err = %v", filtered, err)
	}
	missed, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Query: "no-such-kb", Limit: 20})
	if err != nil || missed.Total != 0 || len(missed.Items) != 0 {
		t.Fatalf("miss catalog = %#v err = %v", missed, err)
	}
	assertKnowledgeJSONSafe(t, catalog)

	// ── Lexical search hits with provenance and neighbors ──
	search, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "zebra installation", TopK: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(search.Items) == 0 {
		t.Fatalf("search returned no hits: %#v", search)
	}
	hit := search.Items[0]
	if hit.Path != "raw/guide.md" || hit.SourceID != source.ID || hit.RevisionID != ingest.Revision.ID {
		t.Fatalf("hit provenance = %#v", hit)
	}
	if hit.Knowledge != "linked-kb" || hit.HeadingPath == "" || hit.ChunkKey == "" {
		t.Fatalf("hit metadata = %#v", hit)
	}
	if hit.Content != "" {
		t.Fatalf("content returned without include_content: %#v", hit)
	}
	if hit.Snippet == "" || !strings.Contains(strings.ToLower(hit.Snippet), "zebra") {
		t.Fatalf("snippet = %q", hit.Snippet)
	}
	if len(hit.Neighbors) != 3 {
		t.Fatalf("neighbors = %#v", hit.Neighbors)
	}
	for _, neighbor := range hit.Neighbors {
		if neighbor.Direction != "out" && neighbor.Direction != "in" {
			t.Fatalf("neighbor direction = %#v", neighbor)
		}
	}

	withContent, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "zebra installation", TopK: 5, IncludeContent: true})
	if err != nil || len(withContent.Items) == 0 || !strings.Contains(withContent.Items[0].Content, "Zebra installation") {
		t.Fatalf("include_content search = %#v err = %v", withContent, err)
	}

	// source_ids filter: matching source keeps hits, foreign ID is rejected.
	scoped, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "zebra installation", TopK: 5, SourceIDs: []string{source.ID}})
	if err != nil || len(scoped.Items) == 0 {
		t.Fatalf("scoped search = %#v err = %v", scoped, err)
	}
	if _, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "zebra", SourceIDs: []string{"00000000-0000-0000-0000-000000000000"}}); err == nil {
		t.Fatal("unknown source_id must be rejected")
	}

	// ── Reindex idempotency: chunk count is stable, links unchanged ──
	var chunkCountBefore int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM retrieval_documents WHERE account_id = $1 AND namespace = 'knowledge'`,
		owner.Account(),
	).Scan(&chunkCountBefore); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if _, err := service.IndexActiveRevisions(ctx, owner, source.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	var chunkCountAfter, linkCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM retrieval_documents WHERE account_id = $1 AND namespace = 'knowledge'`,
		owner.Account(),
	).Scan(&chunkCountAfter); err != nil {
		t.Fatalf("recount chunks: %v", err)
	}
	if chunkCountAfter != chunkCountBefore {
		t.Fatalf("reindex changed chunk count %d -> %d", chunkCountBefore, chunkCountAfter)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_document_links WHERE account_id = $1 AND revision_id = $2`,
		owner.Account(), ingest.Revision.ID,
	).Scan(&linkCount); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if linkCount != 3 {
		t.Fatalf("link rows = %d, want 3", linkCount)
	}

	// ── New revision replaces old chunks ──
	second, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: linked-kb\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/guide.md", Content: "# Guide v2\n\nWalrus deployment content only.\n"},
	}})
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if _, err := service.IndexActiveRevisions(ctx, owner, source.ID); err != nil {
		t.Fatalf("index second revision: %v", err)
	}
	var staleCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM retrieval_documents
		 WHERE account_id = $1 AND namespace = 'knowledge' AND metadata->>'revision_id' <> $2`,
		owner.Account(), second.Revision.ID,
	).Scan(&staleCount); err != nil {
		t.Fatalf("count stale chunks: %v", err)
	}
	if staleCount != 0 {
		t.Fatalf("old revision left %d retrieval documents behind", staleCount)
	}
	oldHits, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "zebra installation", TopK: 5})
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(oldHits.Items) != 0 {
		t.Fatalf("stale revision content still searchable: %#v", oldHits.Items)
	}
}

func TestKnowledgeK2CrossAccountIsolation(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	retrievalSvc := retrieval.NewService(retrieval.NewRepo(pool), &knowledgeTestVectorStore{}, knowledgeTestEmbedder{})
	service := NewService(NewRepo(pool), retrievalSvc)

	ownerA, cleanupA := createKnowledgeIntegrationOwner(t, ctx, pool, "k2-tenant-a")
	defer cleanupA()
	ownerB, cleanupB := createKnowledgeIntegrationOwner(t, ctx, pool, "k2-tenant-b")
	defer cleanupB()

	source := createIntegrationSource(t, ctx, service, ownerA, "k2-isolation-kb")
	ingest, err := service.SubmitSnapshot(ctx, ownerA, source.ID, linkedSnapshot())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := service.IndexActiveRevisions(ctx, ownerA, ""); err != nil {
		t.Fatalf("index: %v", err)
	}

	catalogB, err := service.ListCatalog(ctx, ownerB.Account(), KnowledgeCatalogListParams{Limit: 20})
	if err != nil || catalogB.Total != 0 {
		t.Fatalf("tenant B catalog = %#v err = %v", catalogB, err)
	}
	searchB, err := service.Search(ctx, ownerB, SearchKnowledgeRequest{Query: "zebra installation", TopK: 5})
	if err != nil || len(searchB.Items) != 0 {
		t.Fatalf("tenant B search = %#v err = %v", searchB, err)
	}
	if _, err := service.Search(ctx, ownerB, SearchKnowledgeRequest{Query: "zebra", SourceIDs: []string{source.ID}}); err == nil {
		t.Fatal("tenant B must not filter by tenant A's source")
	}
	if _, err := service.ListDocumentLinks(ctx, ownerB.Account(), ingest.Documents[0].ID, 20, 0); err == nil {
		t.Fatal("tenant B must not read tenant A's links")
	}
	if _, err := service.IndexActiveRevisions(ctx, ownerB, source.ID); err == nil {
		t.Fatal("tenant B must not index tenant A's source")
	}
}

func TestKnowledgeK2EmbeddingFailureKeepsLexicalFallback(t *testing.T) {
	ctx := context.Background()
	service, owner, cleanup := newKnowledgeTestService(t, ctx, true)
	defer cleanup()

	source := createIntegrationSource(t, ctx, service, owner, "k2-fallback-kb")
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, linkedSnapshot()); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	indexResponse, err := service.IndexActiveRevisions(ctx, owner, "")
	if err != nil {
		t.Fatalf("index with failing embedder: %v", err)
	}
	if len(indexResponse.Indexed) != 1 || indexResponse.Indexed[0].ChunksFailed == 0 || indexResponse.Indexed[0].ChunksIndexed != 0 {
		t.Fatalf("failing-embedder index = %#v", indexResponse)
	}

	// Failed chunk rows still serve the PostgreSQL lexical channel; the
	// query embedding failure is tolerated by hybrid fusion.
	search, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "zebra installation", TopK: 5})
	if err != nil {
		t.Fatalf("lexical fallback search: %v", err)
	}
	if len(search.Items) == 0 || search.Items[0].Path != "raw/guide.md" {
		t.Fatalf("lexical fallback hits = %#v", search)
	}
}

func TestKnowledgeK2CatalogPagination(t *testing.T) {
	ctx := context.Background()
	service, owner, cleanup := newKnowledgeTestService(t, ctx, false)
	defer cleanup()

	for _, name := range []string{"alpha-kb", "beta-kb", "gamma-kb"} {
		source := createIntegrationSource(t, ctx, service, owner, name)
		if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
			{Path: "KNOWLEDGE.yaml", Content: "name: " + name + "\ninclude:\n  - \"raw/**\"\n"},
			{Path: "raw/doc.md", Content: "# " + name + "\ncontent\n"},
		}}); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
	}

	pageOne, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Limit: 2})
	if err != nil || pageOne.Total != 3 || len(pageOne.Items) != 2 {
		t.Fatalf("page one = %#v err = %v", pageOne, err)
	}
	pageTwo, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Limit: 2, Offset: 2})
	if err != nil || pageTwo.Total != 3 || len(pageTwo.Items) != 1 {
		t.Fatalf("page two = %#v err = %v", pageTwo, err)
	}
	if pageOne.Items[0].Name != "alpha-kb" || pageOne.Items[1].Name != "beta-kb" || pageTwo.Items[0].Name != "gamma-kb" {
		t.Fatalf("unstable catalog order: %#v / %#v", pageOne.Items, pageTwo.Items)
	}
	if _, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Limit: 101}); err == nil {
		t.Fatal("limit above 100 must be rejected")
	}
	if _, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Offset: -1}); err == nil {
		t.Fatal("negative offset must be rejected")
	}
}

func assertKnowledgeJSONSafe(t *testing.T, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	for _, forbidden := range []string{"account_id", "user_id", "key_id", "content_snapshot"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("catalog DTO leaked %q: %s", forbidden, payload)
		}
	}
}

// Domain is derived from the package directory layout and must reach the
// catalog (grouping) and search (filtering) paths. Two collections share the
// leaf name "retrieval" under different domains, which is exactly the case
// that used to collide on the unique (account_id, name) constraint.
func TestKnowledgeDomainGroupingAndSearchFilter(t *testing.T) {
	ctx := context.Background()
	service, owner, cleanup := newKnowledgeTestService(t, ctx, false)
	defer cleanup()

	create := func(packagePath string) *KnowledgeSource {
		t.Helper()
		source, err := service.CreateSource(ctx, owner, CreateKnowledgeSourceRequest{
			Type:          "local",
			RepositoryURL: "file:///wiki",
			PackagePath:   packagePath,
		})
		if err != nil {
			t.Fatalf("create source %s: %v", packagePath, err)
		}
		return source
	}

	platform := create("platform/retrieval")
	product := create("product/retrieval")
	flat := create("standalone")

	if platform.Domain != "platform" || product.Domain != "product" {
		t.Fatalf("domains = %q / %q", platform.Domain, product.Domain)
	}
	if flat.Domain != "" {
		t.Fatalf("flat package should have no domain, got %q", flat.Domain)
	}
	if platform.Name == product.Name {
		t.Fatalf("same leaf name under different domains collided: %q", platform.Name)
	}
	if platform.Name != "platform-retrieval" || product.Name != "product-retrieval" {
		t.Fatalf("names = %q / %q", platform.Name, product.Name)
	}

	for _, source := range []*KnowledgeSource{platform, product, flat} {
		if _, err := service.SubmitSnapshot(ctx, owner, source.ID, linkedSnapshot()); err != nil {
			t.Fatalf("snapshot %s: %v", source.Domain, err)
		}
	}
	if _, err := service.IndexActiveRevisions(ctx, owner, ""); err != nil {
		t.Fatalf("index: %v", err)
	}

	// ── Catalog reports the domain roster; the flat package is unclassified ──
	catalog, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Limit: 20})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if catalog.Total != 3 {
		t.Fatalf("catalog total = %d, want 3", catalog.Total)
	}
	counts := map[string]int{}
	for _, item := range catalog.Domains {
		counts[item.Domain] = item.CollectionCount
	}
	if counts["platform"] != 1 || counts["product"] != 1 {
		t.Fatalf("domain counts = %#v", catalog.Domains)
	}
	if _, ok := counts[""]; ok {
		t.Fatalf("unclassified source must not appear as a domain: %#v", catalog.Domains)
	}

	// ── Catalog filters to one domain ──
	filtered, err := service.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Domain: "platform", Limit: 20})
	if err != nil {
		t.Fatalf("catalog filtered: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Domain != "platform" {
		t.Fatalf("filtered catalog = %#v", filtered)
	}

	// ── Search restricted to a domain only returns that domain's sources ──
	hits, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "Zebra installation", Domain: "platform", TopK: 10})
	if err != nil {
		t.Fatalf("domain search: %v", err)
	}
	if hits.Total == 0 {
		t.Fatalf("domain search returned no hits")
	}
	for _, hit := range hits.Items {
		if hit.SourceID != platform.ID {
			t.Fatalf("hit from outside domain: %#v", hit)
		}
	}

	// ── Domain + source_ids intersect (narrow), never widen ──
	if _, err := service.Search(ctx, owner, SearchKnowledgeRequest{
		Query:     "Zebra installation",
		Domain:    "platform",
		SourceIDs: []string{product.ID},
	}); err == nil {
		t.Fatal("expected error when source_ids fall outside the domain")
	}

	if _, err := service.Search(ctx, owner, SearchKnowledgeRequest{Query: "Zebra", Domain: "nope"}); err == nil {
		t.Fatal("expected error for unknown domain")
	}
}
