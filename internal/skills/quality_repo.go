package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const qualityRunColumns = `id, account_id, skill_version_id, baseline_version_id, engine_version, checkset_version, input_package_hash, baseline_package_hash, telemetry_cutoff, status, report, failure_message, created_at, completed_at`
const qualityRunSummaryColumns = `id, skill_version_id, baseline_version_id, engine_version, checkset_version, input_package_hash, baseline_package_hash, telemetry_cutoff, status, failure_message, created_at, completed_at`

func (r *Repo) LoadQualityEngineInput(ctx context.Context, accountID, versionID, baselineVersionID string) (QualityEngineInput, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return QualityEngineInput{}, err
	}
	defer tx.Rollback(ctx)

	var cutoff time.Time
	if err := tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&cutoff); err != nil {
		return QualityEngineInput{}, err
	}
	version, err := getQualityVersionTx(ctx, tx, accountID, versionID)
	if err != nil {
		return QualityEngineInput{}, err
	}
	current, err := loadQualityPackageTx(ctx, tx, accountID, version)
	if err != nil {
		return QualityEngineInput{}, err
	}

	var baseline *QualityPackage
	var baselineVersion SkillVersion
	baselineFound := false
	if baselineVersionID != "" {
		baselineVersion, err = getQualityVersionTx(ctx, tx, accountID, baselineVersionID)
		if err != nil {
			return QualityEngineInput{}, err
		}
		baselineFound = true
	} else {
		err = tx.QueryRow(ctx,
			`SELECT `+skillVersionColumns+`
			 FROM skill_versions
			 WHERE account_id = $1
			   AND skill_name = $2
			   AND (published_at, id) < ($3, $4)
			 ORDER BY published_at DESC, id DESC
			 LIMIT 1`,
			accountID, version.SkillName, version.PublishedAt, version.ID,
		).Scan(scanVersion(&baselineVersion)...)
		if err == nil {
			baselineFound = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return QualityEngineInput{}, err
		}
	}
	if baselineFound {
		loaded, loadErr := loadQualityPackageTx(ctx, tx, accountID, baselineVersion)
		if loadErr != nil {
			return QualityEngineInput{}, loadErr
		}
		baseline = &loaded
	}

	logs, err := listVersionTelemetryTx(ctx, tx, accountID, version.ID, cutoff, maxTelemetryLogs)
	if err != nil {
		return QualityEngineInput{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return QualityEngineInput{}, err
	}
	return QualityEngineInput{Package: current, Baseline: baseline, TelemetryLogs: logs, TelemetryCutoff: cutoff.UTC()}, nil
}

func getQualityVersionTx(ctx context.Context, tx pgx.Tx, accountID, versionID string) (SkillVersion, error) {
	var version SkillVersion
	err := tx.QueryRow(ctx,
		`SELECT `+skillVersionColumns+` FROM skill_versions WHERE account_id = $1 AND id = $2`,
		accountID, versionID,
	).Scan(scanVersion(&version)...)
	return version, err
}

func loadQualityPackageTx(ctx context.Context, tx pgx.Tx, accountID string, version SkillVersion) (QualityPackage, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM skill_version_files
		 WHERE account_id = $1 AND version_id = $2
		 ORDER BY path`,
		accountID, version.ID,
	)
	if err != nil {
		return QualityPackage{}, err
	}
	files := make([]SkillVersionFile, 0)
	for rows.Next() {
		var file SkillVersionFile
		if err := rows.Scan(scanVersionFile(&file)...); err != nil {
			rows.Close()
			return QualityPackage{}, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return QualityPackage{}, err
	}
	rows.Close()

	artifact, err := getQualityCompiledCatalogTx(ctx, tx, accountID, version.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		artifact = nil
	} else if err != nil {
		return QualityPackage{}, err
	}
	return QualityPackage{Version: version, Files: files, Artifact: artifact}, nil
}

func getQualityCompiledCatalogTx(ctx context.Context, tx pgx.Tx, accountID, versionID string) (*CompiledSkillCatalog, error) {
	var artifact CompiledSkillCatalog
	var rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest []byte
	err := tx.QueryRow(ctx,
		`SELECT catalog.id, catalog.account_id, catalog.skill_version_id, catalog.skill_name,
		        version.version, version.source_id, catalog.compiler_name, catalog.compiler_version, catalog.input_package_hash,
		        catalog.description, catalog.triggers, catalog.capabilities, catalog.constraints,
		        catalog.dependencies, catalog.resource_manifest, catalog.compiled_at, version.published_at
		 FROM skill_compiled_catalogs AS catalog
		 JOIN skill_versions AS version
		   ON version.id = catalog.skill_version_id AND version.account_id = catalog.account_id
		 WHERE catalog.account_id = $1 AND catalog.skill_version_id = $2`,
		accountID, versionID,
	).Scan(
		&artifact.ID, &artifact.AccountID, &artifact.SkillVersionID, &artifact.SkillName, &artifact.Version, &artifact.SourceID,
		&artifact.CompilerName, &artifact.CompilerVersion, &artifact.InputPackageHash, &artifact.Description,
		&rawTriggers, &rawCapabilities, &rawConstraints, &rawDependencies, &rawManifest,
		&artifact.CompiledAt, &artifact.PublishedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := decodeCompiledJSON(&artifact, rawTriggers, rawCapabilities, rawConstraints, rawDependencies, rawManifest); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func listVersionTelemetryTx(ctx context.Context, tx pgx.Tx, accountID, versionID string, cutoff time.Time, limit int) ([]SkillLog, error) {
	if limit <= 0 || limit > maxTelemetryLogs {
		limit = maxTelemetryLogs
	}
	rows, err := tx.Query(ctx,
		`SELECT id, skill_version_id, was_triggered, outcome, created_at
		 FROM skill_logs
		 WHERE account_id = $1 AND skill_version_id = $2 AND created_at <= $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		accountID, versionID, cutoff, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]SkillLog, 0)
	for rows.Next() {
		var logEntry SkillLog
		if err := rows.Scan(&logEntry.ID, &logEntry.SkillVersionID, &logEntry.WasTriggered, &logEntry.Outcome, &logEntry.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, logEntry)
	}
	return logs, rows.Err()
}

func (r *Repo) CreateQualityRun(ctx context.Context, run QualityRun) (*QualityRun, error) {
	reportJSON, err := json.Marshal(run.Report)
	if err != nil {
		return nil, fmt.Errorf("marshal quality report: %w", err)
	}
	if len(reportJSON) > maxQualityReportBytes {
		return nil, fmt.Errorf("quality report exceeds %d bytes", maxQualityReportBytes)
	}
	var stored QualityRun
	var rawReport []byte
	err = r.pool.QueryRow(ctx,
		`INSERT INTO skill_quality_runs
		 (account_id, skill_version_id, baseline_version_id, engine_version, checkset_version,
		  input_package_hash, baseline_package_hash, telemetry_cutoff, status, report, failure_message, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12)
		 RETURNING `+qualityRunColumns,
		run.AccountID, run.SkillVersionID, run.BaselineVersionID, run.EngineVersion, run.ChecksetVersion,
		run.InputPackageHash, run.BaselinePackageHash, run.TelemetryCutoff, run.Status, reportJSON,
		run.FailureMessage, run.CompletedAt,
	).Scan(scanQualityRun(&stored, &rawReport)...)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawReport, &stored.Report); err != nil {
		return nil, fmt.Errorf("decode quality report: %w", err)
	}
	normalizeQualityReport(&stored.Report)
	return &stored, nil
}

func (r *Repo) GetQualityRun(ctx context.Context, accountID, runID string) (*QualityRun, error) {
	var run QualityRun
	var rawReport []byte
	err := r.pool.QueryRow(ctx,
		`SELECT `+qualityRunColumns+` FROM skill_quality_runs WHERE account_id = $1 AND id = $2`,
		accountID, runID,
	).Scan(scanQualityRun(&run, &rawReport)...)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawReport, &run.Report); err != nil {
		return nil, fmt.Errorf("decode quality report: %w", err)
	}
	normalizeQualityReport(&run.Report)
	return &run, nil
}

func (r *Repo) ListQualityRuns(ctx context.Context, accountID, versionID string, params QualityRunListParams) ([]QualityRunSummary, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+qualityRunSummaryColumns+`
		 FROM skill_quality_runs
		 WHERE account_id = $1 AND skill_version_id = $2
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3 OFFSET $4`,
		accountID, versionID, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]QualityRunSummary, 0)
	for rows.Next() {
		var run QualityRunSummary
		if err := rows.Scan(scanQualityRunSummary(&run)...); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *Repo) CountQualityRuns(ctx context.Context, accountID, versionID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM skill_quality_runs WHERE account_id = $1 AND skill_version_id = $2`,
		accountID, versionID,
	).Scan(&count)
	return count, err
}

func scanQualityRun(run *QualityRun, rawReport *[]byte) []any {
	return []any{
		&run.ID,
		&run.AccountID,
		&run.SkillVersionID,
		&run.BaselineVersionID,
		&run.EngineVersion,
		&run.ChecksetVersion,
		&run.InputPackageHash,
		&run.BaselinePackageHash,
		&run.TelemetryCutoff,
		&run.Status,
		rawReport,
		&run.FailureMessage,
		&run.CreatedAt,
		&run.CompletedAt,
	}
}

func scanQualityRunSummary(run *QualityRunSummary) []any {
	return []any{
		&run.ID,
		&run.SkillVersionID,
		&run.BaselineVersionID,
		&run.EngineVersion,
		&run.ChecksetVersion,
		&run.InputPackageHash,
		&run.BaselinePackageHash,
		&run.TelemetryCutoff,
		&run.Status,
		&run.FailureMessage,
		&run.CreatedAt,
		&run.CompletedAt,
	}
}
