package retrieval

import (
	"testing"
	"time"
)

func TestFuseSearchCandidatesRewardsMultiChannelHits(t *testing.T) {
	now := time.Now()
	vectorOnly := Document{ID: "vector-only", QdrantPointID: "point-1", UpdatedAt: now}
	both := Document{ID: "both", QdrantPointID: "point-2", UpdatedAt: now}
	vector := []VectorSearchResult{
		{ID: "point-1", Score: 0.95},
		{ID: "point-2", Score: 0.80},
	}
	documents := map[string]Document{
		"point-1": vectorOnly,
		"point-2": both,
	}
	text := []TextSearchResult{
		{Document: both, Score: 0.5},
	}

	results, logs := fuseSearchCandidates(vector, documents, text, 2)
	if len(results) != 2 || len(logs) != 2 {
		t.Fatalf("unexpected result lengths: results=%d logs=%d", len(results), len(logs))
	}
	if results[0].Document == nil || results[0].Document.ID != "both" {
		t.Fatalf("first result = %#v, want multi-channel document", results[0].Document)
	}
	if results[0].Stage != "lexical+vector" {
		t.Fatalf("stage = %q", results[0].Stage)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("multi-channel score %v must exceed vector-only score %v", results[0].Score, results[1].Score)
	}
}

func TestTextFiltersFromSearch(t *testing.T) {
	filters, err := textFiltersFromSearch(map[string]any{
		"source_type":         "memory_entry",
		"metadata.scope_type": "repository",
		"metadata.status":     "active",
	})
	if err != nil {
		t.Fatalf("textFiltersFromSearch: %v", err)
	}
	if filters.SourceType != "memory_entry" {
		t.Fatalf("source type = %q", filters.SourceType)
	}
	if filters.Metadata["scope_type"] != "repository" || filters.Metadata["status"] != "active" {
		t.Fatalf("metadata filters = %#v", filters.Metadata)
	}
}

func TestFuseSearchCandidatesLeavesLexicalPointIDEmpty(t *testing.T) {
	document := Document{ID: "lexical-only", QdrantPointID: "unused-point"}
	results, logs := fuseSearchCandidates(nil, nil, []TextSearchResult{{Document: document, Score: 0.2}}, 1)
	if len(results) != 1 || len(logs) != 1 {
		t.Fatalf("unexpected result lengths: results=%d logs=%d", len(results), len(logs))
	}
	if results[0].PointID != "" || logs[0].QdrantPointID != "" {
		t.Fatalf("lexical-only result must not claim a Qdrant hit: result=%q log=%q", results[0].PointID, logs[0].QdrantPointID)
	}
}

func TestTextFiltersRejectUnsupportedFilter(t *testing.T) {
	if _, err := textFiltersFromSearch(map[string]any{"scope_type": "repository"}); err == nil {
		t.Fatal("expected unsupported filter error")
	}
}
