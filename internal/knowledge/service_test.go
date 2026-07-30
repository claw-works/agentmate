package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/gitfetch"
)

const manifestAllContent = "name: test-kb\n"

// TestComputePackageHashMatchesSkillsAlgorithm locks the canonical package
// hash to the exact construction used by internal/skills computePackageHash:
// "path\x00sha256\x00size" lines, sorted, joined with "\n", then SHA-256.
func TestComputePackageHashMatchesSkillsAlgorithm(t *testing.T) {
	files := []KnowledgeDocument{
		{Path: "raw/b.md", SHA256: sha256HexString("beta"), SizeBytes: 4},
		{Path: "raw/a.md", SHA256: sha256HexString("alpha"), SizeBytes: 5},
		{Path: "KNOWLEDGE.yaml", SHA256: sha256HexString(manifestAllContent), SizeBytes: int64(len(manifestAllContent))},
	}

	lines := make([]string, 0, len(files))
	for _, file := range files {
		lines = append(lines, fmt.Sprintf("%s\x00%s\x00%d", file.Path, file.SHA256, file.SizeBytes))
	}
	sort.Strings(lines)
	expectedDigest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	expected := hex.EncodeToString(expectedDigest[:])

	if got := computePackageHash(files); got != expected {
		t.Fatalf("computePackageHash = %s, want %s", got, expected)
	}

	shuffled := []KnowledgeDocument{files[2], files[0], files[1]}
	if got := computePackageHash(shuffled); got != expected {
		t.Fatalf("computePackageHash must be order independent, got %s", got)
	}
}

func snapshotRequest(files ...SnapshotFile) SubmitSnapshotRequest {
	base := []SnapshotFile{{Path: "KNOWLEDGE.yaml", Content: manifestAllContent}}
	return SubmitSnapshotRequest{Files: append(base, files...)}
}

func TestNormalizeSnapshotFiles(t *testing.T) {
	goodDoc := SnapshotFile{Path: "raw/guide.md", Content: "# Guide\n"}
	binaryDoc := SnapshotFile{Path: "raw/diagram.png", SHA256: sha256HexString("binary-bytes"), SizeBytes: 12}

	for _, testCase := range []struct {
		name      string
		request   SubmitSnapshotRequest
		wantError string
		check     func(t *testing.T, manifest Manifest, documents []KnowledgeDocument, hash string)
	}{
		{
			name:    "text and binary documents",
			request: snapshotRequest(goodDoc, binaryDoc),
			check: func(t *testing.T, manifest Manifest, documents []KnowledgeDocument, hash string) {
				if manifest.Name != "test-kb" {
					t.Fatalf("manifest name = %q", manifest.Name)
				}
				if len(documents) != 2 {
					t.Fatalf("documents = %d, want 2", len(documents))
				}
				byPath := map[string]KnowledgeDocument{}
				for _, document := range documents {
					if document.Path == ManifestFileName {
						t.Fatalf("manifest must not be returned as a document")
					}
					byPath[document.Path] = document
				}
				text := byPath["raw/guide.md"]
				if !text.Indexable || text.ContentSnapshot != "# Guide\n" {
					t.Fatalf("text document = %#v", text)
				}
				binary := byPath["raw/diagram.png"]
				if binary.Indexable || binary.ContentSnapshot != "" || binary.SizeBytes != 12 {
					t.Fatalf("binary document = %#v", binary)
				}
				if !isSHA256Hex(hash) {
					t.Fatalf("package hash = %q", hash)
				}
			},
		},
		{
			name: "manifest participates in identity",
			request: SubmitSnapshotRequest{Files: []SnapshotFile{
				{Path: "KNOWLEDGE.yaml", Content: "name: other-kb\n"},
				goodDoc,
			}},
			check: func(t *testing.T, _ Manifest, _ []KnowledgeDocument, hash string) {
				_, _, baseline, err := normalizeSnapshotFiles(snapshotRequest(goodDoc))
				if err != nil {
					t.Fatalf("baseline: %v", err)
				}
				if hash == baseline {
					t.Fatal("changing KNOWLEDGE.yaml must change the package hash")
				}
			},
		},
		{
			name: "manifest include filters documents but keeps identity stable inputs",
			request: SubmitSnapshotRequest{Files: []SnapshotFile{
				{Path: "KNOWLEDGE.yaml", Content: "name: test-kb\ninclude:\n  - \"raw/**/*.md\"\n"},
				goodDoc,
				{Path: "scratch/tmp.md", Content: "ignored\n"},
			}},
			check: func(t *testing.T, _ Manifest, documents []KnowledgeDocument, _ string) {
				if len(documents) != 1 || documents[0].Path != "raw/guide.md" {
					t.Fatalf("documents = %#v", documents)
				}
			},
		},
		{
			name:      "missing files",
			request:   SubmitSnapshotRequest{},
			wantError: "files required",
		},
		{
			name:      "missing manifest",
			request:   SubmitSnapshotRequest{Files: []SnapshotFile{goodDoc}},
			wantError: "root KNOWLEDGE.yaml required",
		},
		{
			name: "manifest without content",
			request: SubmitSnapshotRequest{Files: []SnapshotFile{
				{Path: "KNOWLEDGE.yaml", SHA256: sha256HexString("x"), SizeBytes: 1},
				goodDoc,
			}},
			wantError: "KNOWLEDGE.yaml content required",
		},
		{
			name:      "duplicate path",
			request:   snapshotRequest(goodDoc, SnapshotFile{Path: "raw/guide.md", Content: "dup"}),
			wantError: "duplicate file path",
		},
		{
			name:      "path traversal",
			request:   snapshotRequest(SnapshotFile{Path: "../escape.md", Content: "x"}),
			wantError: "must be relative",
		},
		{
			name:      "sha mismatch",
			request:   snapshotRequest(SnapshotFile{Path: "raw/x.md", Content: "abc", SHA256: sha256HexString("not-abc")}),
			wantError: "sha256 mismatch",
		},
		{
			name:      "size mismatch",
			request:   snapshotRequest(SnapshotFile{Path: "raw/x.md", Content: "abc", SizeBytes: 99}),
			wantError: "size_bytes mismatch",
		},
		{
			name:      "binary without sha",
			request:   snapshotRequest(SnapshotFile{Path: "raw/x.bin", SizeBytes: 4}),
			wantError: "sha256 required",
		},
		{
			name:      "invalid sha",
			request:   snapshotRequest(SnapshotFile{Path: "raw/x.bin", SHA256: "zz", SizeBytes: 4}),
			wantError: "invalid sha256",
		},
		{
			name:      "negative size",
			request:   snapshotRequest(SnapshotFile{Path: "raw/x.bin", SHA256: sha256HexString("x"), SizeBytes: -1}),
			wantError: "size_bytes must be non-negative",
		},
		{
			name: "manifest selects no documents",
			request: SubmitSnapshotRequest{Files: []SnapshotFile{
				{Path: "KNOWLEDGE.yaml", Content: "name: test-kb\ninclude:\n  - \"raw/**\"\n"},
				{Path: "other/file.md", Content: "x"},
			}},
			wantError: "manifest selects no documents",
		},
		{
			name: "declared package hash mismatch",
			request: SubmitSnapshotRequest{
				PackageHash: sha256HexString("wrong"),
				Files:       snapshotRequest(goodDoc).Files,
			},
			wantError: "package_hash does not match",
		},
		{
			name: "declared tree hash mismatch",
			request: SubmitSnapshotRequest{
				TreeHash: sha256HexString("wrong"),
				Files:    snapshotRequest(goodDoc).Files,
			},
			wantError: "tree_hash does not match",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest, documents, hash, err := normalizeSnapshotFiles(testCase.request)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("normalizeSnapshotFiles error = %v, want containing %q", err, testCase.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSnapshotFiles error = %v", err)
			}
			if testCase.check != nil {
				testCase.check(t, manifest, documents, hash)
			}
		})
	}
}

func TestNormalizeSnapshotFilesDeclaredHashRoundTrip(t *testing.T) {
	request := snapshotRequest(SnapshotFile{Path: "raw/guide.md", Content: "# Guide\n"})
	_, _, computed, err := normalizeSnapshotFiles(request)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	request.PackageHash = computed
	request.TreeHash = computed
	_, _, replayed, err := normalizeSnapshotFiles(request)
	if err != nil {
		t.Fatalf("declared hash replay: %v", err)
	}
	if replayed != computed {
		t.Fatalf("hash changed on replay: %s != %s", replayed, computed)
	}
}

func TestNormalizeGitPackageFiles(t *testing.T) {
	files := []gitfetch.File{
		{Path: "KNOWLEDGE.yaml", Content: []byte(manifestAllContent)},
		{Path: "raw/guide.md", Content: []byte("# Guide\n")},
		{Path: "raw/blob.png", Content: []byte{0xff, 0xfe, 0x00, 0x01}},
	}
	manifest, documents, hash, err := normalizeGitPackageFiles(files)
	if err != nil {
		t.Fatalf("normalizeGitPackageFiles error = %v", err)
	}
	if manifest.Name != "test-kb" || len(documents) != 2 || !isSHA256Hex(hash) {
		t.Fatalf("manifest=%#v documents=%d hash=%q", manifest, len(documents), hash)
	}
	for _, document := range documents {
		if document.Path == "raw/blob.png" && (document.Indexable || document.ContentSnapshot != "") {
			t.Fatalf("binary document must not carry a content snapshot: %#v", document)
		}
	}

	if _, _, _, err := normalizeGitPackageFiles([]gitfetch.File{{Path: "raw/only.md", Content: []byte("x")}}); err == nil || !strings.Contains(err.Error(), "root KNOWLEDGE.yaml required") {
		t.Fatalf("missing manifest error = %v", err)
	}
}

func TestGitAndLocalHashConsistency(t *testing.T) {
	gitFiles := []gitfetch.File{
		{Path: "KNOWLEDGE.yaml", Content: []byte(manifestAllContent)},
		{Path: "raw/guide.md", Content: []byte("# Guide\n")},
	}
	_, _, gitHash, err := normalizeGitPackageFiles(gitFiles)
	if err != nil {
		t.Fatalf("git files: %v", err)
	}
	_, _, localHash, err := normalizeSnapshotFiles(snapshotRequest(SnapshotFile{Path: "raw/guide.md", Content: "# Guide\n"}))
	if err != nil {
		t.Fatalf("local files: %v", err)
	}
	if gitHash != localHash {
		t.Fatalf("same package content must produce the same hash: git=%s local=%s", gitHash, localHash)
	}
}

func TestNormalizeSourceRequest(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		request   CreateKnowledgeSourceRequest
		wantError string
		check     func(t *testing.T, normalized CreateKnowledgeSourceRequest)
	}{
		{
			name:    "git source defaults",
			request: CreateKnowledgeSourceRequest{Type: "git", RepositoryURL: "https://github.com/acme/knowledge.git", PackagePath: "product-support"},
			check: func(t *testing.T, normalized CreateKnowledgeSourceRequest) {
				if normalized.SyncMode != "server_pull" || normalized.Status != "active" || normalized.Name != "product-support" {
					t.Fatalf("normalized = %#v", normalized)
				}
			},
		},
		{
			name:    "local source defaults",
			request: CreateKnowledgeSourceRequest{Type: "local", RepositoryURL: "file:///Users/me/kb"},
			check: func(t *testing.T, normalized CreateKnowledgeSourceRequest) {
				if normalized.SyncMode != "client_push" || normalized.Name != "kb" {
					t.Fatalf("normalized = %#v", normalized)
				}
			},
		},
		{
			name:      "bad type",
			request:   CreateKnowledgeSourceRequest{Type: "svn", RepositoryURL: "https://github.com/a/b"},
			wantError: "type must be git or local",
		},
		{
			name:      "git rejects non-provider URL",
			request:   CreateKnowledgeSourceRequest{Type: "git", RepositoryURL: "https://example.com/a/b"},
			wantError: "unsupported Git provider",
		},
		{
			name:      "git rejects client_push",
			request:   CreateKnowledgeSourceRequest{Type: "git", RepositoryURL: "https://github.com/a/b", SyncMode: "client_push"},
			wantError: "git sources must use server_pull",
		},
		{
			name:      "local rejects server_pull",
			request:   CreateKnowledgeSourceRequest{Type: "local", RepositoryURL: "file:///x", SyncMode: "server_pull"},
			wantError: "local sources must use client_push",
		},
		{
			name:      "package path traversal",
			request:   CreateKnowledgeSourceRequest{Type: "local", RepositoryURL: "file:///x", PackagePath: "../escape"},
			wantError: "package_path must be relative",
		},
		{
			name:      "name too long",
			request:   CreateKnowledgeSourceRequest{Type: "local", RepositoryURL: "file:///x", Name: strings.Repeat("n", 161)},
			wantError: "name must be 160 characters or fewer",
		},
		{
			// knowledge_sources is unique per (account_id, name), so a
			// basename-derived name let two domains silently overwrite each
			// other's source.
			name:    "domain qualified path keeps domain in name",
			request: CreateKnowledgeSourceRequest{Type: "git", RepositoryURL: "https://github.com/acme/wiki.git", PackagePath: "platform/retrieval"},
			check: func(t *testing.T, normalized CreateKnowledgeSourceRequest) {
				if normalized.Name != "platform-retrieval" {
					t.Fatalf("Name = %q, want platform-retrieval", normalized.Name)
				}
			},
		},
		{
			name:    "same leaf under another domain gets a distinct name",
			request: CreateKnowledgeSourceRequest{Type: "git", RepositoryURL: "https://github.com/acme/wiki.git", PackagePath: "product/retrieval"},
			check: func(t *testing.T, normalized CreateKnowledgeSourceRequest) {
				if normalized.Name != "product-retrieval" {
					t.Fatalf("Name = %q, want product-retrieval", normalized.Name)
				}
			},
		},
		{
			name:      "bad status",
			request:   CreateKnowledgeSourceRequest{Type: "local", RepositoryURL: "file:///x", Status: "paused"},
			wantError: "status must be active, disabled, or error",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			normalized, err := normalizeSourceRequest(testCase.request)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("normalizeSourceRequest error = %v, want containing %q", err, testCase.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSourceRequest error = %v", err)
			}
			if testCase.check != nil {
				testCase.check(t, normalized)
			}
		})
	}
}
