-- Domain as first-class source metadata.
--
-- Skill and Knowledge packages are organised by domain inside a repository
-- ("platform/retrieval", "product/faq"). Before this migration the domain
-- existed only as a substring of package_path, so it could not be grouped,
-- filtered or indexed on.
--
-- The value is derived from the first path segment and written by the Go
-- registries (internal/pkgpath.Domain) so a single implementation owns the
-- rule. The backfill below intentionally mirrors that rule: a single-segment
-- path has NO domain, because a flat package is not organised by domain and
-- reporting its own name as the domain would invent a grouping that does not
-- exist. Unclassified sources therefore hold '' rather than NULL, keeping the
-- column non-null and comparisons free of tri-state logic.

ALTER TABLE skill_sources
  ADD COLUMN domain VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE knowledge_sources
  ADD COLUMN domain VARCHAR(160) NOT NULL DEFAULT '';

UPDATE skill_sources
   SET domain = split_part(trim(both '/' from package_path), '/', 1)
 WHERE position('/' in trim(both '/' from package_path)) > 0;

UPDATE knowledge_sources
   SET domain = split_part(trim(both '/' from package_path), '/', 1)
 WHERE position('/' in trim(both '/' from package_path)) > 0;

-- Domain listing and per-domain filtering are always account-scoped.
CREATE INDEX idx_skill_sources_domain
  ON skill_sources(account_id, domain)
  WHERE domain <> '';
CREATE INDEX idx_knowledge_sources_domain
  ON knowledge_sources(account_id, domain)
  WHERE domain <> '';
