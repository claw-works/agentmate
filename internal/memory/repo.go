package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		  payload, source_type, source_id, occurred_at, idempotency_key, content_hash, skill_version_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 ON CONFLICT (account_id, idempotency_key) DO NOTHING
		 RETURNING `+eventColumns,
		owner.Account(), owner.UserID, owner.KeyID, req.ScopeType, req.ScopeKey, req.SessionID, req.SequenceNo,
		req.EventType, payload, req.SourceType, req.SourceID, occurredAt, req.IdempotencyKey, contentHash,
		nullableSkillVersionID(req.SkillVersionID),
	).Scan(scanEvent(&event)...)
	if err == nil {
		return &event, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	err = r.pool.QueryRow(ctx,
		`SELECT `+eventColumns+`
		 FROM memory_events
		 WHERE account_id = $1 AND idempotency_key = $2`,
		owner.Account(), req.IdempotencyKey,
	).Scan(scanEvent(&event)...)
	if err != nil {
		return nil, false, err
	}
	return &event, false, nil
}

// nullableSkillVersionID maps an absent attribution to NULL. Storing "" would
// violate the UUID column type, and storing a zero UUID would fabricate a
// reference to a skill version that does not exist.
func nullableSkillVersionID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

const eventColumns = `id, account_id, user_id, key_id, scope_type, scope_key, session_id, sequence_no,
	event_type, payload, source_type, source_id, occurred_at, idempotency_key, content_hash, skill_version_id, created_at`

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

// ScopeUsage 是一个 (scope_type, scope_key) 组合的用量。
type ScopeUsage struct {
	ScopeType  string `json:"scope_type"`
	ScopeKey   string `json:"scope_key"`
	EntryCount int    `json:"entry_count"`
	EventCount int    `json:"event_count"`
}

// ListScopes 返回本账号已经在用的 scope 组合。
//
// scope_key 是自由文本，服务端不该替调用方规定它该是仓库名还是路径——不同项目的
// 惯例不同，硬编一个只会逼人绕开。但"自由"不等于"每个 agent 各编一个"：真实接入
// 里第二个 agent 无从知道第一个用了什么，于是同一个项目散成两个 scope，互相看不见
// 对方的记忆。这个查询让约定可被发现，从而可被跟随。
//
// 按用量降序：用得最多的那个就是这个账号事实上的约定。
func (r *Repo) ListScopes(ctx context.Context, accountID string) ([]ScopeUsage, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT scope_type, scope_key,
		        sum(entry_count)::int AS entry_count,
		        sum(event_count)::int AS event_count
		 FROM (
		   SELECT scope_type, scope_key, count(*) AS entry_count, 0 AS event_count
		   FROM memory_entries WHERE account_id = $1 GROUP BY scope_type, scope_key
		   UNION ALL
		   SELECT scope_type, scope_key, 0 AS entry_count, count(*) AS event_count
		   FROM memory_events WHERE account_id = $1 GROUP BY scope_type, scope_key
		 ) AS combined
		 GROUP BY scope_type, scope_key
		 ORDER BY (sum(entry_count) + sum(event_count)) DESC, scope_type, scope_key`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ScopeUsage, 0)
	for rows.Next() {
		var item ScopeUsage
		if err := rows.Scan(&item.ScopeType, &item.ScopeKey, &item.EntryCount, &item.EventCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
		&event.SourceID, &event.OccurredAt, &event.IdempotencyKey, &event.ContentHash,
		&event.SkillVersionID, &event.CreatedAt,
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

// SkillVersionExistsInAccount reports whether a skill version belongs to the
// account. Memory reads skill_versions directly instead of depending on the
// skills package: this is a single existence probe for an account-scoped
// attribution check, and a package dependency between the two domains would be
// a much heavier coupling than one query.
func (r *Repo) SkillVersionExistsInAccount(ctx context.Context, accountID, skillVersionID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM skill_versions
		    WHERE id = $1::uuid AND account_id = $2
		 )`,
		skillVersionID, accountID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// ─── M1: attribution queries ───

// SessionTimeline merges skill executions and memory events into one
// time-ordered view. Both legs are account-scoped and share the same filter
// semantics: an empty session_id or skill_version_id means "do not filter on
// it", so the same query serves "everything in this session" and "everything
// this skill version touched".
//
// The union runs in the database rather than merging two result sets in Go so
// that LIMIT applies to the merged ordering. Merging after two independent
// LIMITs would silently drop the interleaved tail.
func (r *Repo) SessionTimeline(ctx context.Context, accountID string, params SessionTimelineParams) ([]TimelineItem, error) {
	limit := params.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx,
		`SELECT kind, id::text, occurred_at, session_id, skill_version_id::text,
		        skill_name, skill_version, outcome, was_triggered, failure_reason, duration_ms, event_type
		 FROM (
		   SELECT 'skill_log' AS kind, log.id, log.created_at AS occurred_at,
		          COALESCE(log.session_id, '') AS session_id, log.skill_version_id,
		          COALESCE(log.skill_name, '') AS skill_name, COALESCE(log.skill_version, '') AS skill_version,
		          COALESCE(log.outcome, '') AS outcome, log.was_triggered,
		          COALESCE(log.failure_reason, '') AS failure_reason, log.duration_ms,
		          '' AS event_type
		     FROM skill_logs AS log
		    WHERE log.account_id = $1
		      AND ($2 = '' OR log.session_id = $2)
		      AND ($3 = '' OR log.skill_version_id::text = $3)
		   UNION ALL
		   SELECT 'memory_event' AS kind, event.id, event.occurred_at,
		          COALESCE(event.session_id, '') AS session_id, event.skill_version_id,
		          COALESCE(version.skill_name, '') AS skill_name, COALESCE(version.version, '') AS skill_version,
		          '' AS outcome, NULL::boolean AS was_triggered,
		          '' AS failure_reason, NULL::integer AS duration_ms,
		          event.event_type
		     FROM memory_events AS event
		     LEFT JOIN skill_versions AS version
		       ON version.id = event.skill_version_id AND version.account_id = event.account_id
		    WHERE event.account_id = $1
		      AND ($2 = '' OR event.session_id = $2)
		      AND ($3 = '' OR event.skill_version_id::text = $3)
		 ) AS timeline
		 ORDER BY occurred_at, kind, id
		 LIMIT $4`,
		accountID, params.SessionID, params.SkillVersionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TimelineItem, 0)
	for rows.Next() {
		var item TimelineItem
		if err := rows.Scan(
			&item.Kind, &item.ID, &item.OccurredAt, &item.SessionID, &item.SkillVersionID,
			&item.SkillName, &item.SkillVersion, &item.Outcome, &item.WasTriggered,
			&item.FailureReason, &item.DurationMs, &item.EventType,
		); err != nil {
			return nil, err
		}
		item.Attributed = item.SkillVersionID != nil && *item.SkillVersionID != ""
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetEntryAttribution walks entry -> source event -> skill version in one query.
// Each link is optional, so every projected column is nullable and the caller
// classifies how far the chain resolved.
func (r *Repo) GetEntryAttribution(ctx context.Context, accountID, entryID string) (*EntryAttribution, error) {
	var attribution EntryAttribution
	var sessionID *string
	err := r.pool.QueryRow(ctx,
		`SELECT entry.id::text,
		        event.id::text,
		        event.session_id,
		        event.skill_version_id::text,
		        COALESCE(version.skill_name, ''),
		        COALESCE(version.version, '')
		   FROM memory_entries AS entry
		   LEFT JOIN memory_events AS event
		     ON event.id = entry.source_event_id AND event.account_id = entry.account_id
		   LEFT JOIN skill_versions AS version
		     ON version.id = event.skill_version_id AND version.account_id = event.account_id
		  WHERE entry.account_id = $1 AND entry.id = $2::uuid`,
		accountID, entryID,
	).Scan(
		&attribution.EntryID, &attribution.SourceEventID, &sessionID,
		&attribution.SkillVersionID, &attribution.SkillName, &attribution.SkillVersion,
	)
	if err != nil {
		return nil, err
	}
	if sessionID != nil {
		attribution.SessionID = *sessionID
	}
	return &attribution, nil
}

// ─── M3: supersede ───

// SupersedeEntry marks the superseded entry as replaced by the superseding one,
// in one transaction.
//
// Three invariants are enforced in SQL rather than in the service, so a
// concurrent call cannot slip between check and write:
//
//   - both entries belong to the caller's account;
//   - the superseded entry is not already replaced by a different entry
//     (re-running the same supersede is idempotent, switching the replacement is
//     a conflict);
//   - the pair does not form a cycle — the superseding entry must not already be
//     replaced, directly or transitively, by the entry it is about to replace.
//     Chains are legitimate (C replaces B which replaced A); cycles are not,
//     because they make "which one is current" unanswerable.
//
// valid_to is closed at the supersede time so the replaced entry stops being
// temporally valid, and status moves to superseded so search stops returning it.
func (r *Repo) SupersedeEntry(ctx context.Context, owner ownership.Owner, supersedingID, supersededID string, at time.Time) (*Entry, *Entry, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock both rows in a stable order to avoid deadlocking against a concurrent
	// supersede of the same pair in the opposite direction.
	var lockedStatus string
	for _, id := range sortedPair(supersedingID, supersededID) {
		if err := tx.QueryRow(ctx,
			`SELECT status FROM memory_entries WHERE account_id = $1 AND id = $2::uuid FOR UPDATE`,
			owner.Account(), id,
		).Scan(&lockedStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			return nil, nil, err
		}
	}

	var alreadyReplacedBy *string
	var supersededStatus string
	if err := tx.QueryRow(ctx,
		`SELECT superseded_by::text, status FROM memory_entries WHERE account_id = $1 AND id = $2::uuid`,
		owner.Account(), supersededID,
	).Scan(&alreadyReplacedBy, &supersededStatus); err != nil {
		return nil, nil, err
	}
	if alreadyReplacedBy != nil {
		if *alreadyReplacedBy == supersedingID {
			// Idempotent replay: return the current state unchanged.
			superseding, err := getEntryTx(ctx, tx, owner.Account(), supersedingID)
			if err != nil {
				return nil, nil, err
			}
			superseded, err := getEntryTx(ctx, tx, owner.Account(), supersededID)
			if err != nil {
				return nil, nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, nil, err
			}
			return superseding, superseded, nil
		}
		return nil, nil, fmt.Errorf("%w: entry %s is already superseded by %s", ErrSupersedeConflict, supersededID, *alreadyReplacedBy)
	}

	// Cycle check: walk the superseding entry's own replacement chain.
	var cycle bool
	if err := tx.QueryRow(ctx,
		`WITH RECURSIVE chain AS (
		   SELECT id, superseded_by FROM memory_entries
		    WHERE account_id = $1 AND id = $2::uuid
		   UNION ALL
		   SELECT next.id, next.superseded_by FROM memory_entries AS next
		     JOIN chain ON chain.superseded_by = next.id
		    WHERE next.account_id = $1
		 )
		 SELECT EXISTS (SELECT 1 FROM chain WHERE id = $3::uuid)`,
		owner.Account(), supersedingID, supersededID,
	).Scan(&cycle); err != nil {
		return nil, nil, err
	}
	if cycle {
		return nil, nil, fmt.Errorf("%w: a cycle between %s and %s", ErrSupersedeConflict, supersedingID, supersededID)
	}

	var superseded Entry
	if err := tx.QueryRow(ctx,
		`UPDATE memory_entries
		    SET superseded_by = $3::uuid,
		        status = $4,
		        -- Close the validity window at the supersede time unless the
		        -- entry already expired earlier; moving valid_to forward would
		        -- resurrect it for the interval in between.
		        valid_to = LEAST(COALESCE(valid_to, $5::timestamptz), $5::timestamptz),
		        updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid
		 RETURNING `+entryColumns,
		owner.Account(), supersededID, supersedingID, StatusSuperseded, at,
	).Scan(scanEntry(&superseded)...); err != nil {
		return nil, nil, err
	}

	superseding, err := getEntryTx(ctx, tx, owner.Account(), supersedingID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return superseding, &superseded, nil
}

func getEntryTx(ctx context.Context, tx pgx.Tx, accountID, id string) (*Entry, error) {
	var entry Entry
	if err := tx.QueryRow(ctx,
		`SELECT `+entryColumns+` FROM memory_entries WHERE account_id = $1 AND id = $2::uuid`,
		accountID, id,
	).Scan(scanEntry(&entry)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, err
	}
	return &entry, nil
}

func sortedPair(a, b string) []string {
	if a <= b {
		return []string{a, b}
	}
	return []string{b, a}
}

// ─── M3: feedback ───

// RecordFeedback stores one usefulness signal and moves the entry's counter, in
// one transaction so the counter can never drift from the signal log.
//
// The signal row is the durable record; the counters on memory_entries are a
// denormalised projection kept for ranking, where a per-search aggregate query
// would be too costly. Because the counters are derived, they can be rebuilt
// from memory_feedback at any time.
//
// A repeated (entry, session, signal) triple is ignored rather than counted
// twice: an agent retrying a call must not be able to inflate a memory's
// standing. The insert therefore relies on a unique constraint and reports
// whether it actually created a row.
func (r *Repo) RecordFeedback(ctx context.Context, owner ownership.Owner, in FeedbackRequest, at time.Time) (*Feedback, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	metadata, err := marshalJSON(in.Metadata)
	if err != nil {
		return nil, false, err
	}

	var feedback Feedback
	err = tx.QueryRow(ctx,
		`INSERT INTO memory_feedback
		   (account_id, user_id, key_id, memory_id, signal, reason, session_id, skill_version_id, metadata, observed_at)
		 VALUES ($1, $2, $3, $4::uuid, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (account_id, memory_id, session_id, signal) DO NOTHING
		 RETURNING `+feedbackColumns,
		owner.Account(), nullableString(owner.UserID), owner.KeyID, in.MemoryID, in.Signal, in.Reason,
		in.SessionID, nullableSkillVersionID(in.SkillVersionID), metadata, at,
	).Scan(scanFeedback(&feedback)...)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already recorded: return the existing row without touching counters.
		if err := tx.QueryRow(ctx,
			`SELECT `+feedbackColumns+` FROM memory_feedback
			  WHERE account_id = $1 AND memory_id = $2::uuid AND session_id = $3 AND signal = $4`,
			owner.Account(), in.MemoryID, in.SessionID, in.Signal,
		).Scan(scanFeedback(&feedback)...); err != nil {
			return nil, false, err
		}
		// Still return the entry: a caller that retried wants the current
		// counters, and omitting them would look like the entry has none.
		entry, err := getEntryTx(ctx, tx, owner.Account(), in.MemoryID)
		if err != nil {
			return nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		feedback.Entry = entry
		return &feedback, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	column := "useful_count"
	if in.Signal == SignalHarmful {
		column = "harmful_count"
	}
	var entry Entry
	if err := tx.QueryRow(ctx,
		`UPDATE memory_entries
		    SET `+column+` = `+column+` + 1, updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid
		 RETURNING `+entryColumns,
		owner.Account(), in.MemoryID,
	).Scan(scanEntry(&entry)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("%w: %s", ErrNotFound, in.MemoryID)
		}
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	feedback.Entry = &entry
	return &feedback, true, nil
}

// ListFeedback returns the signal log for one entry, newest first.
func (r *Repo) ListFeedback(ctx context.Context, accountID, memoryID string, limit int) ([]Feedback, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+feedbackColumns+` FROM memory_feedback
		  WHERE account_id = $1 AND memory_id = $2::uuid
		  ORDER BY observed_at DESC, id
		  LIMIT $3`,
		accountID, memoryID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Feedback, 0)
	for rows.Next() {
		var item Feedback
		if err := rows.Scan(scanFeedback(&item)...); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const feedbackColumns = `id, account_id, user_id, key_id, memory_id, signal, reason, session_id,
	skill_version_id, metadata, observed_at, created_at`

func scanFeedback(feedback *Feedback) []any {
	return []any{
		&feedback.ID, &feedback.AccountID, &feedback.UserID, &feedback.KeyID, &feedback.MemoryID,
		&feedback.Signal, &feedback.Reason, &feedback.SessionID, &feedback.SkillVersionID,
		&feedback.Metadata, &feedback.ObservedAt, &feedback.CreatedAt,
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// ─── M3: checkpoint ───

// LatestCheckpoint returns the newest checkpoint event of a session, or nil when
// the session has none.
func (r *Repo) LatestCheckpoint(ctx context.Context, accountID, sessionID string) (*Event, error) {
	var event Event
	err := r.pool.QueryRow(ctx,
		`SELECT `+eventColumns+`
		   FROM memory_events
		  WHERE account_id = $1 AND session_id = $2 AND event_type = 'checkpoint'
		  ORDER BY occurred_at DESC, sequence_no DESC NULLS LAST, created_at DESC
		  LIMIT 1`,
		accountID, sessionID,
	).Scan(scanEvent(&event)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// TimelineSince returns session activity strictly after a point in time. Used to
// show what happened after the last checkpoint, which is the part a naive resume
// would drop.
func (r *Repo) TimelineSince(ctx context.Context, accountID, sessionID string, since time.Time, limit int) ([]TimelineItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx,
		`SELECT kind, id::text, occurred_at, session_id, skill_version_id::text,
		        skill_name, skill_version, outcome, was_triggered, failure_reason, duration_ms, event_type
		 FROM (
		   SELECT 'skill_log' AS kind, log.id, log.created_at AS occurred_at,
		          COALESCE(log.session_id, '') AS session_id, log.skill_version_id,
		          COALESCE(log.skill_name, '') AS skill_name, COALESCE(log.skill_version, '') AS skill_version,
		          COALESCE(log.outcome, '') AS outcome, log.was_triggered,
		          COALESCE(log.failure_reason, '') AS failure_reason, log.duration_ms,
		          '' AS event_type
		     FROM skill_logs AS log
		    WHERE log.account_id = $1 AND log.session_id = $2 AND log.created_at > $3
		   UNION ALL
		   SELECT 'memory_event' AS kind, event.id, event.occurred_at,
		          COALESCE(event.session_id, '') AS session_id, event.skill_version_id,
		          COALESCE(version.skill_name, '') AS skill_name, COALESCE(version.version, '') AS skill_version,
		          '' AS outcome, NULL::boolean AS was_triggered,
		          '' AS failure_reason, NULL::integer AS duration_ms,
		          event.event_type
		     FROM memory_events AS event
		     LEFT JOIN skill_versions AS version
		       ON version.id = event.skill_version_id AND version.account_id = event.account_id
		    WHERE event.account_id = $1 AND event.session_id = $2 AND event.occurred_at > $3
		 ) AS timeline
		 ORDER BY occurred_at, kind, id
		 LIMIT $4`,
		accountID, sessionID, since, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TimelineItem, 0)
	for rows.Next() {
		var item TimelineItem
		if err := rows.Scan(
			&item.Kind, &item.ID, &item.OccurredAt, &item.SessionID, &item.SkillVersionID,
			&item.SkillName, &item.SkillVersion, &item.Outcome, &item.WasTriggered,
			&item.FailureReason, &item.DurationMs, &item.EventType,
		); err != nil {
			return nil, err
		}
		item.Attributed = item.SkillVersionID != nil && *item.SkillVersionID != ""
		items = append(items, item)
	}
	return items, rows.Err()
}
