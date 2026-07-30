package knowledge

import (
	"encoding/json"
	"errors"
	"time"
)

// ─── K3: compiled wiki layer ───
//
// Design: docs/knowledge-wiki-compiler-k3-v0.1.md
//
// A build is not reproducible: compiling the same sources twice with the same
// model yields different text. That single fact drives the shape here — builds
// are immutable and retained, provenance is complete, and content hashes serve
// diffing rather than identity.

// Page kinds. The set is validated against the profile version, so a profile can
// forbid kinds it does not want without the code changing.
const (
	PageKindSummary   = "summary"
	PageKindEntity    = "entity"
	PageKindConcept   = "concept"
	PageKindOverview  = "overview"
	PageKindSynthesis = "synthesis"
	PageKindIndex     = "index"
	PageKindLog       = "log"
)

// Typed link kinds, from architecture §14.5.
const (
	LinkReferences     = "references"
	LinkContradicts    = "contradicts"
	LinkSupersedes     = "supersedes"
	LinkElaborates     = "elaborates"
	LinkMentionsEntity = "mentions_entity"
)

// Build lifecycle.
const (
	BuildStatusQueued    = "queued"
	BuildStatusRunning   = "running"
	BuildStatusSucceeded = "succeeded"
	BuildStatusFailed    = "failed"
	BuildStatusCancelled = "cancelled"

	CheckStatusPending = "pending"
	CheckStatusPassed  = "passed"
	CheckStatusFailed  = "failed"

	ReviewStatusSkipped = "skipped"
	ReviewStatusClean   = "clean"
	ReviewStatusFlagged = "flagged"
	ReviewStatusFailed  = "failed"

	BuildModeFull        = "full"
	BuildModeIncremental = "incremental"

	CitationPolicyRequired  = "required"
	CitationPolicyPreferred = "preferred"
	CitationPolicyOptional  = "optional"
)

// CompilerVersion identifies the compilation implementation. It is part of build
// provenance: changing how pages are assembled changes the output even when the
// model and prompt are untouched.
const CompilerVersion = "wiki-compiler-2"

// PromptVersion identifies the prompt text. Same reasoning as CompilerVersion —
// editing a prompt silently alters every future build, so the edit must be
// visible in provenance.
const PromptVersion = "wiki-prompt-2"

// ReviewerPromptVersion identifies the review prompt, versioned for the same
// reason as PromptVersion: the review standard must be explicit and auditable,
// and it must never be relaxed silently.
const ReviewerPromptVersion = "wiki-review-prompt-1"

type KnowledgeProfile struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProfileVersion is immutable. A profile edit creates a new version because the
// conventions it declares shape compiler output.
type ProfileVersion struct {
	ID               string   `json:"id"`
	AccountID        string   `json:"account_id"`
	ProfileID        string   `json:"profile_id"`
	Version          int      `json:"version"`
	AllowedPageKinds []string `json:"allowed_page_kinds"`
	AllowedLinkTypes []string `json:"allowed_link_types"`
	CitationPolicy   string   `json:"citation_policy"`
	MaxPages         int      `json:"max_pages"`
	MaxPageChars     int      `json:"max_page_chars"`
	MaxBuildTokens   int      `json:"max_build_tokens"`
	// MaxPageCountDrift bounds how far a build's page count may move from its
	// parent's. A build collapsing 30 pages into 3 is far more likely to be a
	// compiler failure than a legitimate rewrite.
	MaxPageCountDrift float64   `json:"max_page_count_drift"`
	Instructions      string    `json:"instructions,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type BuildRevision struct {
	ID               string  `json:"id"`
	AccountID        string  `json:"account_id"`
	UserID           *string `json:"user_id,omitempty"`
	KeyID            *string `json:"key_id,omitempty"`
	SourceID         string  `json:"source_id"`
	SourceRevisionID string  `json:"source_revision_id"`
	RawPackageHash   string  `json:"raw_package_hash"`
	ProfileVersionID string  `json:"profile_version_id"`

	CompilerVersion string `json:"compiler_version"`
	Model           string `json:"model"`
	PromptVersion   string `json:"prompt_version"`
	// Reviewer provenance. ReviewerIndependence records how separated the
	// reviewer actually was — cross_provider, same_provider, same_model or
	// unavailable — so the collusion risk of a build is visible in the data
	// rather than depending on someone recalling the configuration.
	ReviewerModel         string `json:"reviewer_model,omitempty"`
	ReviewerPromptVersion string `json:"reviewer_prompt_version,omitempty"`
	ReviewerIndependence  string `json:"reviewer_independence"`

	ParentBuildID *string `json:"parent_build_id,omitempty"`
	Mode          string  `json:"mode"`
	Status        string  `json:"status"`

	// CheckStatus is the only gate. ReviewStatus annotates and never blocks
	// activation: an unreproducible verdict must not sit on the blocking path.
	CheckStatus   string          `json:"check_status"`
	CheckFailures json.RawMessage `json:"check_failures,omitempty"`
	ReviewStatus  string          `json:"review_status"`

	PagesWritten     int   `json:"pages_written"`
	PagesReused      int   `json:"pages_reused"`
	InputTokens      int   `json:"input_tokens"`
	OutputTokens     int   `json:"output_tokens"`
	CostMicros       int64 `json:"cost_micros"`
	ReviewTokens     int   `json:"review_tokens"`
	ReviewCostMicros int64 `json:"review_cost_micros"`

	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`

	// ─── queue state (K3.3) ───
	//
	// The lease lives on the build rather than in a separate jobs table, so there
	// is exactly one row that answers "is this build running" — two rows can
	// disagree after a crash, which is when the answer matters most.

	// LeaseOwner is an opaque worker identity: host plus a process-lifetime
	// nonce, so a restarted process never inherits its own stale lease.
	LeaseOwner     string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	// Attempt counts claims rather than failures, so a build that silently kills
	// every worker touching it still runs out of attempts.
	Attempt       int       `json:"attempt"`
	MaxAttempts   int       `json:"max_attempts"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	QueuedAt      time.Time `json:"queued_at"`
	// ActivateOnSuccess carries the request's intent across the queue: the caller
	// is long gone by the time a worker runs, so the intent lives on the job.

	ActivateOnSuccess bool `json:"activate_on_success"`

	// IsActive is derived from the source pointer, not stored on the build, so
	// there is exactly one place that decides which build is current.
	IsActive bool `json:"is_active"`
}

type WikiPage struct {
	ID          string          `json:"id"`
	AccountID   string          `json:"account_id"`
	BuildID     string          `json:"build_id"`
	Path        string          `json:"path"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title,omitempty"`
	Content     string          `json:"content,omitempty"`
	Frontmatter json.RawMessage `json:"frontmatter,omitempty"`
	// ContentHash exists for diffing and incremental reuse. It is deliberately
	// NOT an idempotency key: equal hashes do not prove logical equivalence.
	ContentHash string `json:"content_hash"`
	// DerivedFromBuildID is set when the page was copied unchanged from the
	// parent build, which is how incremental compilation keeps cost down.
	DerivedFromBuildID *string   `json:"derived_from_build_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`

	Citations []PageCitation `json:"citations,omitempty"`
	Links     []PageLink     `json:"links,omitempty"`
}

type PageLink struct {
	ID           string `json:"id"`
	BuildID      string `json:"build_id"`
	SourcePageID string `json:"source_page_id"`
	// TargetPageID is nil when the target path does not exist in this build. The
	// dangling link is kept because it is a useful lint signal.
	TargetPageID *string   `json:"target_page_id,omitempty"`
	TargetPath   string    `json:"target_path"`
	LinkType     string    `json:"link_type"`
	Note         string    `json:"note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	// Direction is set when links are listed for one page.
	Direction string `json:"direction,omitempty"`
}

// PageCitation anchors one claim on a page to a location in the raw layer. This
// is the credibility basis of the wiki: the page is LLM-generated, so only the
// citation makes a claim checkable.
type PageCitation struct {
	ID      string `json:"id"`
	BuildID string `json:"build_id"`
	PageID  string `json:"page_id"`
	// DocumentID is nil when the compiler cited a path absent from the revision.
	// check treats that as a failure, but the row is stored so the failure can be
	// reported precisely rather than as a bare count.
	DocumentID   *string   `json:"document_id,omitempty"`
	DocumentPath string    `json:"document_path"`
	HeadingPath  string    `json:"heading_path,omitempty"`
	ChunkKey     string    `json:"chunk_key,omitempty"`
	Claim        string    `json:"claim,omitempty"`
	Excerpt      string    `json:"excerpt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type BuildEvent struct {
	ID         string          `json:"id"`
	BuildID    string          `json:"build_id"`
	SequenceNo int             `json:"sequence_no"`
	EventType  string          `json:"event_type"`
	PagePath   string          `json:"page_path,omitempty"`
	Detail     string          `json:"detail,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// Build event types. The log page is a rendering of these.
const (
	BuildEventStarted     = "started"
	BuildEventSourceRead  = "source_read"
	BuildEventPageWritten = "page_written"
	BuildEventPageReused  = "page_reused"
	// BuildEventPageRejected records a model page the platform refused, so a
	// dropped page is auditable rather than invisible.
	BuildEventPageRejected = "page_rejected"
	// BuildEventPageDeleted records a page the compiler declared unsupported by its
	// sources. A page it merely omits is treated as unchanged, so removal has to be
	// stated explicitly — the two are otherwise indistinguishable.
	BuildEventPageDeleted = "page_deleted"
	// BuildEventPlanned records the incremental plan: what the raw diff was and how
	// much of the wiki it touched. Without it, a build that reused the wrong pages
	// leaves no trace of the decision that produced it.
	BuildEventPlanned     = "planned"
	BuildEventCheckFailed = "check_failed"
	BuildEventCheckPassed = "check_passed"
	BuildEventActivated   = "activated"
	BuildEventFailed      = "failed"
	BuildEventFinished    = "finished"
)

// ─── requests and responses ───

type CompileRequest struct {
	SourceID string `json:"source_id"`
	// Mode is full or incremental. Incremental needs a previous succeeded build to
	// diff against, and is refused rather than silently downgraded to full when
	// there is none: a caller that asked for incremental and got a full rebuild
	// would believe it saved cost it did not save.
	Mode string `json:"mode"`
	// Force recompiles even when a succeeded build already exists for the same
	// input identity. Used when an operator suspects the previous output was poor.
	Force bool `json:"force"`
	// Activate defaults to true: a build that passes check is activated
	// automatically, because there is no human approval gate by design (§2.3).
	Activate *bool `json:"activate"`
}

type CompileResponse struct {
	Build *BuildRevision `json:"build"`
	// Reused is true when an existing build matched the input identity and no
	// compilation was performed.
	Reused bool `json:"reused"`
	// Activated reports whether the active pointer moved to this build.
	Activated bool     `json:"activated"`
	Pages     []string `json:"pages,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// CheckFailure is one violated invariant. Machine-checkable and deterministic,
// which is why check can gate activation while review cannot.
type CheckFailure struct {
	Rule     string `json:"rule"`
	PagePath string `json:"page_path,omitempty"`
	Detail   string `json:"detail"`
}

type BuildListResponse struct {
	Items  []BuildRevision `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type PageListResponse struct {
	BuildID string     `json:"build_id"`
	Items   []WikiPage `json:"items"`
	Total   int        `json:"total"`
}

// BuildDiff compares two builds page by page. It exists because builds are not
// reproducible: an operator has to be able to answer "why did this page change"
// without re-running anything.
type BuildDiff struct {
	FromBuildID string          `json:"from_build_id"`
	ToBuildID   string          `json:"to_build_id"`
	Added       []string        `json:"added"`
	Removed     []string        `json:"removed"`
	Changed     []string        `json:"changed"`
	Unchanged   int             `json:"unchanged"`
	Summary     BuildDiffCounts `json:"summary"`
}

type BuildDiffCounts struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

type ActivateBuildResponse struct {
	Build *BuildRevision `json:"build"`
	// PreviousBuildID is reported so a rollback can be undone by activating it
	// again.
	PreviousBuildID *string `json:"previous_build_id,omitempty"`
}

// RevisionDiff compares the raw documents of two source revisions. It is the input
// to incremental compilation: what changed in the sources decides what has to be
// recompiled.
//
// Unlike BuildDiff, content hashes are load-bearing here. Raw documents are authored
// by people, so identical bytes really do mean identical content — the reasoning
// that disqualifies hashes for build identity does not apply.
type RevisionDiff struct {
	FromRevisionID string   `json:"from_revision_id"`
	ToRevisionID   string   `json:"to_revision_id"`
	Added          []string `json:"added"`
	Removed        []string `json:"removed"`
	Changed        []string `json:"changed"`
	Unchanged      int      `json:"unchanged"`
}

// Touched returns the documents whose content the wiki can no longer rely on.
//
// Added documents are excluded on purpose: nothing cites a document that did not
// exist, so no existing page is stale because of it. New material reaches the model
// as input to write new pages, not as a reason to rewrite old ones.
func (d RevisionDiff) Touched() []string {
	touched := make([]string, 0, len(d.Changed)+len(d.Removed))
	touched = append(touched, d.Changed...)
	touched = append(touched, d.Removed...)
	return touched
}

func (d RevisionDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// IncrementalPlan is what an incremental build decided to do, recorded so the
// decision can be audited after the fact. A compile that reused the wrong pages is
// invisible unless the plan is kept.
//
// Three lists rather than two, because "planned" and "happened" diverge and a record
// that conflates them is worse than none. A page can be scheduled for rewrite and
// still come back unchanged — the compiler may simply not return it — and reporting
// it as recompiled would credit the model with text it never produced.
type IncrementalPlan struct {
	ParentBuildID string       `json:"parent_build_id"`
	RevisionDiff  RevisionDiff `json:"revision_diff"`
	// ScheduledPaths is the impact closure: pages citing a touched document, plus one
	// hop of pages linking to those. Platform-generated pages are excluded — index and
	// log are regenerated on every build, so scheduling them would be meaningless.
	ScheduledPaths []string `json:"scheduled_paths"`
	// RecompiledPaths are the pages the compiler actually returned.
	RecompiledPaths []string `json:"recompiled_paths"`
	// ReusedPaths are the pages carried over from the parent unchanged.
	ReusedPaths []string `json:"reused_paths"`
	// DeletedPaths are the pages the compiler declared unsupported by their sources.
	DeletedPaths []string `json:"deleted_paths"`
	// RejectedPaths are pages the compiler tried to change or delete without them being
	// in the plan. They are kept separate rather than folded into RecompiledPaths: those
	// pages are still carried over untouched, so recording them as recompiled would put
	// the same path in two outcome lists and reintroduce exactly the contradiction this
	// split exists to remove.
	RejectedPaths []string `json:"rejected_paths"`
}

// ErrNoParentBuild means there is nothing to be incremental against. Reported rather
// than quietly compiling everything, because a caller that asked for incremental and
// silently got a full build believes it saved cost it did not save.
var ErrNoParentBuild = errors.New("no parent build to compile incrementally against")

// ErrIncompatibleParent means the parent build was produced by a different compiler
// identity — model, prompt, compiler version or profile. Such a build cannot be
// incrementally updated: the raw diff would be empty while every page still needs
// rewriting, and carrying pages forward would stamp this build's provenance onto text
// the recorded model never produced.
var ErrIncompatibleParent = errors.New("parent build has a different compiler identity")

// ErrBuildNotActivatable means a build cannot become the active wiki: it did not
// succeed, or it did not pass check. check is the only gate, so this is where
// that rule is enforced regardless of who asks.
var ErrBuildNotActivatable = errors.New("build is not activatable")

// ErrLeaseLost means another worker took over this build. The losing worker must
// discard its output rather than merge it: the new owner is producing a complete
// wiki, and interleaving two of them yields a graph neither of them checked.
var ErrLeaseLost = errors.New("build lease lost")

// QueueStats answers "why is my compile not done yet" without reading the build
// table by hand. Without it, queue wait is indistinguishable from a stuck worker.
type QueueStats struct {
	Queued          int `json:"queued"`
	Running         int `json:"running"`
	WaitingForRetry int `json:"waiting_for_retry"`
	// OldestQueuedSeconds is the age of the oldest waiting build, which is the
	// number that actually tells whether the queue is keeping up.
	OldestQueuedSeconds int64 `json:"oldest_queued_seconds"`
}

// EnqueueCompileResponse is returned by the asynchronous compile entry point.
//
// Compilation is queued rather than performed inline: a synchronous compile was
// measured at 200-400 seconds, past any sane HTTP client default, and a caller
// that gives up loses the work. Callers poll the build instead.
type EnqueueCompileResponse struct {
	Build *BuildRevision `json:"build"`
	// Reused is true when a succeeded build already matched the input identity, in
	// which case nothing was queued at all.
	Reused bool `json:"reused"`
	// Activated is only ever true on the reuse path, where there is already a
	// wiki to point at.
	Activated bool        `json:"activated"`
	Queue     *QueueStats `json:"queue,omitempty"`
	Warnings  []string    `json:"warnings,omitempty"`
}

// ─── K3.6: wiki retrieval ───

// wikiSearchOverFetch multiplies TopK before per-build filtering and per-page collapsing.
// Without it a single long page occupying the top chunks would crowd out every other
// answer, and hits from non-active builds would silently eat the result budget.
const wikiSearchOverFetch = 4

type IndexWikiRequest struct {
	// SourceID is optional; empty indexes every source that has an active wiki build.
	SourceID string `json:"source_id"`
}

type IndexedWikiBuild struct {
	SourceID string `json:"source_id"`
	Name     string `json:"name"`
	BuildID  string `json:"build_id"`
	Pages    int    `json:"pages"`
	// PagesSkipped counts pages deliberately left out of the index — the build log, which
	// is a transcript of the compile rather than knowledge about the domain.
	PagesSkipped    int   `json:"pages_skipped"`
	ChunksIndexed   int   `json:"chunks_indexed"`
	ChunksFailed    int   `json:"chunks_failed"`
	TruncatedChunks int   `json:"truncated_chunks"`
	StaleDeleted    int64 `json:"stale_deleted"`
}

type IndexWikiResponse struct {
	Indexed []IndexedWikiBuild    `json:"indexed"`
	Errors  []KnowledgeIndexError `json:"errors"`
}

type SearchWikiRequest struct {
	Query     string   `json:"query"`
	TopK      int      `json:"top_k"`
	Domain    string   `json:"domain"`
	SourceIDs []string `json:"source_ids"`
	// IncludeContent returns full page bodies. Off by default: a wiki page is long
	// enough that returning several of them in full defeats the point of retrieving.
	IncludeContent bool `json:"include_content"`
}

// WikiSearchHit is one page, not one chunk.
//
// Citations travel with the hit because they are the second level of the query: the page
// is generated text, so a claim is only checkable by following it to the document it came
// from. A hit without them would be a plausible paragraph with no way to verify it, which
// is exactly the failure a generated wiki invites.
type WikiSearchHit struct {
	PageID        string  `json:"page_id"`
	SourceID      string  `json:"source_id"`
	BuildID       string  `json:"build_id"`
	KnowledgeBase string  `json:"knowledge_base,omitempty"`
	Path          string  `json:"path"`
	Kind          string  `json:"kind"`
	Title         string  `json:"title,omitempty"`
	HeadingPath   string  `json:"heading_path,omitempty"`
	Snippet       string  `json:"snippet,omitempty"`
	Content       string  `json:"content,omitempty"`
	Score         float64 `json:"score"`
	// MatchedChunks is how many chunks of this page matched. Several matches make a page
	// more relevant, but reporting it several times would make one page look like several
	// answers.
	MatchedChunks int `json:"matched_chunks"`
	// DerivedFromBuildID is set when the page was carried over unchanged by an incremental
	// build, so a reader can tell which model run actually wrote the text.
	DerivedFromBuildID string         `json:"derived_from_build_id,omitempty"`
	Citations          []PageCitation `json:"citations,omitempty"`
	Links              []PageLink     `json:"links,omitempty"`
}

type SearchWikiResponse struct {
	Query string          `json:"query"`
	TopK  int             `json:"top_k"`
	Items []WikiSearchHit `json:"items"`
	// Note explains an empty result that is a normal state rather than a fault — most
	// often that nothing has been compiled yet.
	Note string `json:"note,omitempty"`
}

// WikiIndexStatus reports the gap between what is active and what is searchable.
//
// The two are allowed to differ: indexing costs embedding round trips and cannot run
// inside a pointer move. What must not happen is the difference being invisible, so it is
// reported rather than assumed away.
type WikiIndexStatus struct {
	ID             string  `json:"source_id"`
	Name           string  `json:"name"`
	ActiveBuildID  *string `json:"active_build_id,omitempty"`
	IndexedBuildID *string `json:"indexed_build_id,omitempty"`
	// Stale is true when an active wiki exists that the index does not cover. Search
	// filters on the active build, so a stale index yields fewer hits rather than pages
	// from a build that is no longer current.
	Stale         bool   `json:"stale"`
	WikiIndexedAt string `json:"wiki_indexed_at,omitempty"`
}
