package knowledge

import "time"

// ReviewFinding is one claim on one page that its sources do not support.
//
// It carries no severity. The kind already says what is wrong, and a model asked to also
// rate seriousness would be calibrating a scale it has no basis for — an "overstated"
// finding that says "low" tells the reader nothing the kind did not.
type ReviewFinding struct {
	ID       string `json:"id,omitempty"`
	BuildID  string `json:"build_id,omitempty"`
	PagePath string `json:"page_path"`
	Kind     string `json:"kind"`
	// Claim is the reviewer's quotation of the page, so the finding can be read and acted
	// on without opening the page. It is not verified to be a byte-exact substring:
	// dropping a real finding because the reviewer normalised whitespace or markdown would
	// lose signal to win a formality.
	Claim     string    `json:"claim"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// ReviewResponse pairs the verdict with the findings behind it.
//
// The whole build is returned, not just the status, because the status alone is
// misleading: review_pages_examined against review_pages_total is what says whether
// "clean" covers the wiki or a fifth of it.
type ReviewResponse struct {
	Build    BuildRevision   `json:"build"`
	Findings []ReviewFinding `json:"findings"`
}

type recordReviewInput struct {
	BuildID string
	Status  string
	Note    string
	// Provenance of the reviewer that actually produced these findings. Empty leaves the
	// stored values alone, which is what a skipped review should do.
	ReviewerModel         string
	ReviewerPromptVersion string
	ReviewerIndependence  string
	PagesExamined         int
	PagesTotal            int
	Tokens                int
	CostMicros            int64
	Findings              []ReviewFinding
}
