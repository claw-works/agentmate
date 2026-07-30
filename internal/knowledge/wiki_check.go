package knowledge

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// check is the only gate on activation.
//
// Every rule here is machine-decidable and deterministic, which is exactly why it
// can block: the same build always yields the same verdict. The LLM reviewer is
// deliberately kept off this path — its judgement is not reproducible, so letting
// it block would mean a retried build could pass or fail at random.
//
// These rules catch structural failures, which is the large majority of what
// actually needs stopping. Semantic problems ("this summary overstates its
// source") are review's job and only ever annotate.

// checkRule names appear in CheckFailure.Rule so an operator can grep for a
// recurring failure mode.
const (
	ruleCitationRequired   = "citation_required"
	ruleCitationResolvable = "citation_resolvable"
	rulePageKindAllowed    = "page_kind_allowed"
	ruleLinkTypeAllowed    = "link_type_allowed"
	ruleLinkClosed         = "link_closed"
	rulePageCount          = "page_count"
	rulePageSize           = "page_size"
	rulePageCountDrift     = "page_count_drift"
	ruleTokenBudget        = "token_budget"
	ruleIndexCoverage      = "index_coverage"
	rulePathUnique         = "path_unique"
	ruleContentNonEmpty    = "content_non_empty"
	// ruleIncrementalCoverage fails a build that left a scheduled page untouched.
	ruleIncrementalCoverage = "incremental_coverage"
	// ruleIncrementalScope fails a build that changed a page outside its plan.
	ruleIncrementalScope = "incremental_scope"
)

// checkInput is everything the invariants need. Passing a value rather than
// querying inside keeps check pure and therefore trivially testable — an
// important property for the one component allowed to fail a build.
type checkInput struct {
	Profile ProfileVersion
	Pages   []WikiPage
	// KnownDocumentPaths are the paths present in the compiled revision, used to
	// decide whether a citation resolves.
	KnownDocumentPaths map[string]struct{}
	// ParentPageCount is 0 for a full build with no baseline, which disables the
	// drift rule rather than comparing against nothing.
	ParentPageCount int
	TotalTokens     int

	// Incremental is nil for a full build. When set, check additionally verifies that
	// the build did what its plan said: every scheduled page was rewritten or deleted,
	// and nothing outside the plan was touched.
	Incremental *IncrementalPlan
}

// runChecks returns the violated invariants, in a stable order so two runs over
// the same build produce identical output.
func runChecks(in checkInput) []CheckFailure {
	failures := make([]CheckFailure, 0)

	allowedKinds := toSet(in.Profile.AllowedPageKinds)
	allowedLinks := toSet(in.Profile.AllowedLinkTypes)
	pagePaths := make(map[string]int, len(in.Pages))
	for _, page := range in.Pages {
		pagePaths[page.Path]++
	}

	// A duplicate path means two pages claim the same identity; whichever wins is
	// arbitrary, so the build is not usable.
	duplicates := make([]string, 0)
	for path, count := range pagePaths {
		if count > 1 {
			duplicates = append(duplicates, path)
		}
	}
	sort.Strings(duplicates)
	for _, path := range duplicates {
		failures = append(failures, CheckFailure{
			Rule: rulePathUnique, PagePath: path,
			Detail: fmt.Sprintf("%d pages share this path", pagePaths[path]),
		})
	}

	if in.Profile.MaxPages > 0 && len(in.Pages) > in.Profile.MaxPages {
		failures = append(failures, CheckFailure{
			Rule:   rulePageCount,
			Detail: fmt.Sprintf("%d pages exceeds the profile limit of %d", len(in.Pages), in.Profile.MaxPages),
		})
	}

	// Drift guards against a compiler failure that produces a plausible-looking
	// but far smaller or larger wiki. Only meaningful with a baseline.
	if in.ParentPageCount > 0 && in.Profile.MaxPageCountDrift > 0 {
		drift := relativeDrift(len(in.Pages), in.ParentPageCount)
		if drift > in.Profile.MaxPageCountDrift {
			failures = append(failures, CheckFailure{
				Rule: rulePageCountDrift,
				Detail: fmt.Sprintf("page count moved from %d to %d (drift %.2f exceeds %.2f)",
					in.ParentPageCount, len(in.Pages), drift, in.Profile.MaxPageCountDrift),
			})
		}
	}

	if in.Profile.MaxBuildTokens > 0 && in.TotalTokens > in.Profile.MaxBuildTokens {
		failures = append(failures, CheckFailure{
			Rule:   ruleTokenBudget,
			Detail: fmt.Sprintf("%d tokens exceeds the profile budget of %d", in.TotalTokens, in.Profile.MaxBuildTokens),
		})
	}

	// Pages are examined in path order so failures are reported deterministically.
	ordered := append([]WikiPage(nil), in.Pages...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })

	indexTargets := make(map[string]struct{})
	hasIndex := false

	for _, page := range ordered {
		if strings.TrimSpace(page.Content) == "" {
			failures = append(failures, CheckFailure{
				Rule: ruleContentNonEmpty, PagePath: page.Path,
				Detail: "page has no content",
			})
		}
		if _, ok := allowedKinds[page.Kind]; !ok {
			failures = append(failures, CheckFailure{
				Rule: rulePageKindAllowed, PagePath: page.Path,
				Detail: fmt.Sprintf("kind %q is not allowed by the profile", page.Kind),
			})
		}
		if in.Profile.MaxPageChars > 0 {
			if length := utf8.RuneCountInString(page.Content); length > in.Profile.MaxPageChars {
				failures = append(failures, CheckFailure{
					Rule: rulePageSize, PagePath: page.Path,
					Detail: fmt.Sprintf("%d characters exceeds the profile limit of %d", length, in.Profile.MaxPageChars),
				})
			}
		}

		if page.Kind == PageKindIndex {
			hasIndex = true
			for _, link := range page.Links {
				indexTargets[link.TargetPath] = struct{}{}
			}
		}

		// Citation rules. index and log are generated from the build itself
		// rather than from sources, so requiring citations on them would demand
		// evidence for a table of contents.
		requiresCitation := in.Profile.CitationPolicy == CitationPolicyRequired &&
			page.Kind != PageKindIndex && page.Kind != PageKindLog
		if requiresCitation && len(page.Citations) == 0 {
			failures = append(failures, CheckFailure{
				Rule: ruleCitationRequired, PagePath: page.Path,
				Detail: "profile requires citations but the page has none",
			})
		}
		for _, citation := range page.Citations {
			// A citation pointing at a path absent from the compiled revision is
			// unverifiable, which defeats the only mechanism making a generated
			// page checkable.
			if _, ok := in.KnownDocumentPaths[citation.DocumentPath]; !ok {
				failures = append(failures, CheckFailure{
					Rule: ruleCitationResolvable, PagePath: page.Path,
					Detail: fmt.Sprintf("citation targets %q, which is not in the compiled revision", citation.DocumentPath),
				})
			}
		}

		for _, link := range page.Links {
			if _, ok := allowedLinks[link.LinkType]; !ok {
				failures = append(failures, CheckFailure{
					Rule: ruleLinkTypeAllowed, PagePath: page.Path,
					Detail: fmt.Sprintf("link type %q is not allowed by the profile", link.LinkType),
				})
			}
			// Links must close inside the build so a build is a self-consistent
			// graph that can be rolled back as a unit.
			if _, ok := pagePaths[link.TargetPath]; !ok {
				failures = append(failures, CheckFailure{
					Rule: ruleLinkClosed, PagePath: page.Path,
					Detail: fmt.Sprintf("link targets %q, which is not a page in this build", link.TargetPath),
				})
			}
		}
	}

	failures = append(failures, checkIncrementalPlan(in)...)

	// The index is how an agent navigates before drilling in, so a page missing
	// from it is effectively invisible.
	if hasIndex {
		missing := make([]string, 0)
		for _, page := range ordered {
			if page.Kind == PageKindIndex || page.Kind == PageKindLog {
				continue
			}
			if _, ok := indexTargets[page.Path]; !ok {
				missing = append(missing, page.Path)
			}
		}
		for _, path := range missing {
			failures = append(failures, CheckFailure{
				Rule: ruleIndexCoverage, PagePath: path,
				Detail: "page is not linked from the index",
			})
		}
	}

	return failures
}

// checkIncrementalPlan verifies an incremental build against its own plan.
//
// Both rules exist because the structural checks cannot see what they are about. A
// page whose sources moved but whose text was not rewritten still cites a path that
// exists, so every structural rule passes while the page asserts something its source
// no longer says — and an agent reading it cannot tell. That is the same reasoning that
// makes a half-written wiki worse than none, applied to a half-updated one.
//
// The scope rule is the other half. The compiler is handed every page path so it can
// link freely, which also means it can return a page nobody asked it to touch. Nothing
// structural would notice: an overwritten page outside the plan is still a valid page.
// Enforcing the plan here keeps the audit record true — ScheduledPaths is supposed to
// state what this build was allowed to change, and a record that does not bind is not
// a record.
func checkIncrementalPlan(in checkInput) []CheckFailure {
	if in.Incremental == nil {
		return nil
	}
	plan := in.Incremental
	failures := make([]CheckFailure, 0)

	scheduled := toSet(plan.ScheduledPaths)
	recompiled := toSet(plan.RecompiledPaths)
	deleted := toSet(plan.DeletedPaths)

	missing := make([]string, 0)
	for _, path := range plan.ScheduledPaths {
		if _, rewritten := recompiled[path]; rewritten {
			continue
		}
		if _, removed := deleted[path]; removed {
			continue
		}
		missing = append(missing, path)
	}
	sort.Strings(missing)
	for _, path := range missing {
		failures = append(failures, CheckFailure{
			Rule: ruleIncrementalCoverage, PagePath: path,
			Detail: "page was scheduled for rewrite because its sources changed, but the compiler " +
				"neither rewrote nor deleted it; keeping the old text would leave a claim its source no longer supports",
		})
	}

	// RejectedPaths is where the orchestrator recorded every attempt to change a page
	// outside the plan. The attempt was already dropped, so the wiki is intact — but a
	// compiler ignoring its scope is a defect worth surfacing rather than papering over,
	// and the plan would otherwise no longer describe what this build was allowed to do.
	outOfScope := append([]string{}, plan.RejectedPaths...)
	// Belt and braces: a deletion that reached the plan without being scheduled means the
	// enforcement above was bypassed.
	for _, path := range plan.DeletedPaths {
		if _, allowed := scheduled[path]; !allowed {
			outOfScope = append(outOfScope, path)
		}
	}
	sort.Strings(outOfScope)
	for _, path := range outOfScope {
		failures = append(failures, CheckFailure{
			Rule: ruleIncrementalScope, PagePath: path,
			Detail: "page was changed or deleted without being in the plan; the build modified part of " +
				"the wiki its source diff did not touch",
		})
	}
	return failures
}

// relativeDrift measures change as a fraction of the larger count, so growth and
// shrinkage are treated symmetrically: 30→3 and 3→30 are equally suspicious.
func relativeDrift(current, parent int) float64 {
	if parent == 0 {
		return 0
	}
	larger := parent
	if current > larger {
		larger = current
	}
	delta := current - parent
	if delta < 0 {
		delta = -delta
	}
	return float64(delta) / float64(larger)
}

func toSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
