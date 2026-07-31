package knowledge

import (
	"strings"
	"testing"
)

func validResolutionRequest() RecordResolutionRequest {
	return RecordResolutionRequest{
		SkillVersionID:       "version-1",
		RequirementID:        "primary-domain",
		DiscoveryFingerprint: strings.Repeat("ab", 32),
		DiscoveryStatus:      DiscoveryStatusMatched,
		Selected:             []ResolutionSelectedBase{{SourceID: "source-1"}},
		Retrieved:            []ResolutionRetrievedRef{{DocumentID: "doc-1", ChunkKey: "raw/guide.md#0"}},
		Citations:            []ResolutionCitation{{DocumentID: "doc-1", Path: "raw/guide.md"}},
		SelectionReason:      "highest capability overlap",
	}
}

func TestValidateResolutionRequest(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		mutate  func(*RecordResolutionRequest)
		wantErr string
	}{
		{"valid", func(*RecordResolutionRequest) {}, ""},
		{"missing version", func(r *RecordResolutionRequest) { r.SkillVersionID = "" }, "skill_version_id required"},
		{"missing requirement", func(r *RecordResolutionRequest) { r.RequirementID = "" }, "requirement_id required"},
		{"bad fingerprint", func(r *RecordResolutionRequest) { r.DiscoveryFingerprint = "deadbeef" }, "discovery_fingerprint"},
		{"unknown status", func(r *RecordResolutionRequest) { r.DiscoveryStatus = "resolved" }, "not a discovery status"},
		{"selected without source", func(r *RecordResolutionRequest) { r.Selected = []ResolutionSelectedBase{{}} }, "source_id required"},
		{"retrieved without any reference", func(r *RecordResolutionRequest) { r.Retrieved = []ResolutionRetrievedRef{{SourceID: "s"}} }, "document_id or page_path required"},
		{"citation without any reference", func(r *RecordResolutionRequest) { r.Citations = []ResolutionCitation{{SourceID: "s"}} }, "document_id or path required"},
		{"confidence out of range", func(r *RecordResolutionRequest) { two := 2.0; r.Confidence = &two }, "between 0 and 1"},
		{"too many selected", func(r *RecordResolutionRequest) {
			r.Selected = make([]ResolutionSelectedBase, maxResolutionSelectedBases+1)
			for i := range r.Selected {
				r.Selected[i].SourceID = "s"
			}
		}, "platform ceiling"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			req := validResolutionRequest()
			testCase.mutate(&req)
			normalizeResolutionRequest(&req)
			err := validateResolutionRequest(req)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, testCase.wantErr)
			}
		})
	}
}

// The idempotency key names the attempt, not the content: replaying under a different key
// must still hash identically, and any content change must move the hash — that pair is
// what makes the replay-conflict rule mean something.
func TestResolutionContentHashExcludesIdempotencyKey(t *testing.T) {
	first := validResolutionRequest()
	second := validResolutionRequest()
	second.IdempotencyKey = "retry-1"

	hashFirst, err := resolutionContentHash(first)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	hashSecond, err := resolutionContentHash(second)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hashFirst != hashSecond {
		t.Fatal("the idempotency key leaked into the content hash")
	}

	changed := validResolutionRequest()
	changed.SelectionReason = "different reason"
	hashChanged, err := resolutionContentHash(changed)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hashChanged == hashFirst {
		t.Fatal("a content change did not move the hash")
	}
}
