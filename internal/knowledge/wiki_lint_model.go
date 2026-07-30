package knowledge

import "time"

// LintFinding is one thing worth a human's attention in a wiki that is already serving.
//
// It carries no status field. A finding is an observation, not a ticket: lint is stateless
// between runs, and a finding that matters is one that reappears on the next run. Letting
// findings be resolved in place would make a stale acknowledgement look like a clean wiki.
type LintFinding struct {
	ID       string `json:"id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	PagePath string `json:"page_path,omitempty"`
	// RelatedPath is the other end of the relation the finding is about: the link target,
	// the superseding page, the document nothing cites. Graph findings are about pairs.
	RelatedPath string    `json:"related_path,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

// LintRun is one execution of every rule against one build.
//
// BuildID and RevisionID are both stored because staleness is a relation between them:
// the same build linted before and after a sync yields different findings, and a run that
// records only the build cannot explain why.
type LintRun struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	BuildID    string `json:"build_id"`
	RevisionID string `json:"revision_id"`

	PagesExamined   int `json:"pages_examined"`
	FindingsTotal   int `json:"findings_total"`
	FindingsWarning int `json:"findings_warning"`
	FindingsInfo    int `json:"findings_info"`

	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type LintRunResponse struct {
	Run      LintRun       `json:"run"`
	Findings []LintFinding `json:"findings"`
}

type LintRunListResponse struct {
	Items  []LintRun `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

type recordLintRunInput struct {
	SourceID   string
	BuildID    string
	RevisionID string
	// StartedAt is captured before the rules run rather than at insert time. The duration
	// is the evidence for architecture §14's claim that recursive CTEs are enough for KB
	// lint; a run that reports no duration cannot support or refute it.
	StartedAt     time.Time
	PagesExamined int
	Findings      []LintFinding
}

const lintRunColumns = `id, source_id, build_id, revision_id, pages_examined,
	findings_total, findings_warning, findings_info, started_at, finished_at, created_at`

func scanLintRun(run *LintRun) []any {
	return []any{
		&run.ID, &run.SourceID, &run.BuildID, &run.RevisionID, &run.PagesExamined,
		&run.FindingsTotal, &run.FindingsWarning, &run.FindingsInfo,
		&run.StartedAt, &run.FinishedAt, &run.CreatedAt,
	}
}
