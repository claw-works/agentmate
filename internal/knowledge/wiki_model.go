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
	BuildEventCheckFailed  = "check_failed"
	BuildEventCheckPassed  = "check_passed"
	BuildEventActivated    = "activated"
	BuildEventFailed       = "failed"
	BuildEventFinished     = "finished"
)

// ─── requests and responses ───

type CompileRequest struct {
	SourceID string `json:"source_id"`
	// Mode is full or incremental. Incremental compilation lands in K3.4; until
	// then a request for it is rejected rather than silently downgraded, so a
	// caller does not believe it saved cost it did not save.
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
