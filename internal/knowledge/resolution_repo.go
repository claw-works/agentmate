package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/jackc/pgx/v5"
)

const resolutionRunColumns = `id, account_id, skill_version_id, session_id, requirement_id,
	contract_identity, discovery_fingerprint, discovery_status,
	candidates, selected, retrieved, citations,
	selection_reason, confidence, idempotency_key, content_hash, created_at`

// InsertResolutionRun appends one run. With an idempotency key, a replay returns the
// existing row and created=false; the caller compares content hashes and decides whether
// the replay agrees with the original. Without a key every call appends.
func (r *Repo) InsertResolutionRun(ctx context.Context, owner ownership.Owner, req RecordResolutionRequest, contractIdentity, contentHash string) (*KnowledgeResolutionRun, bool, error) {
	candidates, err := json.Marshal(nonNilCandidates(req.Candidates))
	if err != nil {
		return nil, false, err
	}
	selected, err := json.Marshal(nonNilSelected(req.Selected))
	if err != nil {
		return nil, false, err
	}
	retrieved, err := json.Marshal(nonNilRetrieved(req.Retrieved))
	if err != nil {
		return nil, false, err
	}
	citations, err := json.Marshal(nonNilCitations(req.Citations))
	if err != nil {
		return nil, false, err
	}

	// INSERT ... SELECT against skill_versions makes version ownership part of the write
	// itself (the compiled-catalog pattern): a version outside the account inserts zero
	// rows rather than relying on a separate probe that could race a deletion.
	var run KnowledgeResolutionRun
	err = r.pool.QueryRow(ctx,
		`INSERT INTO knowledge_resolution_runs
		 (account_id, user_id, key_id, skill_version_id, session_id, requirement_id,
		  contract_identity, discovery_fingerprint, discovery_status,
		  candidates, selected, retrieved, citations,
		  selection_reason, confidence, idempotency_key, content_hash)
		 SELECT $1, $2, $3, version.id, $5, $6, $7, $8, $9,
		        $10::jsonb, $11::jsonb, $12::jsonb, $13::jsonb, $14, $15, $16, $17
		 FROM skill_versions AS version
		 WHERE version.account_id = $1 AND version.id = $4
		 ON CONFLICT (account_id, idempotency_key) WHERE idempotency_key IS NOT NULL
		 DO NOTHING
		 RETURNING `+resolutionRunColumns,
		owner.Account(), owner.UserID, owner.KeyID, req.SkillVersionID,
		nullableString(req.SessionID), req.RequirementID,
		contractIdentity, req.DiscoveryFingerprint, req.DiscoveryStatus,
		candidates, selected, retrieved, citations,
		req.SelectionReason, req.Confidence, nullableString(req.IdempotencyKey), contentHash,
	).Scan(scanResolutionRun(&run)...)
	if err == nil {
		return &run, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	// Zero rows means either the idempotency conflict fired or the version does not
	// belong to the account. Without a key only the second is possible.
	if req.IdempotencyKey == "" {
		return nil, false, fmt.Errorf("skill version not found: %s", req.SkillVersionID)
	}
	err = r.pool.QueryRow(ctx,
		`SELECT `+resolutionRunColumns+`
		 FROM knowledge_resolution_runs
		 WHERE account_id = $1 AND idempotency_key = $2`,
		owner.Account(), req.IdempotencyKey,
	).Scan(scanResolutionRun(&run)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("skill version not found: %s", req.SkillVersionID)
	}
	if err != nil {
		return nil, false, err
	}
	return &run, false, nil
}

func (r *Repo) GetResolutionRun(ctx context.Context, accountID, runID string) (*KnowledgeResolutionRun, error) {
	var run KnowledgeResolutionRun
	err := r.pool.QueryRow(ctx,
		`SELECT `+resolutionRunColumns+`
		 FROM knowledge_resolution_runs
		 WHERE account_id = $1 AND id::text = $2`,
		accountID, runID,
	).Scan(scanResolutionRun(&run)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("resolution run not found")
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

const resolutionListFilterClause = `
	   AND ($2 = '' OR skill_version_id::text = $2)
	   AND ($3 = '' OR session_id::text = $3)
	   AND ($4 = '' OR selected @> jsonb_build_array(jsonb_build_object('source_id', $4::text)))`

func (r *Repo) ListResolutionRuns(ctx context.Context, accountID string, params ResolutionListParams) ([]KnowledgeResolutionRunSummary, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*)
		 FROM knowledge_resolution_runs
		 WHERE account_id = $1`+resolutionListFilterClause,
		accountID, params.SkillVersionID, params.SessionID, params.SourceID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, skill_version_id, COALESCE(session_id::text, ''), requirement_id,
		        discovery_fingerprint, discovery_status,
		        jsonb_array_length(selected), jsonb_array_length(retrieved), jsonb_array_length(citations),
		        created_at
		 FROM knowledge_resolution_runs
		 WHERE account_id = $1`+resolutionListFilterClause+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT $5 OFFSET $6`,
		accountID, params.SkillVersionID, params.SessionID, params.SourceID, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]KnowledgeResolutionRunSummary, 0)
	for rows.Next() {
		var item KnowledgeResolutionRunSummary
		if err := rows.Scan(
			&item.ID, &item.SkillVersionID, &item.SessionID, &item.RequirementID,
			&item.DiscoveryFingerprint, &item.DiscoveryStatus,
			&item.SelectedCount, &item.RetrievedCount, &item.CitationCount,
			&item.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func scanResolutionRun(run *KnowledgeResolutionRun) []any {
	return []any{
		&run.ID, &run.AccountID, &run.SkillVersionID,
		&nullableStringScanner{value: &run.SessionID}, &run.RequirementID,
		&run.ContractIdentity, &run.DiscoveryFingerprint, &run.DiscoveryStatus,
		&jsonScanner{value: &run.Candidates}, &jsonScanner{value: &run.Selected},
		&jsonScanner{value: &run.Retrieved}, &jsonScanner{value: &run.Citations},
		&run.SelectionReason, &run.Confidence,
		&nullableStringScanner{value: &run.IdempotencyKey}, &run.ContentHash, &run.CreatedAt,
	}
}

// nullableStringScanner maps SQL NULL to "" for optional text/uuid columns.
type nullableStringScanner struct{ value *string }

func (s *nullableStringScanner) Scan(src any) error {
	switch typed := src.(type) {
	case nil:
		*s.value = ""
	case string:
		*s.value = typed
	case []byte:
		*s.value = string(typed)
	default:
		return fmt.Errorf("cannot scan %T into string", src)
	}
	return nil
}

// jsonScanner decodes a JSONB column into its typed slice.
type jsonScanner struct{ value any }

func (s *jsonScanner) Scan(src any) error {
	var raw []byte
	switch typed := src.(type) {
	case nil:
		return nil
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return fmt.Errorf("cannot scan %T into json", src)
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, s.value)
}

func nonNilCandidates(values []ResolutionCandidateSummary) []ResolutionCandidateSummary {
	if values == nil {
		return []ResolutionCandidateSummary{}
	}
	return values
}

func nonNilSelected(values []ResolutionSelectedBase) []ResolutionSelectedBase {
	if values == nil {
		return []ResolutionSelectedBase{}
	}
	return values
}

func nonNilRetrieved(values []ResolutionRetrievedRef) []ResolutionRetrievedRef {
	if values == nil {
		return []ResolutionRetrievedRef{}
	}
	return values
}

func nonNilCitations(values []ResolutionCitation) []ResolutionCitation {
	if values == nil {
		return []ResolutionCitation{}
	}
	return values
}
