package retrieval

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claw-works/agentmate/internal/ownership"
)

func lexicalIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

func lexicalIntegrationOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) (ownership.Owner, func()) {
	t.Helper()
	var accountID string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`, "lexical integration "+label).Scan(&accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, account_id) VALUES ($1, 'test', $2) RETURNING id::text`,
		"lexical-"+label+"-"+accountID+"@example.test", accountID,
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

// insertIndexedDocument writes a row straight through the repo (no embedder, no
// vector store) so the lexical leg can be exercised on its own, then marks it
// indexed since the text search only considers indexed/failed rows.
func insertIndexedDocument(t *testing.T, ctx context.Context, repo *Repo, owner ownership.Owner, chunkKey, title, content string) {
	t.Helper()
	document, err := repo.UpsertDocument(ctx, owner, UpsertDocumentInput{
		Namespace:  NamespaceKnowledge,
		SourceType: "knowledge_chunk",
		SourceID:   "source-" + chunkKey,
		ChunkKey:   chunkKey,
		Title:      title,
		Content:    content,
	})
	if err != nil {
		t.Fatalf("upsert %s: %v", chunkKey, err)
	}
	if err := repo.MarkDocumentIndexed(ctx, owner.Account(), document.ID); err != nil {
		t.Fatalf("mark indexed %s: %v", chunkKey, err)
	}
}

// This is the regression that motivated the projection: with the previous
// to_tsvector('simple', title || content) index a whole Chinese sentence became
// one token, so every CJK query returned zero lexical hits and hybrid search
// silently degraded to semantic-only.
func TestLexicalSearchMatchesChineseQuery(t *testing.T) {
	ctx := context.Background()
	pool := lexicalIntegrationPool(t, ctx)
	repo := NewRepo(pool)
	owner, cleanup := lexicalIntegrationOwner(t, ctx, pool, "cjk")
	defer cleanup()

	insertIndexedDocument(t, ctx, repo, owner, "chunk-memory",
		"raw/memory-model.md # 记忆晋升",
		"记忆什么时候可以进入知识库？必须通过使用信号门槛与人工审批。")
	insertIndexedDocument(t, ctx, repo, owner, "chunk-registry",
		"raw/skill-registry.md # 包身份",
		"Skill Registry 使用 canonical package hash 作为包身份，pax_global_header 不参与。")

	t.Run("sentence length CJK query hits", func(t *testing.T) {
		results, err := repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "记忆什么时候可以进入知识库", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("CJK query returned no lexical hits")
		}
		if results[0].ChunkKey != "chunk-memory" {
			t.Fatalf("top hit = %q, want chunk-memory", results[0].ChunkKey)
		}
	})

	t.Run("partial CJK term hits longer indexed term", func(t *testing.T) {
		results, err := repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "晋升", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("partial CJK term returned no hits")
		}
	})

	t.Run("identifier keeps exact matching", func(t *testing.T) {
		results, err := repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "pax_global_header", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) != 1 || results[0].ChunkKey != "chunk-registry" {
			t.Fatalf("identifier search = %#v", results)
		}
	})

	// ASCII words stay conjunctive, so an identifier absent from a document
	// must exclude it even when the CJK part of the query matches.
	t.Run("mixed query requires the identifier", func(t *testing.T) {
		results, err := repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "记忆 pax_global_header", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, result := range results {
			if result.ChunkKey == "chunk-memory" {
				t.Fatalf("document without the identifier matched: %#v", results)
			}
		}
	})

	t.Run("query without searchable tokens yields nothing", func(t *testing.T) {
		results, err := repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "!!! ???", 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected no hits, got %d", len(results))
		}
	})
}

// Rows written before the projection existed carry an empty lexical_text and
// must be repairable without re-embedding.
func TestRebuildLexicalProjectionsRepairsExistingRows(t *testing.T) {
	ctx := context.Background()
	pool := lexicalIntegrationPool(t, ctx)
	repo := NewRepo(pool)
	owner, cleanup := lexicalIntegrationOwner(t, ctx, pool, "rebuild")
	defer cleanup()

	insertIndexedDocument(t, ctx, repo, owner, "chunk-rebuild",
		"raw/retrieval.md # 检索约定", "中文检索需要 bigram 投影才能命中。")

	// Simulate a pre-migration row.
	if _, err := pool.Exec(ctx,
		`UPDATE retrieval_documents SET lexical_text = '' WHERE account_id = $1`, owner.Account()); err != nil {
		t.Fatalf("clear projection: %v", err)
	}
	results, err := repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "bigram 投影", 10)
	if err != nil {
		t.Fatalf("search before rebuild: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no hits while the projection is empty, got %d", len(results))
	}

	updated, err := repo.RebuildLexicalProjections(ctx, owner.Account(), NamespaceKnowledge, 0)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	results, err = repo.SearchDocumentsText(ctx, owner.Account(), NamespaceKnowledge, "bigram 投影", 10)
	if err != nil {
		t.Fatalf("search after rebuild: %v", err)
	}
	if len(results) != 1 || results[0].ChunkKey != "chunk-rebuild" {
		t.Fatalf("after rebuild = %#v", results)
	}
}
