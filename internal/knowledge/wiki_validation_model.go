package knowledge

import "time"

// ValidationSignal is one piece of evidence about whether the wiki worked.
type ValidationSignal struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	// BuildID is the wiki that was serving when this happened, not the current one. A
	// complaint about a recompiled wiki is not a complaint about its replacement.
	BuildID  *string `json:"build_id,omitempty"`
	PagePath string  `json:"page_path,omitempty"`
	QueryID  *string `json:"query_id,omitempty"`

	Signal    string `json:"signal"`
	Direction string `json:"direction"`
	// Origin separates what a caller chose to tell us from what the platform computed.
	// Reported signals carry reporting bias — silence is indistinguishable from success —
	// and mixing them into one number without saying which is which hides that.
	Origin string `json:"origin"`

	Cause string `json:"cause"`
	// AttributionBasis names the rule that produced Cause. An attribution nobody can audit
	// is the same dead end as no attribution.
	AttributionBasis string    `json:"attribution_basis,omitempty"`
	Detail           string    `json:"detail,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type RecordSignalRequest struct {
	SourceID string `json:"source_id"`
	// BuildID is optional; the source's active build is used when omitted. Passing it
	// explicitly is what lets a late report land on the wiki it was actually about.
	BuildID  string `json:"build_id,omitempty"`
	PagePath string `json:"page_path,omitempty"`
	QueryID  string `json:"query_id,omitempty"`
	Signal   string `json:"signal"`
	Detail   string `json:"detail,omitempty"`
}

type SignalFilter struct {
	SourceID  string
	PagePath  string
	Direction string
	Cause     string
}

type SignalListResponse struct {
	Items  []ValidationSignal `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

type SignalCount struct {
	Key      string `json:"key"`
	Positive int    `json:"positive"`
	Negative int    `json:"negative"`
}

// SignalSummaryResponse reports counts and no verdict.
//
// §7.3 names sparsity as a built-in defect: a small knowledge base will never produce enough
// signal to be sure of anything. A summary that concluded "this wiki is fine" from four
// positives would be inventing confidence, so the shape of this response deliberately offers
// no place to put one.
type SignalSummaryResponse struct {
	SourceID string `json:"source_id"`
	Total    int    `json:"total"`
	Positive int    `json:"positive"`
	Negative int    `json:"negative"`
	// Reported and Derived split the totals by origin, so a reader can see how much of the
	// picture depends on callers choosing to speak.
	Reported int `json:"reported"`
	Derived  int `json:"derived"`

	ByPage   []SignalCount `json:"by_page"`
	ByCause  []SignalCount `json:"by_cause"`
	BySignal []SignalCount `json:"by_signal"`
}

type SignalSweepResponse struct {
	Recorded int `json:"recorded"`
	// AlreadyRecorded is not an error. The sweep is idempotent per day, and a caller running
	// it hourly needs to see that the second run added nothing rather than assume it failed.
	AlreadyRecorded int `json:"already_recorded"`
	IdleDays        int `json:"idle_days"`
}

type insertSignalInput struct {
	SourceID         string
	BuildID          string
	PagePath         string
	QueryID          string
	Signal           string
	Direction        string
	Origin           string
	Cause            string
	AttributionBasis string
	Detail           string
}

const validationSignalColumns = `id, source_id, build_id, page_path, query_id,
	signal, direction, origin, cause, attribution_basis, detail, created_at`

func scanValidationSignal(signal *ValidationSignal) []any {
	return []any{
		&signal.ID, &signal.SourceID, &signal.BuildID, &signal.PagePath, &signal.QueryID,
		&signal.Signal, &signal.Direction, &signal.Origin, &signal.Cause,
		&signal.AttributionBasis, &signal.Detail, &signal.CreatedAt,
	}
}
