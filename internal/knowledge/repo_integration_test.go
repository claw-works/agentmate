package knowledge

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/ownership"
)

// createKnowledgeIntegrationOwner mirrors the skills integration helper: it
// provisions a scratch account plus user and returns an owner and a cleanup
// function that deletes the account (exercising the FK cascade).
func createKnowledgeIntegrationOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) (ownership.Owner, func()) {
	t.Helper()
	var accountID string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`, "knowledge integration "+label).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, account_id) VALUES ($1, 'test', $2) RETURNING id::text`,
		"knowledge-"+label+"-"+accountID+"@example.test", accountID,
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
			t.Errorf("clean up account %s: %v", accountID, err)
		}
	}
	return ownership.Owner{AccountID: accountID, UserID: userID}, cleanup
}

func integrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("AGENTMATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTMATE_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func integrationSnapshot(documentBody string) SubmitSnapshotRequest {
	return SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: integration-kb\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/guide.md", Content: documentBody},
	}}
}

func createIntegrationSource(t *testing.T, ctx context.Context, service *Service, owner ownership.Owner, name string) *KnowledgeSource {
	t.Helper()
	source, err := service.CreateSource(ctx, owner, CreateKnowledgeSourceRequest{
		Name:          name,
		Type:          "local",
		RepositoryURL: "file:///" + name,
		PackagePath:   "kb",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	return source
}

func TestKnowledgeIngestIdempotencyAndConcurrency(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "idempotency")
	defer cleanup()

	service := NewService(NewRepo(pool))
	source := createIntegrationSource(t, ctx, service, owner, "idempotency-kb")

	request := integrationSnapshot("# Guide v1\n")
	const parallelReplays = 6
	responses := make([]*SubmitSnapshotResponse, parallelReplays)
	errs := make([]error, parallelReplays)
	var waitGroup sync.WaitGroup
	for index := range responses {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			responses[index], errs[index] = service.SubmitSnapshot(ctx, owner, source.ID, request)
		}(index)
	}
	waitGroup.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("parallel replay %d: %v", index, err)
		}
		if responses[index].Revision.ID != responses[0].Revision.ID {
			t.Fatalf("parallel replay %d returned a different immutable revision", index)
		}
	}

	// Sequential replay stays idempotent and keeps exactly one revision.
	replay, err := service.SubmitSnapshot(ctx, owner, source.ID, request)
	if err != nil {
		t.Fatalf("sequential replay: %v", err)
	}
	if replay.Revision.ID != responses[0].Revision.ID {
		t.Fatal("sequential replay returned a different revision")
	}
	revisions, err := service.ListSourceRevisions(ctx, owner.Account(), source.ID, 100, 0)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("revisions = %d, want 1", len(revisions))
	}
	if len(replay.Documents) != 1 || replay.Documents[0].Path != "raw/guide.md" {
		t.Fatalf("documents = %#v", replay.Documents)
	}
}

func TestKnowledgeActivePointerSwitching(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "pointer")
	defer cleanup()

	service := NewService(NewRepo(pool))
	source := createIntegrationSource(t, ctx, service, owner, "pointer-kb")

	first, err := service.SubmitSnapshot(ctx, owner, source.ID, integrationSnapshot("# Version A\n"))
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if first.Source.ActiveRevisionID == nil || *first.Source.ActiveRevisionID != first.Revision.ID {
		t.Fatalf("active pointer after first ingest = %v", first.Source.ActiveRevisionID)
	}

	second, err := service.SubmitSnapshot(ctx, owner, source.ID, integrationSnapshot("# Version B\n"))
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second.Revision.ID == first.Revision.ID {
		t.Fatal("different content must create a new revision")
	}
	if second.Source.ActiveRevisionID == nil || *second.Source.ActiveRevisionID != second.Revision.ID {
		t.Fatalf("active pointer after second ingest = %v", second.Source.ActiveRevisionID)
	}

	// Replaying the first snapshot re-targets the active pointer at the
	// existing immutable revision instead of creating a new one.
	replay, err := service.SubmitSnapshot(ctx, owner, source.ID, integrationSnapshot("# Version A\n"))
	if err != nil {
		t.Fatalf("replay first snapshot: %v", err)
	}
	if replay.Revision.ID != first.Revision.ID {
		t.Fatal("replay must reuse the first revision")
	}
	if replay.Source.ActiveRevisionID == nil || *replay.Source.ActiveRevisionID != first.Revision.ID {
		t.Fatalf("active pointer after replay = %v", replay.Source.ActiveRevisionID)
	}

	// Deleting the currently active revision directly is rejected until the
	// pointer is cleared (NO ACTION composite FK).
	if _, err := pool.Exec(ctx, `DELETE FROM knowledge_source_revisions WHERE id = $1`, first.Revision.ID); err == nil {
		t.Fatal("deleting the active revision must be rejected while referenced")
	}
}

func TestKnowledgeCrossAccountIsolation(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	ownerA, cleanupA := createKnowledgeIntegrationOwner(t, ctx, pool, "tenant-a")
	defer cleanupA()
	ownerB, cleanupB := createKnowledgeIntegrationOwner(t, ctx, pool, "tenant-b")
	defer cleanupB()

	service := NewService(NewRepo(pool))
	source := createIntegrationSource(t, ctx, service, ownerA, "isolation-kb")
	response, err := service.SubmitSnapshot(ctx, ownerA, source.ID, integrationSnapshot("# Secret A\n"))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if _, err := service.GetSource(ctx, ownerB.Account(), source.ID); err == nil {
		t.Fatal("tenant B must not read tenant A's source")
	}
	if _, err := service.ListSourceRevisions(ctx, ownerB.Account(), source.ID, 20, 0); err == nil {
		t.Fatal("tenant B must not list tenant A's revisions")
	}
	if _, err := service.ListRevisionDocuments(ctx, ownerB.Account(), response.Revision.ID, DocumentListParams{Limit: 20}); err == nil {
		t.Fatal("tenant B must not list tenant A's documents")
	}
	if _, err := service.GetDocument(ctx, ownerB.Account(), response.Revision.ID, response.Documents[0].ID); err == nil {
		t.Fatal("tenant B must not read tenant A's document")
	}
	if _, err := service.SubmitSnapshot(ctx, ownerB, source.ID, integrationSnapshot("# Hijack\n")); err == nil {
		t.Fatal("tenant B must not push snapshots into tenant A's source")
	}

	sources, err := service.ListSources(ctx, ownerB.Account(), KnowledgeSourceListParams{})
	if err != nil {
		t.Fatalf("list tenant B sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("tenant B sources = %d, want 0", len(sources))
	}
}

func TestKnowledgeAccountDeleteCascade(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "cascade")

	service := NewService(NewRepo(pool))
	source := createIntegrationSource(t, ctx, service, owner, "cascade-kb")
	response, err := service.SubmitSnapshot(ctx, owner, source.ID, integrationSnapshot("# Cascade\n"))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Deleting the account cascades through sources, revisions, and
	// documents, even though the source still holds an active pointer.
	cleanup()

	for _, probe := range []struct {
		name  string
		query string
		arg   string
	}{
		{name: "sources", query: `SELECT count(*) FROM knowledge_sources WHERE id = $1`, arg: source.ID},
		{name: "revisions", query: `SELECT count(*) FROM knowledge_source_revisions WHERE id = $1`, arg: response.Revision.ID},
		{name: "documents", query: `SELECT count(*) FROM knowledge_documents WHERE revision_id = $1`, arg: response.Revision.ID},
	} {
		var count int
		if err := pool.QueryRow(ctx, probe.query, probe.arg).Scan(&count); err != nil {
			t.Fatalf("probe %s: %v", probe.name, err)
		}
		if count != 0 {
			t.Fatalf("%s rows remain after account delete: %d", probe.name, count)
		}
	}
}

func TestKnowledgeIngestFailureRecordsSourceError(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "failure")
	defer cleanup()

	service := NewService(NewRepo(pool))
	source := createIntegrationSource(t, ctx, service, owner, "failure-kb")

	// Invalid snapshot: manifest missing. No revision may be produced and
	// the source must move to error status.
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{{Path: "raw/only.md", Content: "x"}}}); err == nil {
		t.Fatal("expected snapshot rejection")
	}
	failed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if failed.Status != "error" {
		t.Fatalf("source status = %q, want error", failed.Status)
	}
	revisions, err := service.ListSourceRevisions(ctx, owner.Account(), source.ID, 20, 0)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 0 {
		t.Fatalf("failed ingest left %d revisions behind", len(revisions))
	}

	// A subsequent valid ingest recovers the source to active.
	recovered, err := service.SubmitSnapshot(ctx, owner, source.ID, integrationSnapshot("# Recovered\n"))
	if err != nil {
		t.Fatalf("recovery snapshot: %v", err)
	}
	if recovered.Source.Status != "active" {
		t.Fatalf("recovered status = %q, want active", recovered.Source.Status)
	}
}

// Registration upserts by name and names are derived from package_path, so two
// different packages can resolve to the same name. Before the conflict guard the
// second registration silently repointed the first source at another repository,
// leaving one source whose revision history spanned two unrelated origins.
func TestCreateSourceRejectsNameCollisionFromDifferentPackage(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "name-collision")
	defer cleanup()
	service := NewService(NewRepo(pool))

	flat, err := service.CreateSource(ctx, owner, CreateKnowledgeSourceRequest{
		Type:          "git",
		RepositoryURL: "https://github.com/acme/old-kb",
		PackagePath:   "product-support",
	})
	if err != nil {
		t.Fatalf("create flat source: %v", err)
	}
	if flat.Name != "product-support" || flat.Domain != "" {
		t.Fatalf("flat source = name %q domain %q", flat.Name, flat.Domain)
	}

	// "product/support" derives the same name but is a different package.
	_, err = service.CreateSource(ctx, owner, CreateKnowledgeSourceRequest{
		Type:          "git",
		RepositoryURL: "https://github.com/acme/new-wiki",
		PackagePath:   "product/support",
	})
	if err == nil {
		t.Fatal("expected the colliding registration to be rejected")
	}
	if !strings.Contains(err.Error(), "already used by a different package") {
		t.Fatalf("error = %v", err)
	}

	// The original source must be untouched.
	reloaded, err := service.GetSource(ctx, owner.Account(), flat.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if reloaded.RepositoryURL != "https://github.com/acme/old-kb" || reloaded.PackagePath != "product-support" {
		t.Fatalf("original source was modified: %#v", reloaded)
	}

	// Re-registering the same package must still upsert.
	again, err := service.CreateSource(ctx, owner, CreateKnowledgeSourceRequest{
		Type:          "git",
		RepositoryURL: "https://github.com/acme/old-kb",
		PackagePath:   "product-support",
		DefaultRef:    "release",
	})
	if err != nil {
		t.Fatalf("re-register same package: %v", err)
	}
	if again.ID != flat.ID || again.DefaultRef != "release" {
		t.Fatalf("expected in-place upsert, got %#v", again)
	}

	// An explicit distinct name lets both packages coexist.
	explicit, err := service.CreateSource(ctx, owner, CreateKnowledgeSourceRequest{
		Name:          "product-support-wiki",
		Type:          "git",
		RepositoryURL: "https://github.com/acme/new-wiki",
		PackagePath:   "product/support",
	})
	if err != nil {
		t.Fatalf("create with explicit name: %v", err)
	}
	if explicit.Domain != "product" {
		t.Fatalf("explicit source domain = %q, want product", explicit.Domain)
	}
}
