package skills

import "testing"

func TestNormalizeSourceRequestLocalDefaults(t *testing.T) {
	req, err := normalizeSourceRequest(CreateSkillSourceRequest{
		Type:          "local",
		RepositoryURL: "file:///Users/me/.agents/skills",
		PackagePath:   "./domain-web",
	})
	if err != nil {
		t.Fatalf("normalizeSourceRequest error: %v", err)
	}
	if req.Name != "domain-web" {
		t.Fatalf("Name = %q, want domain-web", req.Name)
	}
	if req.SyncMode != "client_push" {
		t.Fatalf("SyncMode = %q, want client_push", req.SyncMode)
	}
	if req.Visibility != "private" {
		t.Fatalf("Visibility = %q, want private", req.Visibility)
	}
	if req.Status != "active" {
		t.Fatalf("Status = %q, want active", req.Status)
	}
}

func TestNormalizeSnapshotFiles(t *testing.T) {
	req := SubmitLocalSnapshotRequest{
		Files: []SnapshotFile{
			{Path: "./scripts/run.sh", Content: "echo smoke\n"},
			{Path: "SKILL.md", Content: "---\nname: smoke-skill\ndescription: Smoke\n---\n\n# Smoke\n"},
		},
	}
	files, skillContent, packageHash, treeHash, err := normalizeSnapshotFiles(req)
	if err != nil {
		t.Fatalf("normalizeSnapshotFiles error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if skillContent == "" {
		t.Fatal("skillContent is empty")
	}
	if packageHash == "" || treeHash == "" {
		t.Fatalf("hashes should be populated: package=%q tree=%q", packageHash, treeHash)
	}
	if packageHash != treeHash {
		t.Fatalf("treeHash = %q, want packageHash %q", treeHash, packageHash)
	}

	var foundSkill bool
	for _, file := range files {
		if file.Path == "SKILL.md" {
			foundSkill = true
			if file.Kind != "instruction" {
				t.Fatalf("SKILL.md kind = %q, want instruction", file.Kind)
			}
			if !file.Indexable {
				t.Fatal("SKILL.md should be indexable")
			}
			if file.SHA256 != sha256HexString(file.ContentSnapshot) {
				t.Fatal("SKILL.md sha256 should match content snapshot")
			}
		}
	}
	if !foundSkill {
		t.Fatal("SKILL.md not found")
	}
}

func TestNormalizeSnapshotFilesRequiresRootSkill(t *testing.T) {
	_, _, _, _, err := normalizeSnapshotFiles(SubmitLocalSnapshotRequest{
		Files: []SnapshotFile{{Path: "docs/SKILL.md", Content: "# Nested\n"}},
	})
	if err == nil {
		t.Fatal("expected missing root SKILL.md error")
	}
}

func TestNormalizeSnapshotFilesRejectsSHAMismatch(t *testing.T) {
	_, _, _, _, err := normalizeSnapshotFiles(SubmitLocalSnapshotRequest{
		Files: []SnapshotFile{{
			Path:    "SKILL.md",
			SHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Content: "# Different content\n",
		}},
	})
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
}
