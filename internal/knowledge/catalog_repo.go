package knowledge

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/claw-works/agentmate/internal/retrieval"
)

// catalogRecord is one raw K0 catalog row before manifest decoding.
type catalogRecord struct {
	SourceID         string
	Name             string
	Domain           string
	Type             string
	ActiveRevisionID string
	PackageHash      string
	Manifest         json.RawMessage
	DocumentCount    int
	IndexedChunks    int
	FailedChunks     int
	PendingChunks    int
}

const catalogFilterClause = `
  AND ($2 = '' OR lower(source.name) LIKE '%' || $2 || '%'
       OR lower(COALESCE(revision.manifest->>'description', '')) LIKE '%' || $2 || '%'
       OR lower(COALESCE(revision.manifest->>'name', '')) LIKE '%' || $2 || '%')
  AND ($3 = '' OR source.domain = $3)`

// ListCatalog returns K0 collection cards for sources that have an active
// ingested revision, in a stable name order. Chunk counts come from the
// rebuildable retrieval projection (namespace 'knowledge').
func (r *Repo) ListCatalog(ctx context.Context, accountID string, params KnowledgeCatalogListParams) ([]catalogRecord, error) {
	query := strings.ToLower(strings.TrimSpace(params.Query))
	rows, err := r.pool.Query(ctx,
		`SELECT source.id, source.name, source.domain, source.type, revision.id, revision.package_hash, revision.manifest,
		        (SELECT count(*) FROM knowledge_documents AS document
		          WHERE document.account_id = source.account_id AND document.revision_id = revision.id) AS document_count,
		        COALESCE(chunk_stats.indexed, 0), COALESCE(chunk_stats.failed, 0), COALESCE(chunk_stats.pending, 0)
		 FROM knowledge_sources AS source
		 JOIN knowledge_source_revisions AS revision
		   ON revision.id = source.active_revision_id AND revision.account_id = source.account_id
		 LEFT JOIN LATERAL (
		   SELECT count(*) FILTER (WHERE rd.status = 'indexed') AS indexed,
		          count(*) FILTER (WHERE rd.status = 'failed') AS failed,
		          count(*) FILTER (WHERE rd.status = 'pending') AS pending
		   FROM retrieval_documents AS rd
		   WHERE rd.account_id = source.account_id
		     AND rd.namespace = '`+retrieval.NamespaceKnowledge+`'
		     AND rd.source_type = '`+knowledgeChunkSourceType+`'
		     AND rd.metadata @> jsonb_build_object('source_id', source.id::text, 'revision_id', revision.id::text)
		 ) AS chunk_stats ON true
		 WHERE source.account_id = $1
		   AND source.active_revision_id IS NOT NULL`+catalogFilterClause+`
		 ORDER BY source.domain, lower(source.name), source.name, source.id
		 LIMIT $4 OFFSET $5`,
		accountID, query, params.Domain, params.Limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]catalogRecord, 0)
	for rows.Next() {
		var record catalogRecord
		if err := rows.Scan(
			&record.SourceID,
			&record.Name,
			&record.Domain,
			&record.Type,
			&record.ActiveRevisionID,
			&record.PackageHash,
			&record.Manifest,
			&record.DocumentCount,
			&record.IndexedChunks,
			&record.FailedChunks,
			&record.PendingChunks,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *Repo) CountCatalog(ctx context.Context, accountID, query, domain string) (int, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	var total int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*)
		 FROM knowledge_sources AS source
		 JOIN knowledge_source_revisions AS revision
		   ON revision.id = source.active_revision_id AND revision.account_id = source.account_id
		 WHERE source.account_id = $1
		   AND source.active_revision_id IS NOT NULL`+catalogFilterClause,
		accountID, query, domain,
	).Scan(&total)
	return total, err
}

// ListCatalogDomains returns the domains present in an account's catalog with
// their collection counts. Unclassified sources (flat package paths) are
// omitted rather than grouped under an empty domain.
func (r *Repo) ListCatalogDomains(ctx context.Context, accountID string) ([]KnowledgeDomainCount, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT source.domain, count(*)
		 FROM knowledge_sources AS source
		 WHERE source.account_id = $1
		   AND source.active_revision_id IS NOT NULL
		   AND source.domain <> ''
		 GROUP BY source.domain
		 ORDER BY source.domain`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := make([]KnowledgeDomainCount, 0)
	for rows.Next() {
		var item KnowledgeDomainCount
		if err := rows.Scan(&item.Domain, &item.CollectionCount); err != nil {
			return nil, err
		}
		domains = append(domains, item)
	}
	return domains, rows.Err()
}

// ListIndexableSources returns sources that carry an active revision,
// optionally narrowed to one source ID, for chunk indexing.
func (r *Repo) ListIndexableSources(ctx context.Context, accountID, sourceID string) ([]KnowledgeSource, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+sourceColumns+`
		 FROM knowledge_sources
		 WHERE account_id = $1
		   AND active_revision_id IS NOT NULL
		   AND status <> 'disabled'
		   AND ($2 = '' OR id::text = $2)
		 ORDER BY lower(name), name, id`,
		accountID, sourceID,
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

// ListSourceIDsByDomain returns the account's source IDs owned by one domain
// that carry an active revision. Used to resolve a domain-scoped search into
// the existing source_ids filter, which already enforces ownership.
func (r *Repo) ListSourceIDsByDomain(ctx context.Context, accountID, domain string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id
		 FROM knowledge_sources
		 WHERE account_id = $1
		   AND domain = $2
		   AND domain <> ''
		   AND active_revision_id IS NOT NULL
		   AND status <> 'disabled'
		 ORDER BY lower(name), name, id`,
		accountID, strings.TrimSpace(domain),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
