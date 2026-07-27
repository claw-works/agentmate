package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

const sourceColumns = `id, account_id, user_id, key_id, name, type, repository_url, package_path, domain, default_ref, sync_mode, status, active_revision_id, metadata, created_at, updated_at`
const revisionColumns = `id, account_id, source_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, manifest, status, error, created_at`
const documentSummaryColumns = `id, source_id, revision_id, path, sha256, size_bytes, mime_type, indexable, created_at`

// ─── Sources ───

func (r *Repo) UpsertSource(ctx context.Context, owner ownership.Owner, req CreateKnowledgeSourceRequest) (*KnowledgeSource, error) {
	metadata, err := normalizeMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}
	var source KnowledgeSource
	err = r.pool.QueryRow(ctx,
		`INSERT INTO knowledge_sources
		 (account_id, user_id, key_id, name, type, repository_url, package_path, domain, default_ref, sync_mode, status, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (account_id, name)
		 DO UPDATE SET
		   user_id = EXCLUDED.user_id,
		   key_id = EXCLUDED.key_id,
		   type = EXCLUDED.type,
		   repository_url = EXCLUDED.repository_url,
		   package_path = EXCLUDED.package_path,
		   domain = EXCLUDED.domain,
		   default_ref = EXCLUDED.default_ref,
		   sync_mode = EXCLUDED.sync_mode,
		   status = EXCLUDED.status,
		   metadata = EXCLUDED.metadata,
		   updated_at = NOW()
		 RETURNING `+sourceColumns,
		owner.Account(), nullableString(owner.UserID), owner.KeyID, req.Name, req.Type, req.RepositoryURL, req.PackagePath, req.Domain, req.DefaultRef, req.SyncMode, req.Status, metadata,
	).Scan(scanSource(&source)...)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func (r *Repo) ListSources(ctx context.Context, accountID string, params KnowledgeSourceListParams) ([]KnowledgeSource, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+sourceColumns+`
		 FROM knowledge_sources
		 WHERE account_id = $1
		   AND ($2 = '' OR type = $2)
		   AND ($3 = '' OR status = $3)
		 ORDER BY updated_at DESC, created_at DESC
		 LIMIT $4 OFFSET $5`,
		accountID, params.Type, params.Status, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KnowledgeSource, 0)
	for rows.Next() {
		var source KnowledgeSource
		if err := rows.Scan(scanSource(&source)...); err != nil {
			return nil, err
		}
		items = append(items, source)
	}
	return items, rows.Err()
}

func (r *Repo) GetSource(ctx context.Context, accountID, id string) (*KnowledgeSource, error) {
	var source KnowledgeSource
	err := r.pool.QueryRow(ctx,
		`SELECT `+sourceColumns+` FROM knowledge_sources WHERE id = $1 AND account_id = $2`,
		id, accountID,
	).Scan(scanSource(&source)...)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

// RecordSourceError marks a source as failed with sync state metadata. It
// runs outside the ingest transaction so a failed sync never leaves a
// half-produced revision behind.
func (r *Repo) RecordSourceError(ctx context.Context, accountID, sourceID string, state GitSourceSyncState) (*KnowledgeSource, error) {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var source KnowledgeSource
	err = r.pool.QueryRow(ctx,
		`UPDATE knowledge_sources
		 SET status = 'error',
		     metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('sync', $3::jsonb),
		     updated_at = NOW()
		 WHERE id = $1 AND account_id = $2
		 RETURNING `+sourceColumns,
		sourceID, accountID, string(stateJSON),
	).Scan(scanSource(&source)...)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

// ─── Revisions ───

func (r *Repo) ListSourceRevisions(ctx context.Context, accountID, sourceID string, limit, offset int) ([]KnowledgeSourceRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+revisionColumns+`
		 FROM knowledge_source_revisions
		 WHERE account_id = $1 AND source_id = $2
		 ORDER BY created_at DESC
		 LIMIT $3 OFFSET $4`,
		accountID, sourceID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KnowledgeSourceRevision, 0)
	for rows.Next() {
		var revision KnowledgeSourceRevision
		if err := rows.Scan(scanRevision(&revision)...); err != nil {
			return nil, err
		}
		items = append(items, revision)
	}
	return items, rows.Err()
}

func (r *Repo) GetRevision(ctx context.Context, accountID, id string) (*KnowledgeSourceRevision, error) {
	var revision KnowledgeSourceRevision
	err := r.pool.QueryRow(ctx,
		`SELECT `+revisionColumns+` FROM knowledge_source_revisions WHERE id = $1 AND account_id = $2`,
		id, accountID,
	).Scan(scanRevision(&revision)...)
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

// IngestRevision creates one immutable revision plus its documents and moves
// the source active pointer inside a single transaction. Package-internal
// Markdown links are written in the same transaction as a rebuildable
// derivation. Replaying the same package_hash is idempotent: the existing
// revision is returned and the active pointer is re-targeted at it.
// Concurrency is serialized with an advisory lock per source, mirroring the
// skills lock pattern.
func (r *Repo) IngestRevision(ctx context.Context, owner ownership.Owner, source *KnowledgeSource, revisionIn KnowledgeSourceRevision, documents []KnowledgeDocument, links []DocumentLinkInput, syncState *GitSourceSyncState) (*KnowledgeSourceRevision, []KnowledgeDocumentSummary, error) {
	if revisionIn.RevisionKey == "" {
		return nil, nil, fmt.Errorf("revision_key required")
	}
	if revisionIn.PackageHash == "" {
		return nil, nil, fmt.Errorf("package_hash required")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	if err := lockKnowledgeSourceTx(ctx, tx, source.ID); err != nil {
		return nil, nil, err
	}

	existing, err := findRevisionByIdentityTx(ctx, tx, owner.Account(), source.ID, revisionIn)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	if existing != nil {
		if existing.PackageHash != revisionIn.PackageHash || existing.TreeHash != revisionIn.TreeHash {
			return nil, nil, fmt.Errorf("revision identity already exists with different package content")
		}
		updatedSource, err := setActiveRevisionTx(ctx, tx, owner.Account(), source.ID, existing.ID, syncState)
		if err != nil {
			return nil, nil, err
		}
		summaries, err := listDocumentSummariesTx(ctx, tx, owner.Account(), existing.ID)
		if err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		*source = *updatedSource
		return existing, summaries, nil
	}

	revision := &KnowledgeSourceRevision{}
	err = tx.QueryRow(ctx,
		`INSERT INTO knowledge_source_revisions
		 (account_id, source_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, manifest, status, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'ingested', '')
		 RETURNING `+revisionColumns,
		owner.Account(), source.ID, revisionIn.RevisionKey, revisionIn.CommitSHA, revisionIn.LocalSnapshotID, revisionIn.TreeHash, revisionIn.PackageHash, ensureJSONObject(revisionIn.Manifest),
	).Scan(scanRevision(revision)...)
	if err != nil {
		return nil, nil, err
	}

	summaries := make([]KnowledgeDocumentSummary, 0, len(documents))
	documentIDsByPath := make(map[string]string, len(documents))
	for _, document := range documents {
		var summary KnowledgeDocumentSummary
		err = tx.QueryRow(ctx,
			`INSERT INTO knowledge_documents
			 (account_id, source_id, revision_id, path, sha256, size_bytes, mime_type, indexable, content_snapshot)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 RETURNING `+documentSummaryColumns,
			owner.Account(), source.ID, revision.ID, document.Path, document.SHA256, document.SizeBytes, document.MimeType, document.Indexable, document.ContentSnapshot,
		).Scan(scanDocumentSummary(&summary)...)
		if err != nil {
			return nil, nil, err
		}
		summaries = append(summaries, summary)
		documentIDsByPath[summary.Path] = summary.ID
	}

	if err := upsertRevisionLinksTx(ctx, tx, owner.Account(), revision.ID, documentIDsByPath, links); err != nil {
		return nil, nil, err
	}

	updatedSource, err := setActiveRevisionTx(ctx, tx, owner.Account(), source.ID, revision.ID, syncState)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	*source = *updatedSource
	return revision, summaries, nil
}

// ─── Documents ───

func (r *Repo) ListRevisionDocuments(ctx context.Context, accountID, revisionID string, params DocumentListParams) ([]KnowledgeDocumentSummary, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+documentSummaryColumns+`
		 FROM knowledge_documents
		 WHERE account_id = $1 AND revision_id = $2
		 ORDER BY path
		 LIMIT $3 OFFSET $4`,
		accountID, revisionID, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KnowledgeDocumentSummary, 0)
	for rows.Next() {
		var summary KnowledgeDocumentSummary
		if err := rows.Scan(scanDocumentSummary(&summary)...); err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	return items, rows.Err()
}

func (r *Repo) CountRevisionDocuments(ctx context.Context, accountID, revisionID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_documents WHERE account_id = $1 AND revision_id = $2`,
		accountID, revisionID,
	).Scan(&count)
	return count, err
}

func (r *Repo) GetDocument(ctx context.Context, accountID, revisionID, documentID string) (*KnowledgeDocument, error) {
	var document KnowledgeDocument
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, source_id, revision_id, path, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM knowledge_documents
		 WHERE id = $1 AND revision_id = $2 AND account_id = $3`,
		documentID, revisionID, accountID,
	).Scan(
		&document.ID,
		&document.AccountID,
		&document.SourceID,
		&document.RevisionID,
		&document.Path,
		&document.SHA256,
		&document.SizeBytes,
		&document.MimeType,
		&document.Indexable,
		&document.ContentSnapshot,
		&document.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &document, nil
}

// GetDocumentByID loads one account-scoped document without requiring the
// caller to know its revision.
func (r *Repo) GetDocumentByID(ctx context.Context, accountID, documentID string) (*KnowledgeDocument, error) {
	var document KnowledgeDocument
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, source_id, revision_id, path, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM knowledge_documents
		 WHERE id = $1 AND account_id = $2`,
		documentID, accountID,
	).Scan(
		&document.ID,
		&document.AccountID,
		&document.SourceID,
		&document.RevisionID,
		&document.Path,
		&document.SHA256,
		&document.SizeBytes,
		&document.MimeType,
		&document.Indexable,
		&document.ContentSnapshot,
		&document.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &document, nil
}

// ListRevisionIndexableDocuments loads full indexable text documents of one
// revision in stable path order for chunking. The result stays bounded by
// the per-package snapshot file cap enforced at ingest.
func (r *Repo) ListRevisionIndexableDocuments(ctx context.Context, accountID, revisionID string) ([]KnowledgeDocument, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, source_id, revision_id, path, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM knowledge_documents
		 WHERE account_id = $1 AND revision_id = $2 AND indexable = true AND content_snapshot <> ''
		 ORDER BY path`,
		accountID, revisionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KnowledgeDocument, 0)
	for rows.Next() {
		var document KnowledgeDocument
		if err := rows.Scan(
			&document.ID,
			&document.AccountID,
			&document.SourceID,
			&document.RevisionID,
			&document.Path,
			&document.SHA256,
			&document.SizeBytes,
			&document.MimeType,
			&document.Indexable,
			&document.ContentSnapshot,
			&document.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, document)
	}
	return items, rows.Err()
}

// ─── Document links ───

// RebuildRevisionLinks re-derives the link rows of one immutable revision.
// Revisions never change, so re-deriving produces the same set; the upsert
// keeps replays idempotent.
func (r *Repo) RebuildRevisionLinks(ctx context.Context, accountID, revisionID string, documentIDsByPath map[string]string, links []DocumentLinkInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := upsertRevisionLinksTx(ctx, tx, accountID, revisionID, documentIDsByPath, links); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertRevisionLinksTx(ctx context.Context, tx pgx.Tx, accountID, revisionID string, documentIDsByPath map[string]string, links []DocumentLinkInput) error {
	for _, link := range links {
		sourceID, ok := documentIDsByPath[link.SourcePath]
		if !ok {
			continue
		}
		var targetID any
		if resolved, ok := documentIDsByPath[link.TargetPath]; ok {
			targetID = resolved
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO knowledge_document_links
			 (account_id, revision_id, source_document_id, target_document_id, target_path)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (revision_id, source_document_id, target_path)
			 DO UPDATE SET target_document_id = EXCLUDED.target_document_id`,
			accountID, revisionID, sourceID, targetID, link.TargetPath,
		); err != nil {
			return err
		}
	}
	return nil
}

// ListDocumentLinks returns both directions of one document's links in a
// stable order: outgoing links (by target path) before incoming links (by
// linking document path).
func (r *Repo) ListDocumentLinks(ctx context.Context, accountID, documentID string, limit, offset int) ([]KnowledgeDocumentLinkItem, error) {
	rows, err := r.pool.Query(ctx,
		`(SELECT 'out' AS direction, link.target_document_id AS document_id, link.target_path AS path
		  FROM knowledge_document_links AS link
		  WHERE link.account_id = $1 AND link.source_document_id = $2)
		 UNION ALL
		 (SELECT 'in' AS direction, link.source_document_id, source_document.path
		  FROM knowledge_document_links AS link
		  JOIN knowledge_documents AS source_document
		    ON source_document.id = link.source_document_id AND source_document.account_id = link.account_id
		  WHERE link.account_id = $1 AND link.target_document_id = $2)
		 ORDER BY direction DESC, path, document_id
		 LIMIT $3 OFFSET $4`,
		accountID, documentID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KnowledgeDocumentLinkItem, 0)
	for rows.Next() {
		var item KnowledgeDocumentLinkItem
		if err := rows.Scan(&item.Direction, &item.DocumentID, &item.Path); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repo) CountDocumentLinks(ctx context.Context, accountID, documentID string) (int, error) {
	var total int
	err := r.pool.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM knowledge_document_links WHERE account_id = $1 AND source_document_id = $2)
		 + (SELECT count(*) FROM knowledge_document_links WHERE account_id = $1 AND target_document_id = $2)`,
		accountID, documentID,
	).Scan(&total)
	return total, err
}

// ListLinksForDocuments batch-loads both directions of links for a bounded
// hit set, grouped by the anchor document ID. Per-document totals stay
// bounded by the per-document link cap enforced at derivation time.
func (r *Repo) ListLinksForDocuments(ctx context.Context, accountID string, documentIDs []string, perDocumentLimit int) (map[string][]KnowledgeDocumentLinkItem, error) {
	result := make(map[string][]KnowledgeDocumentLinkItem, len(documentIDs))
	if len(documentIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx,
		`(SELECT link.source_document_id::text AS anchor, 'out' AS direction, link.target_document_id AS document_id, link.target_path AS path
		  FROM knowledge_document_links AS link
		  WHERE link.account_id = $1 AND link.source_document_id::text = ANY($2))
		 UNION ALL
		 (SELECT link.target_document_id::text, 'in', link.source_document_id, source_document.path
		  FROM knowledge_document_links AS link
		  JOIN knowledge_documents AS source_document
		    ON source_document.id = link.source_document_id AND source_document.account_id = link.account_id
		  WHERE link.account_id = $1 AND link.target_document_id::text = ANY($2))
		 ORDER BY anchor, direction DESC, path, document_id`,
		accountID, documentIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var anchor string
		var item KnowledgeDocumentLinkItem
		if err := rows.Scan(&anchor, &item.Direction, &item.DocumentID, &item.Path); err != nil {
			return nil, err
		}
		if perDocumentLimit > 0 && len(result[anchor]) >= perDocumentLimit {
			continue
		}
		result[anchor] = append(result[anchor], item)
	}
	return result, rows.Err()
}

// ─── helpers ───

func findRevisionByIdentityTx(ctx context.Context, tx pgx.Tx, accountID, sourceID string, revisionIn KnowledgeSourceRevision) (*KnowledgeSourceRevision, error) {
	var revision KnowledgeSourceRevision
	err := tx.QueryRow(ctx,
		`SELECT `+revisionColumns+`
		 FROM knowledge_source_revisions
		 WHERE account_id = $1 AND source_id = $2
		   AND (revision_key = $3 OR package_hash = $4)`,
		accountID, sourceID, revisionIn.RevisionKey, revisionIn.PackageHash,
	).Scan(scanRevision(&revision)...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, err
	}
	return &revision, nil
}

func setActiveRevisionTx(ctx context.Context, tx pgx.Tx, accountID, sourceID, revisionID string, syncState *GitSourceSyncState) (*KnowledgeSource, error) {
	var source KnowledgeSource
	if syncState == nil {
		err := tx.QueryRow(ctx,
			`UPDATE knowledge_sources
			 SET status = 'active', active_revision_id = $3, updated_at = NOW()
			 WHERE id = $1 AND account_id = $2
			 RETURNING `+sourceColumns,
			sourceID, accountID, revisionID,
		).Scan(scanSource(&source)...)
		if err != nil {
			return nil, err
		}
		return &source, nil
	}
	stateJSON, err := json.Marshal(syncState)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx,
		`UPDATE knowledge_sources
		 SET status = 'active',
		     active_revision_id = $3,
		     metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('sync', $4::jsonb),
		     updated_at = NOW()
		 WHERE id = $1 AND account_id = $2
		 RETURNING `+sourceColumns,
		sourceID, accountID, revisionID, string(stateJSON),
	).Scan(scanSource(&source)...)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func listDocumentSummariesTx(ctx context.Context, tx pgx.Tx, accountID, revisionID string) ([]KnowledgeDocumentSummary, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+documentSummaryColumns+`
		 FROM knowledge_documents
		 WHERE account_id = $1 AND revision_id = $2
		 ORDER BY path`,
		accountID, revisionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KnowledgeDocumentSummary, 0)
	for rows.Next() {
		var summary KnowledgeDocumentSummary
		if err := rows.Scan(scanDocumentSummary(&summary)...); err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	return items, rows.Err()
}

func lockKnowledgeSourceTx(ctx context.Context, tx pgx.Tx, sourceID string) error {
	_, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"knowledge-source:"+sourceID,
	)
	return err
}

func normalizeMetadata(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("metadata must be valid JSON")
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	return []byte(raw), nil
}

func ensureJSONObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return []byte(raw)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func scanSource(source *KnowledgeSource) []any {
	return []any{
		&source.ID,
		&source.AccountID,
		&source.UserID,
		&source.KeyID,
		&source.Name,
		&source.Type,
		&source.RepositoryURL,
		&source.PackagePath,
		&source.Domain,
		&source.DefaultRef,
		&source.SyncMode,
		&source.Status,
		&source.ActiveRevisionID,
		&source.Metadata,
		&source.CreatedAt,
		&source.UpdatedAt,
	}
}

func scanRevision(revision *KnowledgeSourceRevision) []any {
	return []any{
		&revision.ID,
		&revision.AccountID,
		&revision.SourceID,
		&revision.RevisionKey,
		&revision.CommitSHA,
		&revision.LocalSnapshotID,
		&revision.TreeHash,
		&revision.PackageHash,
		&revision.Manifest,
		&revision.Status,
		&revision.Error,
		&revision.CreatedAt,
	}
}

func scanDocumentSummary(summary *KnowledgeDocumentSummary) []any {
	return []any{
		&summary.ID,
		&summary.SourceID,
		&summary.RevisionID,
		&summary.Path,
		&summary.SHA256,
		&summary.SizeBytes,
		&summary.MimeType,
		&summary.Indexable,
		&summary.CreatedAt,
	}
}
