package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/claw-works/agentmate/internal/ownership"
)

// ─── K3.9 (part 1): validation signals and attribution ───
//
// §7.3 defines validation as the confirmation carried by behaviour rather than by anyone
// filling in a rating. It also names three defects that have to be designed for instead of
// wished away: the signal is biased (only active users produce it), lagging (a worse build
// serves for weeks before it shows), and sparse (small accounts produce almost none).
// Those are the reasons validation measures long-term quality and can never be a gate.
//
// One thing the design did not anticipate: here the consumer is an agent calling MCP tools,
// not a person clicking. An agent adopting an answer cannot be observed — it has to say so.
// That makes most signals self-reported, which sharpens the bias defect rather than
// softening it: an agent that never reports is indistinguishable from one that had no
// trouble. So every signal records its origin, and only never_retrieved is derived from data
// the platform already holds.

// Reported signals. The vocabulary is closed: a free-text signal name would let each caller
// invent its own, and a series nobody can compare across callers measures nothing.
const (
	// Positive.
	signalAnswerAdopted    = "answer_adopted"
	signalCitationVerified = "citation_verified"
	signalReusedForSimilar = "reused_for_similar"

	// Negative.
	signalQuestionRepeated  = "question_repeated"
	signalQueryRephrased    = "query_rephrased"
	signalCitationAbandoned = "citation_abandoned"
	signalAnswerRewritten   = "answer_rewritten"

	// Derived, not reported: computed by sweeping sources that have never been retrieved.
	signalNeverRetrieved = "never_retrieved"
)

const (
	signalDirectionPositive = "positive"
	signalDirectionNegative = "negative"

	signalOriginReported = "reported"
	signalOriginDerived  = "derived"
)

// signalDirections is also the allow-list. Direction is a property of the signal, not
// something a caller may declare: letting a caller file answer_rewritten as positive would
// make aggregate counts meaningless.
var signalDirections = map[string]string{
	signalAnswerAdopted:     signalDirectionPositive,
	signalCitationVerified:  signalDirectionPositive,
	signalReusedForSimilar:  signalDirectionPositive,
	signalQuestionRepeated:  signalDirectionNegative,
	signalQueryRephrased:    signalDirectionNegative,
	signalCitationAbandoned: signalDirectionNegative,
	signalAnswerRewritten:   signalDirectionNegative,
	signalNeverRetrieved:    signalDirectionNegative,
}

// Attribution causes, from §7.4. Four ways the same complaint can arise, each with a
// different fix — plus the honest fifth.
const (
	causeUnattributed  = "unattributed"
	causeWikiSynthesis = "wiki_synthesis"
	causeRetrievalMiss = "retrieval_miss"
	causeSourceGap     = "source_gap"
	causeSkillApproach = "skill_approach"
)

// attribute decides what a signal points at, and refuses to guess.
//
// §7.4's point is that a signal you cannot attribute leaves you with "this account seems
// unhappy", which has no action attached. The temptation is to make something up so every
// signal gets a cause. That is worse: a wrong cause sends someone to fix the wrong layer and
// then the correct fix looks like it did not work.
//
// So attribution runs on evidence and returns unattributed whenever the evidence does not
// separate the candidates. The basis string always says which branch fired, because an
// attribution nobody can audit is the same dead end as no attribution.
func attribute(in RecordSignalRequest, evidence signalEvidence) (string, string) {
	if signalDirections[in.Signal] == signalDirectionPositive {
		// Nothing to attribute: attribution answers "which layer is at fault", and a
		// working answer has no fault to place.
		return causeUnattributed, "positive signals name no fault to attribute"
	}

	switch in.Signal {
	case signalNeverRetrieved:
		// A source nobody queried says nothing about any layer's quality — it says the
		// source was registered and then not used, which may simply mean nobody had the
		// question.
		return causeUnattributed, "a source that was never queried carries no evidence about which layer failed"

	case signalCitationAbandoned:
		// The agent opened a citation and immediately re-queried: it read the source and
		// the source did not settle the question. The page pointed at text that did not
		// support what the page said, which is a synthesis problem.
		if in.PagePath != "" {
			return causeWikiSynthesis, "a citation was opened and then abandoned, so the page led to source text that did not settle the claim"
		}
		return causeUnattributed, "citation_abandoned without a page cannot be placed"

	case signalQuestionRepeated, signalQueryRephrased, signalAnswerRewritten:
		switch {
		case !evidence.QueryKnown:
			// Without knowing what the retrieval returned, every one of the four causes
			// remains possible.
			return causeUnattributed, "no query was reported, so nothing distinguishes a retrieval miss from a synthesis error"
		case evidence.WikiHitCount == 0 && evidence.RawCandidateCount == 0:
			// Nothing at either layer had anything to say. The fact is not in the sources.
			return causeSourceGap, "neither the wiki nor the raw layer returned anything for this query"
		case evidence.WikiHitCount == 0 && evidence.RawCandidateCount > 0:
			// The raw layer holds material and the wiki did not surface a page: either the
			// wiki does not cover it or search did not rank it. Both are retrieval-side.
			return causeRetrievalMiss, "the raw layer had candidates but no wiki page was returned"
		case in.PagePath != "":
			// A page was returned, used, and the question came back.
			return causeWikiSynthesis, "a wiki page was returned for this query and the question recurred"
		default:
			return causeUnattributed, "a wiki page was returned but the signal does not say which, so the fault cannot be placed"
		}
	}
	return causeUnattributed, "no attribution rule matches this signal"
}

// signalEvidence is what the platform can verify about the query a signal followed.
type signalEvidence struct {
	QueryKnown        bool
	WikiHitCount      int
	RawCandidateCount int
}

// RecordSignal stores one reported validation signal.
//
// It deliberately accepts signals about builds that are no longer active: a complaint is
// about the wiki that was serving when it happened, and rewriting it onto the current build
// would blame a recompile for the previous version's faults.
func (s *Service) RecordSignal(ctx context.Context, owner ownership.Owner, req RecordSignalRequest) (*ValidationSignal, error) {
	req.SourceID = strings.TrimSpace(req.SourceID)
	req.Signal = strings.ToLower(strings.TrimSpace(req.Signal))
	req.PagePath = strings.TrimSpace(req.PagePath)
	req.QueryID = strings.TrimSpace(req.QueryID)

	if req.SourceID == "" {
		return nil, fmt.Errorf("source_id required")
	}
	direction, known := signalDirections[req.Signal]
	if !known {
		return nil, fmt.Errorf("unknown signal %q; allowed: %s", req.Signal, strings.Join(reportableSignals(), ", "))
	}
	if req.Signal == signalNeverRetrieved {
		// Derived signals are computed, never reported. Accepting one from a caller would
		// put a biased signal into the only series that is free of reporting bias.
		return nil, fmt.Errorf("%s is derived by the platform and cannot be reported", signalNeverRetrieved)
	}

	source, err := s.repo.GetSource(ctx, owner.Account(), req.SourceID)
	if err != nil {
		return nil, err
	}

	evidence, err := s.repo.SignalEvidence(ctx, owner.Account(), req.QueryID)
	if err != nil {
		return nil, err
	}
	cause, basis := attribute(req, evidence)

	buildID := req.BuildID
	if strings.TrimSpace(buildID) == "" && source.ActiveBuildID != nil {
		buildID = *source.ActiveBuildID
	}
	return s.repo.InsertValidationSignal(ctx, owner, insertSignalInput{
		SourceID: source.ID, BuildID: strings.TrimSpace(buildID), PagePath: req.PagePath,
		QueryID: req.QueryID, Signal: req.Signal, Direction: direction,
		Origin: signalOriginReported, Cause: cause, AttributionBasis: basis, Detail: req.Detail,
	})
}

func reportableSignals() []string {
	names := make([]string, 0, len(signalDirections)-1)
	for name := range signalDirections {
		if name == signalNeverRetrieved {
			continue
		}
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// SweepNeverRetrieved records a derived signal for every source that has an active wiki but
// has never appeared in a retrieval query.
//
// This is the one signal with no reporting bias, which makes it the most trustworthy and the
// least specific: it says a knowledge base is unused, not that it is wrong. Runs at most once
// per source per day, enforced in the schema — a sweep on a timer would otherwise turn one
// unchanged fact into a rising trend.
func (s *Service) SweepNeverRetrieved(ctx context.Context, owner ownership.Owner, idleDays int) (*SignalSweepResponse, error) {
	if idleDays <= 0 {
		idleDays = 14
	}
	recorded, skipped, err := s.repo.SweepNeverRetrieved(ctx, owner, idleDays)
	if err != nil {
		return nil, err
	}
	return &SignalSweepResponse{
		Recorded: recorded, AlreadyRecorded: skipped, IdleDays: idleDays,
	}, nil
}

// ListSignals returns raw signals, newest first.
func (s *Service) ListSignals(ctx context.Context, accountID string, filter SignalFilter, limit, offset int) (*SignalListResponse, error) {
	items, total, err := s.repo.ListValidationSignals(ctx, accountID, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return &SignalListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// SignalSummary aggregates signals per page and per cause for one source.
//
// The aggregate is the point: a single negative signal is noise, and §7.3's sparsity defect
// means small knowledge bases will never produce enough to be sure. So the summary reports
// counts and never a verdict, and it separates reported from derived so a reader can tell
// how much of the picture came from callers choosing to speak.
func (s *Service) SignalSummary(ctx context.Context, accountID, sourceID string) (*SignalSummaryResponse, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, fmt.Errorf("source_id required")
	}
	return s.repo.SummariseValidationSignals(ctx, accountID, sourceID)
}
