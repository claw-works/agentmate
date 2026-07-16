package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/ownership"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) RecordEvent(ctx context.Context, owner ownership.Owner, req RecordEventRequest, contentHash string, occurredAt time.Time) (*Event, bool, error) {
	payload, err := marshalJSON(req.Payload)
	if err != nil {
		return nil, false, err
	}

	var event Event
	err = r.pool.QueryRow(ctx,
		`INSERT INTO memory_events
		 (account_id, user_id, key_id, scope_type, scope_key, session_id, sequence_no, event_type,
		  payload, source_type, source_id, occurred_at, idempotency_key, content_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (account_id, idempotency_key) DO NOTHING
		 RETURNING id, account_id, user_id, key_id, scope_type, scope_key, session_id, sequence_no,
		   event_type, payload, source_type, source_id, occurred_at, idempotency_key, content_hash, created_at`,
		owner.Account(), owner.UserID, owner.KeyID, req.ScopeType, req.ScopeKey, req.SessionID, req.SequenceNo,
		req.EventType, payload, req.SourceType, req.SourceID, occurredAt, req.IdempotencyKey, contentHash,
	).Scan(scanEvent(&event)...)
	if err == nil {
		return &event, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	err = r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, scope_type, scope_key, session_id, sequence_no,
		   event_type, payload, source_type, source_id, occurred_at, idempotency_key, content_hash, created_at
		 FROM memory_events
		 WHERE account_id = $1 AND idempotency_key = $2`,
		owner.Account(), req.IdempotencyKey,
	).Scan(scanEvent(&event)...)
	if err != nil {
		return nil, false, err
	}
	return &event, false, nil
}

func (r *Repo) CreateEntry(ctx context.Context, owner ownership.Owner, in createEntryInput) (*EntryDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	metadata, err := marshalJSON(in.Metadata)
	if err != nil {
		return nil, err
	}

	var entry Entry
	err = tx.QueryRow(ctx,
		`INSERT INTO memory_entries
		 (account_id, user_id, key_id, scope_type, scope_key, memory_type, title, content, summary,
		  content_hash, confidence, importance, status, metadata, ttl_at, valid_from, valid_to,
		  source_event_id, extraction_method, extractor_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		 RETURNING `+entryColumns,
		owner.Account(), owner.UserID, owner.KeyID, in.ScopeType, in.ScopeKey, in.MemoryType, in.Title,
		in.Content, in.Summary, in.ContentHash, in.ConfidenceValue, in.ImportanceValue, in.Status, metadata,
		in.TTLAt, in.ValidFromValue, in.ValidTo, in.SourceEventID, in.ExtractionMethod, in.ExtractorVersion,
	).Scan(scanEntry(&entry)...)
	if err != nil {
		return nil, err
	}

	evidence := make([]Evidence, 0, len(in.Evidence))
	for _, item := range in.Evidence {
		created, err := insertEvidence(ctx, tx, owner, entry.ID, item)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, *created)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &EntryDetail{Entry: entry, Evidence: evidence}, nil
}

func (r *Repo) GetEntry(ctx context.Context, accountID, id string) (*EntryDetail, error) {
	var entry Entry
	err := r.pool.QueryRow(ctx,
		`SELECT `+entryColumns+`
		 FROM memory_entries
		 WHERE account_id = $1 AND id = $2`,
		accountID, id,
	).Scan(scanEntry(&entry)...)
	if err != nil {
		return nil, err
	}

	evidence, err := r.ListEvidence(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	return &EntryDetail{Entry: entry, Evidence: evidence}, nil
}

func (r *Repo) ListEvidence(ctx context.Context, accountID, memoryID string) ([]Evidence, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, memory_id, source_type, source_id, excerpt, metadata, created_at
		 FROM memory_evidence
		 WHERE account_id = $1 AND memory_id = $2
		 ORDER BY created_at, id`,
		accountID, memoryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Evidence, 0)
	for rows.Next() {
		var item Evidence
		if err := rows.Scan(
			&item.ID, &item.AccountID, &item.UserID, &item.KeyID, &item.MemoryID,
			&item.SourceType, &item.SourceID, &item.Excerpt, &item.Metadata, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repo) CountEntries(ctx context.Context, accountID string, params ListEntriesParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*)
		 FROM memory_entries
		 WHERE account_id = $1
		   AND ($2 = '' OR scope_type = $2)
		   AND ($3 = '' OR scope_key = $3)
		   AND ($4 = '' OR memory_type = $4)
		   AND ($5 = '' OR status = $5)`,
		accountID, params.ScopeType, params.ScopeKey, params.MemoryType, params.Status,
	).Scan(&count)
	return count, err
}

func (r *Repo) ListEntries(ctx context.Context, accountID string, params ListEntriesParams) ([]Entry, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+entryColumns+`
		 FROM memory_entries
		 WHERE account_id = $1
		   AND ($2 = '' OR scope_type = $2)
		   AND ($3 = '' OR scope_key = $3)
		   AND ($4 = '' OR memory_type = $4)
		   AND ($5 = '' OR status = $5)
		 ORDER BY importance DESC, updated_at DESC
		 LIMIT $6 OFFSET $7`,
		accountID, params.ScopeType, params.ScopeKey, params.MemoryType, params.Status, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Entry, 0)
	for rows.Next() {
		var item Entry
		if err := rows.Scan(scanEntry(&item)...); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repo) IncrementAccess(ctx context.Context, accountID string, memoryIDs []string) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE memory_entries
		 SET access_count = access_count + 1, last_accessed_at = NOW()
		 WHERE account_id = $1 AND id::text = ANY($2)`,
		accountID, memoryIDs,
	)
	return err
}

func insertEvidence(ctx context.Context, tx pgx.Tx, owner ownership.Owner, memoryID string, in EvidenceInput) (*Evidence, error) {
	metadata, err := marshalJSON(in.Metadata)
	if err != nil {
		return nil, err
	}
	var item Evidence
	err = tx.QueryRow(ctx,
		`INSERT INTO memory_evidence
		 (account_id, user_id, key_id, memory_id, source_type, source_id, excerpt, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, account_id, user_id, key_id, memory_id, source_type, source_id, excerpt, metadata, created_at`,
		owner.Account(), owner.UserID, owner.KeyID, memoryID, in.SourceType, in.SourceID, in.Excerpt, metadata,
	).Scan(
		&item.ID, &item.AccountID, &item.UserID, &item.KeyID, &item.MemoryID,
		&item.SourceType, &item.SourceID, &item.Excerpt, &item.Metadata, &item.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func scanEvent(event *Event) []any {
	return []any{
		&event.ID, &event.AccountID, &event.UserID, &event.KeyID, &event.ScopeType, &event.ScopeKey,
		&event.SessionID, &event.SequenceNo, &event.EventType, &event.Payload, &event.SourceType,
		&event.SourceID, &event.OccurredAt, &event.IdempotencyKey, &event.ContentHash, &event.CreatedAt,
	}
}

const entryColumns = `id, account_id, user_id, key_id, scope_type, scope_key, memory_type, title,
	content, summary, content_hash, confidence, importance, status, metadata, ttl_at, last_accessed_at,
	valid_from, valid_to, superseded_by, source_event_id, extraction_method, extractor_version,
	access_count, useful_count, harmful_count, created_at, updated_at`

func scanEntry(entry *Entry) []any {
	return []any{
		&entry.ID, &entry.AccountID, &entry.UserID, &entry.KeyID, &entry.ScopeType, &entry.ScopeKey,
		&entry.MemoryType, &entry.Title, &entry.Content, &entry.Summary, &entry.ContentHash,
		&entry.Confidence, &entry.Importance, &entry.Status, &entry.Metadata, &entry.TTLAt,
		&entry.LastAccessedAt, &entry.ValidFrom, &entry.ValidTo, &entry.SupersededBy,
		&entry.SourceEventID, &entry.ExtractionMethod, &entry.ExtractorVersion, &entry.AccessCount,
		&entry.UsefulCount, &entry.HarmfulCount, &entry.CreatedAt, &entry.UpdatedAt,
	}
}

func marshalJSON(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return encoded, nil
}
