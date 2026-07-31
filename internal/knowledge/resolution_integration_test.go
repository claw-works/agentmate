package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/skills"
)

const resolutionTestSkill = `---
name: resolution-skill
description: Resolution run integration
triggers: [grounded question]
knowledge:
  mode: discover
  requirements:
    - id: primary-domain
      purpose: Ground answers in domain knowledge
      required: true
      match:
        capabilities: [factual-reference]
      retrieval:
        max_knowledge_bases: 2
        top_k_per_base: 4
---

# Resolution instructions
`

// End to end across the real trust boundary: the contract comes from the skills domain,
// selected bases are verified against the knowledge domain, replays are idempotent, and a
// disagreeing replay is a conflict rather than a second row or a silent overwrite.
func TestRecordResolutionIntegration(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "resolution")
	defer cleanup()
	stranger, strangerCleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "resolution-stranger")
	defer strangerCleanup()

	skillsService := skills.NewService(skills.NewRepo(pool))
	version, err := skillsService.CreateVersion(ctx, owner, skills.CreateVersionRequest{
		SkillName: "resolution-skill", Version: "v1", Content: resolutionTestSkill, Activate: true,
	})
	if err != nil {
		t.Fatalf("create skill version: %v", err)
	}

	service := NewService(NewRepo(pool))
	service.WithSkillContracts(skillsService)
	source := createIntegrationSource(t, ctx, service, owner, "resolution-kb")
	ingest, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: resolution-kb\ncapabilities:\n  - factual-reference\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/guide.md", Content: "# Guide\n"},
	}})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	strangerSource := createIntegrationSource(t, ctx, service, stranger, "resolution-stranger-kb")

	fingerprint := strings.Repeat("cd", 32)
	request := RecordResolutionRequest{
		SkillVersionID:       version.ID,
		RequirementID:        "primary-domain",
		DiscoveryFingerprint: fingerprint,
		DiscoveryStatus:      DiscoveryStatusMatched,
		Candidates:           []ResolutionCandidateSummary{{SourceID: source.ID, Name: "resolution-kb", Score: 1, Rank: 1}},
		Selected:             []ResolutionSelectedBase{{SourceID: source.ID, RevisionID: ingest.Revision.ID}},
		Retrieved:            []ResolutionRetrievedRef{{SourceID: source.ID, DocumentID: "doc-1", ChunkKey: "raw/guide.md#0"}},
		Citations:            []ResolutionCitation{{SourceID: source.ID, Path: "raw/guide.md"}},
		SelectionReason:      "only matching base",
		IdempotencyKey:       "resolution-attempt-1",
	}

	recorded, err := service.RecordResolution(ctx, owner, request)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !recorded.Created || recorded.Run.ID == "" {
		t.Fatalf("record response = %+v", recorded)
	}
	if recorded.Run.ContractIdentity == "" || !strings.Contains(recorded.Run.ContractIdentity, "primary-domain") {
		// The identity is the server's own assertion, never client input.
		t.Fatalf("contract identity not filled by server: %q", recorded.Run.ContractIdentity)
	}

	// Idempotent replay: the original evidence, once.
	replayed, err := service.RecordResolution(ctx, owner, request)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replayed.Created || replayed.Run.ID != recorded.Run.ID {
		t.Fatalf("replay = created %t id %s, want the original row", replayed.Created, replayed.Run.ID)
	}

	// Same key, different content: conflict, not overwrite, not a second row.
	conflicting := request
	conflicting.SelectionReason = "a different account of what happened"
	if _, err := service.RecordResolution(ctx, owner, conflicting); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrResolutionConflict", err)
	}

	// A requirement the compiled contract never declared is corrupt evidence.
	unknown := request
	unknown.RequirementID = "no-such-need"
	unknown.IdempotencyKey = ""
	if _, err := service.RecordResolution(ctx, owner, unknown); err == nil || !strings.Contains(err.Error(), "requirement not found") {
		t.Fatalf("unknown requirement error = %v", err)
	}

	// A selection the account cannot see must be rejected: recording it would defeat
	// the table's authorisation-audit purpose.
	crossTenant := request
	crossTenant.Selected = []ResolutionSelectedBase{{SourceID: strangerSource.ID}}
	crossTenant.IdempotencyKey = ""
	if _, err := service.RecordResolution(ctx, owner, crossTenant); err == nil || !strings.Contains(err.Error(), "source not found") {
		t.Fatalf("cross-tenant selection error = %v", err)
	}

	// The stranger cannot record against a skill version outside their account.
	theirs := request
	theirs.Selected = nil
	theirs.IdempotencyKey = ""
	if _, err := service.RecordResolution(ctx, stranger, theirs); err == nil {
		t.Fatal("stranger recorded a resolution against another account's skill version")
	}

	// A run that selected nothing is evidence too: the fallback path made visible.
	empty := RecordResolutionRequest{
		SkillVersionID:       version.ID,
		RequirementID:        "primary-domain",
		DiscoveryFingerprint: fingerprint,
		DiscoveryStatus:      DiscoveryStatusNoMetadataMatch,
		SelectionReason:      "proceeded without knowledge per fallback",
	}
	emptyRecorded, err := service.RecordResolution(ctx, owner, empty)
	if err != nil {
		t.Fatalf("record empty selection: %v", err)
	}
	if len(emptyRecorded.Run.Selected) != 0 {
		t.Fatalf("empty selection stored as %+v", emptyRecorded.Run.Selected)
	}

	// List: the source_id filter answers "which executions rested on this base".
	bySource, err := service.ListResolutions(ctx, owner.Account(), ResolutionListParams{SourceID: source.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list by source: %v", err)
	}
	if bySource.Total != 1 || bySource.Items[0].ID != recorded.Run.ID {
		t.Fatalf("list by source = %+v", bySource)
	}
	byVersion, err := service.ListResolutions(ctx, owner.Account(), ResolutionListParams{SkillVersionID: version.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list by version: %v", err)
	}
	if byVersion.Total != 2 {
		t.Fatalf("list by version total = %d, want 2", byVersion.Total)
	}
	summary := byVersion.Items[1]
	if summary.SelectedCount != 1 || summary.RetrievedCount != 1 || summary.CitationCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}

	// Full read round-trips the evidence references.
	full, err := service.GetResolution(ctx, owner.Account(), recorded.Run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(full.Selected) != 1 || full.Selected[0].RevisionID != ingest.Revision.ID {
		t.Fatalf("full run selected = %+v", full.Selected)
	}
	if full.DiscoveryFingerprint != fingerprint || full.IdempotencyKey != "resolution-attempt-1" {
		t.Fatalf("full run head = %+v", full)
	}
	// The stranger cannot read it.
	if _, err := service.GetResolution(ctx, stranger.Account(), recorded.Run.ID); err == nil {
		t.Fatal("cross-account read succeeded")
	}
}
