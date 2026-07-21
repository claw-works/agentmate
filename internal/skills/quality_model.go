package skills

import "time"

const (
	QualityEngineVersion   = "agentmate-static-quality/1.1.0"
	QualityChecksetVersion = "phase4-contracts/1.1.0"
	maxQualityReportBytes  = 1024 * 1024
	maxTelemetryLogs       = 200
	minTelemetryTriggered  = 20
)

type CreateQualityRunRequest struct {
	BaselineVersionID string `json:"baseline_version_id"`
}

type QualityRun struct {
	ID                  string        `json:"id"`
	AccountID           string        `json:"-"`
	SkillVersionID      string        `json:"skill_version_id"`
	BaselineVersionID   *string       `json:"baseline_version_id,omitempty"`
	EngineVersion       string        `json:"engine_version"`
	ChecksetVersion     string        `json:"checkset_version"`
	InputPackageHash    string        `json:"input_package_hash"`
	BaselinePackageHash *string       `json:"baseline_package_hash,omitempty"`
	TelemetryCutoff     time.Time     `json:"telemetry_cutoff"`
	Status              string        `json:"status"`
	Report              QualityReport `json:"report"`
	FailureMessage      string        `json:"failure_message,omitempty"`
	CreatedAt           time.Time     `json:"created_at"`
	CompletedAt         *time.Time    `json:"completed_at,omitempty"`
}

type QualityRunSummary struct {
	ID                  string     `json:"id"`
	SkillVersionID      string     `json:"skill_version_id"`
	BaselineVersionID   *string    `json:"baseline_version_id,omitempty"`
	EngineVersion       string     `json:"engine_version"`
	ChecksetVersion     string     `json:"checkset_version"`
	InputPackageHash    string     `json:"input_package_hash"`
	BaselinePackageHash *string    `json:"baseline_package_hash,omitempty"`
	TelemetryCutoff     time.Time  `json:"telemetry_cutoff"`
	Status              string     `json:"status"`
	FailureMessage      string     `json:"failure_message,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

type QualityRunListParams struct {
	Limit  int
	Offset int
}

type QualityRunListResponse struct {
	Items  []QualityRunSummary `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type QualityReport struct {
	SchemaVersion string            `json:"schema_version"`
	EngineVersion string            `json:"engine_version"`
	Checkset      string            `json:"checkset_version"`
	Input         QualityPackageRef `json:"input"`
	Lint          []QualityCheck    `json:"lint"`
	Eval          []QualityCheck    `json:"eval"`
	Comparison    QualityComparison `json:"comparison"`
	Telemetry     QualityTelemetry  `json:"telemetry"`
}

type QualityPackageRef struct {
	VersionID   string `json:"version_id"`
	SkillName   string `json:"skill_name"`
	Version     string `json:"version"`
	PackageHash string `json:"package_hash"`
}

type QualitySeverity string

const (
	QualitySeverityBlocker QualitySeverity = "blocker"
	QualitySeverityError   QualitySeverity = "error"
	QualitySeverityWarning QualitySeverity = "warning"
)

type QualityCheck struct {
	ID         string          `json:"id"`
	Severity   QualitySeverity `json:"severity"`
	Passed     bool            `json:"passed"`
	Applicable bool            `json:"applicable"`
	Evidence   map[string]any  `json:"evidence"`
}

type QualityFileChange struct {
	Path       string `json:"path"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
}

type QualityRoutingDiff struct {
	Field  string   `json:"field"`
	Before []string `json:"before"`
	After  []string `json:"after"`
}

type QualityComparison struct {
	Status                  string               `json:"status"`
	BaselineVersionID       *string              `json:"baseline_version_id,omitempty"`
	PackageHashChanged      bool                 `json:"package_hash_changed"`
	ResourceManifestChanged bool                 `json:"resource_manifest_changed"`
	FilesAdded              []QualityFileChange  `json:"files_added"`
	FilesRemoved            []QualityFileChange  `json:"files_removed"`
	FilesModified           []QualityFileChange  `json:"files_modified"`
	RoutingDiffs            []QualityRoutingDiff `json:"routing_diffs"`
	LintRegressions         []string             `json:"lint_regressions"`
	EvalRegressions         []string             `json:"eval_regressions"`
}

type QualityOutcomeCounts struct {
	Success       int `json:"success"`
	Failure       int `json:"failure"`
	Partial       int `json:"partial"`
	UserCorrected int `json:"user_corrected"`
	Other         int `json:"other"`
}

type QualitySuggestion struct {
	Category    string   `json:"category"`
	Count       int      `json:"count"`
	Denominator int      `json:"denominator"`
	Rate        float64  `json:"rate"`
	Fingerprint string   `json:"fingerprint"`
	LogIDs      []string `json:"log_ids"`
}

type QualityTelemetry struct {
	Status             string               `json:"status"`
	Cutoff             time.Time            `json:"cutoff"`
	Considered         int                  `json:"considered"`
	Triggered          int                  `json:"triggered"`
	Bypass             int                  `json:"bypass"`
	OutcomeDenominator int                  `json:"outcome_denominator"`
	Outcomes           QualityOutcomeCounts `json:"outcomes"`
	Suggestions        []QualitySuggestion  `json:"suggestions"`
}

type QualityPackage struct {
	Version  SkillVersion
	Files    []SkillVersionFile
	Artifact *CompiledSkillCatalog
}

type QualityEngineInput struct {
	Package         QualityPackage
	Baseline        *QualityPackage
	TelemetryLogs   []SkillLog
	TelemetryCutoff time.Time
}
