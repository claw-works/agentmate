package knowledge

import (
	"context"
	"strings"
	"testing"
)

// ─── K3.9: validation signals ───

// TestAttributeRefusesToGuess is the centre of K3.9. §7.4 says a signal you cannot attribute
// leaves you with "this account seems unhappy", which has no action attached. The tempting fix
// is to always produce a cause. That is worse: a wrong cause sends someone to fix the wrong
// layer, and then the correct fix looks like it did not work.
func TestAttributeRefusesToGuess(t *testing.T) {
	cases := []struct {
		name      string
		request   RecordSignalRequest
		evidence  signalEvidence
		wantCause string
		wantBasis string
	}{
		{
			name:      "positive signals have no fault to place",
			request:   RecordSignalRequest{Signal: signalAnswerAdopted, PagePath: "wiki/a.md"},
			evidence:  signalEvidence{QueryKnown: true, WikiHitCount: 3},
			wantCause: causeUnattributed,
			wantBasis: "no fault to attribute",
		},
		{
			name:      "no query means nothing separates the candidates",
			request:   RecordSignalRequest{Signal: signalQuestionRepeated, PagePath: "wiki/a.md"},
			evidence:  signalEvidence{},
			wantCause: causeUnattributed,
			wantBasis: "no query was reported",
		},
		{
			name:      "nothing anywhere is a source gap",
			request:   RecordSignalRequest{Signal: signalQuestionRepeated},
			evidence:  signalEvidence{QueryKnown: true},
			wantCause: causeSourceGap,
			wantBasis: "neither the wiki nor the raw layer",
		},
		{
			name:      "raw has material but no wiki page came back",
			request:   RecordSignalRequest{Signal: signalQueryRephrased},
			evidence:  signalEvidence{QueryKnown: true, RawCandidateCount: 5},
			wantCause: causeRetrievalMiss,
			wantBasis: "raw layer had candidates",
		},
		{
			name:      "a page was returned and the question recurred",
			request:   RecordSignalRequest{Signal: signalQuestionRepeated, PagePath: "wiki/a.md"},
			evidence:  signalEvidence{QueryKnown: true, WikiHitCount: 2, RawCandidateCount: 5},
			wantCause: causeWikiSynthesis,
			wantBasis: "question recurred",
		},
		{
			name:      "hits but no page named cannot be placed",
			request:   RecordSignalRequest{Signal: signalAnswerRewritten},
			evidence:  signalEvidence{QueryKnown: true, WikiHitCount: 2, RawCandidateCount: 5},
			wantCause: causeUnattributed,
			wantBasis: "does not say which",
		},
		{
			name:      "citation opened then abandoned is a synthesis problem",
			request:   RecordSignalRequest{Signal: signalCitationAbandoned, PagePath: "wiki/a.md"},
			evidence:  signalEvidence{QueryKnown: true, WikiHitCount: 1},
			wantCause: causeWikiSynthesis,
			wantBasis: "did not settle the claim",
		},
		{
			name:      "an unused source says nothing about any layer",
			request:   RecordSignalRequest{Signal: signalNeverRetrieved},
			evidence:  signalEvidence{},
			wantCause: causeUnattributed,
			wantBasis: "never queried",
		},
	}
	for _, testCase := range cases {
		cause, basis := attribute(testCase.request, testCase.evidence)
		if cause != testCase.wantCause {
			t.Errorf("%s: want %s, got %s (%s)", testCase.name, testCase.wantCause, cause, basis)
		}
		if !strings.Contains(basis, testCase.wantBasis) {
			t.Errorf("%s: basis %q does not mention %q", testCase.name, basis, testCase.wantBasis)
		}
		// Every attribution must be auditable, including the refusals.
		if basis == "" {
			t.Errorf("%s: an attribution with no stated basis is the same dead end as none", testCase.name)
		}
	}
}

// TestSignalVocabularyIsClosed: direction is a property of the signal, not a caller's
// declaration. Letting a caller file answer_rewritten as positive would make every aggregate
// meaningless, and a free-text signal name would let each caller invent its own series.
func TestSignalVocabularyIsClosed(t *testing.T) {
	ctx := context.Background()
	service, worker := newWikiService(t, ctx, &scriptedClient{replies: []string{twoPageReply()}})
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "validation-vocab")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "validation-vocab", "Retention is usually 30 days.")
	compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})

	if _, err := service.RecordSignal(ctx, owner, RecordSignalRequest{
		SourceID: source.ID, Signal: "seems_bad",
	}); err == nil {
		t.Fatalf("an invented signal name must be rejected")
	}
	// The derived signal is computed, never reported: accepting it from a caller would put a
	// biased signal into the one series that is free of reporting bias.
	if _, err := service.RecordSignal(ctx, owner, RecordSignalRequest{
		SourceID: source.ID, Signal: signalNeverRetrieved,
	}); err == nil {
		t.Fatalf("never_retrieved must not be reportable")
	}
	if _, err := service.RecordSignal(ctx, owner, RecordSignalRequest{Signal: signalAnswerAdopted}); err == nil {
		t.Fatalf("a signal without a source cannot be filed")
	}

	// Direction comes from the vocabulary, whatever the caller thinks.
	signal, err := service.RecordSignal(ctx, owner, RecordSignalRequest{
		SourceID: source.ID, Signal: signalAnswerRewritten, PagePath: "wiki/overview.md",
	})
	if err != nil {
		t.Fatalf("record signal: %v", err)
	}
	if signal.Direction != signalDirectionNegative {
		t.Fatalf("answer_rewritten is negative regardless of who files it, got %s", signal.Direction)
	}
	if signal.Origin != signalOriginReported {
		t.Fatalf("a caller-supplied signal is reported, got %s", signal.Origin)
	}
}

// TestSignalRecordsTheBuildThatWasServing: a complaint belongs to the wiki that produced it.
// Rewriting it onto the current build would blame a recompile for its predecessor's faults.
func TestSignalRecordsTheBuildThatWasServing(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{
		twoPageReply(),
		wikiReply(wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Rewritten.",
			[]string{"raw/overview.md"}, nil)),
	}}
	service, worker := newWikiService(t, ctx, compiler)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "validation-build")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "validation-build", "Retention is usually 30 days.")

	first := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})

	// A signal with no build_id lands on whatever is serving.
	current, err := service.RecordSignal(ctx, owner, RecordSignalRequest{
		SourceID: source.ID, Signal: signalAnswerAdopted, PagePath: "wiki/overview.md",
	})
	if err != nil {
		t.Fatalf("record signal: %v", err)
	}
	if current.BuildID == nil || *current.BuildID != first.ID {
		t.Fatalf("a signal should attach to the serving build")
	}

	// A late report about the older wiki must be able to say so.
	late, err := service.RecordSignal(ctx, owner, RecordSignalRequest{
		SourceID: source.ID, BuildID: first.ID, Signal: signalQuestionRepeated,
		PagePath: "wiki/overview.md", Detail: "asked again the next day",
	})
	if err != nil {
		t.Fatalf("record late signal: %v", err)
	}
	if late.BuildID == nil || *late.BuildID != first.ID {
		t.Fatalf("an explicit build_id must be honoured, got %v", late.BuildID)
	}
}

// TestSignalSummarySeparatesReportedFromDerived: an absence of complaints is not evidence of
// success, and a summary that merged the two origins would hide exactly that.
func TestSignalSummarySeparatesReportedFromDerived(t *testing.T) {
	ctx := context.Background()
	service, worker := newWikiService(t, ctx, &scriptedClient{replies: []string{twoPageReply()}})
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "validation-summary")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "validation-summary", "Retention is usually 30 days.")
	compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})

	for _, spec := range []struct {
		signal string
		page   string
	}{
		{signalAnswerAdopted, "wiki/overview.md"},
		{signalCitationVerified, "wiki/overview.md"},
		{signalQuestionRepeated, "wiki/details.md"},
		{signalCitationAbandoned, "wiki/details.md"},
	} {
		if _, err := service.RecordSignal(ctx, owner, RecordSignalRequest{
			SourceID: source.ID, Signal: spec.signal, PagePath: spec.page,
		}); err != nil {
			t.Fatalf("record %s: %v", spec.signal, err)
		}
	}

	summary, err := service.SignalSummary(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Total != 4 || summary.Positive != 2 || summary.Negative != 2 {
		t.Fatalf("counts wrong: %+v", summary)
	}
	if summary.Reported != 4 || summary.Derived != 0 {
		t.Fatalf("origins must be separated: reported=%d derived=%d", summary.Reported, summary.Derived)
	}
	// Pages carrying negatives sort first, because that is what someone acting on this needs
	// to see without paging.
	if len(summary.ByPage) != 2 || summary.ByPage[0].Key != "wiki/details.md" {
		t.Fatalf("pages with negatives must lead: %+v", summary.ByPage)
	}
	causes := make(map[string]int, len(summary.ByCause))
	for _, count := range summary.ByCause {
		causes[count.Key] = count.Negative
	}
	// citation_abandoned attributes to synthesis; question_repeated without a query does not.
	if causes[causeWikiSynthesis] != 1 {
		t.Fatalf("want one synthesis-attributed negative, got %+v", summary.ByCause)
	}
	if causes[causeUnattributed] != 1 {
		t.Fatalf("the unattributable negative must be visible as such, got %+v", summary.ByCause)
	}
}

// TestSweepNeverRetrievedIsIdempotentPerDay: the derived signal states one unchanged fact. A
// sweep on a timer must not turn it into a rising trend.
func TestSweepNeverRetrievedIsIdempotentPerDay(t *testing.T) {
	ctx := context.Background()
	service, worker := newWikiService(t, ctx, &scriptedClient{replies: []string{twoPageReply()}})
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "validation-sweep")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "validation-sweep", "Retention is usually 30 days.")
	compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})

	// A freshly updated source is not idle yet, so nothing is recorded.
	fresh, err := service.SweepNeverRetrieved(ctx, owner, 14)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if fresh.Recorded != 0 {
		t.Fatalf("a source updated moments ago is not idle: %+v", fresh)
	}

	// Age it past the idle window.
	if _, err := pool.Exec(ctx,
		`UPDATE knowledge_sources SET updated_at = NOW() - INTERVAL '30 days' WHERE id::text = $1`,
		source.ID); err != nil {
		t.Fatalf("age source: %v", err)
	}

	first, err := service.SweepNeverRetrieved(ctx, owner, 14)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if first.Recorded != 1 {
		t.Fatalf("want one derived signal, got %+v", first)
	}
	second, err := service.SweepNeverRetrieved(ctx, owner, 14)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.Recorded != 0 || second.AlreadyRecorded != 1 {
		t.Fatalf("a repeat sweep must add nothing and say so: %+v", second)
	}

	summary, err := service.SignalSummary(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Derived != 1 || summary.Reported != 0 {
		t.Fatalf("the sweep's signal must be marked derived: %+v", summary)
	}
	if summary.Negative != 1 {
		t.Fatalf("never_retrieved is a negative signal: %+v", summary)
	}
	// It must not claim to know which layer failed.
	signals, err := service.ListSignals(ctx, owner.Account(), SignalFilter{SourceID: source.ID}, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(signals.Items) != 1 || signals.Items[0].Cause != causeUnattributed {
		t.Fatalf("an unused source carries no attribution: %+v", signals.Items)
	}
	if signals.Items[0].PagePath != "" {
		t.Fatalf("never_retrieved is about a source, not a page: %q", signals.Items[0].PagePath)
	}
}
