package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/ownership"
	"github.com/wellxie/agentmate/internal/retrieval"
)

func TestCompiledCatalogIntegration(t *testing.T) {
	databaseURL := os.Getenv("AGENTMATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTMATE_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()

	owner := createSkillIntegrationOwner(t, ctx, pool, "catalog-owner")
	otherOwner := createSkillIntegrationOwner(t, ctx, pool, "catalog-other")
	defer deleteSkillIntegrationAccount(t, ctx, pool, owner.Account())
	defer deleteSkillIntegrationAccount(t, ctx, pool, otherOwner.Account())

	repo := NewRepo(pool)
	service := NewService(repo)
	source, err := service.CreateSource(ctx, owner, CreateSkillSourceRequest{
		Name:          "catalog-source",
		Type:          "local",
		RepositoryURL: "file:///catalog-source",
		PackagePath:   "catalog-skill",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	activate := true
	index := false
	first, err := service.SubmitLocalSnapshot(ctx, owner, source.ID, SubmitLocalSnapshotRequest{
		SnapshotID: "catalog-v1",
		Activate:   &activate,
		Index:      &index,
		Files: []SnapshotFile{
			{
				Path: "SKILL.md",
				Content: `---
name: catalog-skill
description: Compiled catalog integration
triggers: [catalog request]
capabilities:
  - deterministic compile
constraints: [no content leak]
dependencies: []
---

# Catalog instructions
`,
			},
			{Path: "resources/guide.txt", Content: "TOP SECRET RESOURCE BODY"},
			{Path: "templates/example.txt", Content: "SECOND RESOURCE BODY"},
		},
	})
	if err != nil {
		t.Fatalf("submit snapshot: %v", err)
	}

	artifact, err := repo.GetCompiledCatalog(ctx, owner.Account(), first.Version.ID)
	if err != nil {
		if _, compileErr := service.Compile(ctx, owner.Account(), first.Version.ID); compileErr != nil {
			t.Fatalf("best-effort compile missing; explicit compile: %v", compileErr)
		}
		artifact, err = repo.GetCompiledCatalog(ctx, owner.Account(), first.Version.ID)
	}
	if err != nil {
		t.Fatalf("get compiled artifact after explicit compile: %v", err)
	}
	if artifact.CompilerName != SkillCompilerName || artifact.CompilerVersion != SkillCompilerVersion {
		t.Fatalf("compiler metadata = %s/%s", artifact.CompilerName, artifact.CompilerVersion)
	}
	if len(artifact.ResourceManifest) != 2 || artifact.ResourceManifest[0].Path != "resources/guide.txt" || artifact.ResourceManifest[1].Path != "templates/example.txt" {
		t.Fatalf("resource manifest = %#v", artifact.ResourceManifest)
	}
	if strings.Contains(string(mustJSON(t, artifact.ResourceManifest)), "TOP SECRET RESOURCE BODY") {
		t.Fatal("compiled manifest leaked resource content")
	}

	compileResponse, err := service.Compile(ctx, owner.Account(), first.Version.ID)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	if len(compileResponse.Items) != 1 || !compileResponse.Items[0].ArtifactAvailable {
		t.Fatalf("compile response = %#v", compileResponse)
	}
	var artifactCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM skill_compiled_catalogs WHERE account_id = $1 AND skill_version_id = $2`,
		owner.Account(), first.Version.ID,
	).Scan(&artifactCount); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if artifactCount != 1 {
		t.Fatalf("artifact count = %d, want 1", artifactCount)
	}

	secondVersion, err := service.CreateVersion(ctx, owner, CreateVersionRequest{
		SkillName: "another-skill",
		Version:   "v1",
		Content:   "---\nname: another-skill\ndescription: Another card\n---\n\n# Another\n",
		Activate:  true,
	})
	if err != nil {
		t.Fatalf("create second skill: %v", err)
	}
	catalogPageOne, err := service.ListCatalog(ctx, owner.Account(), SkillCatalogListParams{Limit: 1})
	if err != nil {
		t.Fatalf("list catalog page one: %v", err)
	}
	catalogPageTwo, err := service.ListCatalog(ctx, owner.Account(), SkillCatalogListParams{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list catalog page two: %v", err)
	}
	if catalogPageOne.Total != 2 || catalogPageTwo.Total != 2 || len(catalogPageOne.Items) != 1 || len(catalogPageTwo.Items) != 1 {
		t.Fatalf("catalog pages = %#v / %#v", catalogPageOne, catalogPageTwo)
	}
	if catalogPageOne.Items[0].SkillName != "another-skill" || catalogPageTwo.Items[0].SkillName != "catalog-skill" {
		t.Fatalf("unstable catalog order = %s, %s", catalogPageOne.Items[0].SkillName, catalogPageTwo.Items[0].SkillName)
	}
	filtered, err := service.ListCatalog(ctx, owner.Account(), SkillCatalogListParams{Query: "deterministic compile", Limit: 20})
	if err != nil {
		t.Fatalf("query catalog: %v", err)
	}
	if catalogPageTwo.Items[0].PackageHash != first.Version.PackageHash || catalogPageTwo.Items[0].SourceID == nil || *catalogPageTwo.Items[0].SourceID != source.ID {
		t.Fatalf("catalog identity fields = %#v", catalogPageTwo.Items[0])
	}
	if len(catalogPageTwo.Items[0].ResourceKinds) != 1 || catalogPageTwo.Items[0].ResourceKinds[0] != "document" {
		t.Fatalf("resource kinds = %#v", catalogPageTwo.Items[0].ResourceKinds)
	}
	if filtered.Total != 1 || filtered.Items[0].SkillVersionID != first.Version.ID {
		t.Fatalf("filtered catalog = %#v", filtered)
	}
	assertCatalogJSONSafe(t, catalogPageTwo)

	instructions, err := service.GetInstructions(ctx, owner.Account(), first.Version.ID)
	if err != nil || !strings.Contains(instructions.Instructions, "# Catalog instructions") {
		t.Fatalf("instructions = %#v, err = %v", instructions, err)
	}
	resources, err := service.GetResources(ctx, owner.Account(), first.Version.ID, SkillResourceListParams{Limit: 1})
	if err != nil || resources.Total != 2 || resources.Limit != 1 || resources.Offset != 0 || len(resources.Items) != 1 {
		t.Fatalf("resources page one = %#v, err = %v", resources, err)
	}
	resourcePageTwo, err := service.GetResources(ctx, owner.Account(), first.Version.ID, SkillResourceListParams{Limit: 1, Offset: 1})
	if err != nil || resourcePageTwo.Total != 2 || resourcePageTwo.Limit != 1 || resourcePageTwo.Offset != 1 || len(resourcePageTwo.Items) != 1 {
		t.Fatalf("resources page two = %#v, err = %v", resourcePageTwo, err)
	}
	if resources.Items[0].Path != "resources/guide.txt" || resourcePageTwo.Items[0].Path != "templates/example.txt" {
		t.Fatalf("unstable resource order = %#v / %#v", resources.Items, resourcePageTwo.Items)
	}
	assertCatalogJSONSafe(t, resources)
	resource, err := service.GetResource(ctx, owner.Account(), first.Version.ID, resources.Items[0].FileID)
	if err != nil || resource.Content != "TOP SECRET RESOURCE BODY" {
		t.Fatalf("resource = %#v, err = %v", resource, err)
	}
	gin.SetMode(gin.TestMode)
	handler := NewHandler(service)
	for _, endpoint := range []struct {
		name   string
		params gin.Params
		call   func(*gin.Context)
	}{
		{
			name:   "instructions",
			params: gin.Params{{Key: "id", Value: first.Version.ID}},
			call:   handler.GetInstructions,
		},
		{
			name: "resource",
			params: gin.Params{
				{Key: "id", Value: first.Version.ID},
				{Key: "file_id", Value: resources.Items[0].FileID},
			},
			call: handler.GetResource,
		},
	} {
		recorder := httptest.NewRecorder()
		ginContext, _ := gin.CreateTestContext(recorder)
		ginContext.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		ginContext.Params = endpoint.params
		ginContext.Set(auth.ContextAccountID, owner.Account())
		endpoint.call(ginContext)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s response code=%d cache-control=%q", endpoint.name, recorder.Code, recorder.Header().Get("Cache-Control"))
		}
	}
	versionFiles, err := repo.ListVersionFiles(ctx, owner.Account(), first.Version.ID)
	if err != nil {
		t.Fatalf("list version files: %v", err)
	}
	for _, file := range versionFiles {
		if file.Path == "SKILL.md" {
			if _, err := service.GetResource(ctx, owner.Account(), first.Version.ID, file.ID); err == nil {
				t.Fatal("root SKILL.md was exposed as a selected resource")
			}
		}
	}

	if _, err := service.GetInstructions(ctx, otherOwner.Account(), first.Version.ID); err == nil {
		t.Fatal("cross-account instructions access succeeded")
	}
	if _, err := service.GetResource(ctx, otherOwner.Account(), first.Version.ID, resources.Items[0].FileID); err == nil {
		t.Fatal("cross-account resource access succeeded")
	}
	if _, err := service.GetResource(ctx, owner.Account(), secondVersion.ID, resources.Items[0].FileID); err == nil {
		t.Fatal("cross-version file access succeeded")
	}

	if _, err := pool.Exec(ctx,
		`DELETE FROM skill_compiled_catalogs WHERE account_id = $1 AND skill_version_id = $2`,
		owner.Account(), first.Version.ID,
	); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}

	const fallbackContent = "Skill: catalog-skill\nDescription: safe lexical fallback"
	var stalePointID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO retrieval_documents (
		   account_id, user_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, vector_name, embedding_model, embedding_dimension, lexical_text, status, error
		 ) VALUES (
		   $1, $2, 'skills', 'skill_version', $3, 'active', $3, $4, $5,
		   jsonb_build_object(
		     'skill_name', $3::text, 'version', $6::text, 'version_id', $7::text,
		     'description', 'safe lexical fallback', 'package_hash', $8::text
		   ),
		   'agentmate_retrieval', 'semantic', 'test-model', 3, $9, 'failed', 'reindex required'
		 ) RETURNING qdrant_point_id::text`,
		owner.Account(), owner.UserID, first.Version.SkillName,
		fallbackContent, strings.Repeat("f", 64),
		first.Version.Version, first.Version.ID, first.Version.PackageHash,
		// Rows written outside the Go write path must carry the same projection,
		// otherwise they are invisible to the lexical leg.
		retrieval.LexicalProjection(first.Version.SkillName, fallbackContent),
	).Scan(&stalePointID); err != nil {
		t.Fatalf("insert safe failed retrieval document: %v", err)
	}
	hybridRetrieval := retrieval.NewService(
		retrieval.NewRepo(pool),
		&catalogTestVectorStore{pointID: stalePointID},
		catalogTestEmbedder{},
	)
	searchService := NewService(repo, hybridRetrieval)
	searchResult, err := searchService.Search(ctx, owner, SearchSkillsRequest{Query: "safe lexical fallback", TopK: 5})
	if err != nil {
		t.Fatalf("hybrid lexical fallback search: %v", err)
	}
	if len(searchResult.Items) != 1 || searchResult.Items[0].VersionID != first.Version.ID || searchResult.Items[0].Content != "" {
		t.Fatalf("hybrid lexical fallback result = %#v", searchResult)
	}
	searchWithContent, err := searchService.Search(ctx, owner, SearchSkillsRequest{Query: "safe lexical fallback", TopK: 5, IncludeContent: true})
	if err != nil {
		t.Fatalf("hybrid search with L1 content: %v", err)
	}
	if len(searchWithContent.Items) != 1 || !strings.Contains(searchWithContent.Items[0].Content, "# Catalog instructions") {
		t.Fatalf("hybrid L1 content result = %#v", searchWithContent)
	}
	fallback, err := service.ListCatalog(ctx, owner.Account(), SkillCatalogListParams{Query: "catalog-skill", Limit: 20})
	if err != nil {
		t.Fatalf("list fallback catalog: %v", err)
	}
	if fallback.Total != 1 || fallback.Items[0].ArtifactAvailable || fallback.Items[0].Description != "Compiled catalog integration" {
		t.Fatalf("fallback catalog = %#v", fallback)
	}
	if _, err := service.Compile(ctx, owner.Account(), first.Version.ID); err != nil {
		t.Fatalf("backfill artifact: %v", err)
	}

	broken, err := service.CreateVersion(ctx, owner, CreateVersionRequest{
		SkillName: "broken-skill",
		Version:   "v1",
		Content:   "---\nname: broken-skill\ntriggers: not-a-list\n---\n",
	})
	if err != nil {
		t.Fatalf("best-effort compiler broke direct publish: %v", err)
	}
	if _, err := repo.GetVersion(ctx, owner.Account(), broken.ID); err != nil {
		t.Fatalf("published package identity was lost: %v", err)
	}
	if _, err := service.Compile(ctx, owner.Account(), broken.ID); err == nil {
		t.Fatal("explicit compile accepted malformed frontmatter")
	}
}

func createSkillIntegrationOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) ownership.Owner {
	t.Helper()
	var accountID string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`, name).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, account_id) VALUES ($1, 'test', $2) RETURNING id::text`,
		name+"-"+accountID+"@example.test", accountID,
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return ownership.Owner{AccountID: accountID, UserID: userID}
}

func deleteSkillIntegrationAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM skill_sources WHERE account_id = $1`, accountID); err != nil {
		t.Errorf("clean up skill sources: %v", err)
		return
	}
	if _, err := pool.Exec(ctx, `DELETE FROM skill_versions WHERE account_id = $1`, accountID); err != nil {
		t.Errorf("clean up direct skill versions: %v", err)
		return
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
		t.Errorf("clean up account: %v", err)
	}
}

func assertCatalogJSONSafe(t *testing.T, value any) {
	t.Helper()
	payload := string(mustJSON(t, value))
	for _, forbidden := range []string{"account_id", "user_id", "key_id", "repository_url", "content_snapshot", `"content"`} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("catalog DTO leaked %q: %s", forbidden, payload)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return payload
}

type catalogTestEmbedder struct{}

func (catalogTestEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for index := range texts {
		vectors[index] = []float32{1, 0, 0}
	}
	return vectors, nil
}

func (catalogTestEmbedder) Model() string { return "catalog-test" }

type catalogTestVectorStore struct {
	pointID string
}

func (*catalogTestVectorStore) EnsureCollection(context.Context, int) error           { return nil }
func (*catalogTestVectorStore) Upsert(context.Context, []retrieval.VectorPoint) error { return nil }
func (store *catalogTestVectorStore) Search(context.Context, retrieval.VectorSearchRequest) ([]retrieval.VectorSearchResult, error) {
	return []retrieval.VectorSearchResult{{
		ID:      store.pointID,
		Score:   0.99,
		Payload: map[string]any{"stale": true},
	}}, nil
}
func (*catalogTestVectorStore) Collection() string { return "agentmate_retrieval" }
func (*catalogTestVectorStore) VectorName() string { return "semantic" }
