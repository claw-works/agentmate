-- K3.6: wiki pages enter retrieval.
--
-- Only the active build's pages are searchable. Builds are immutable and retained, so
-- indexing all of them would multiply the index by build count and return pages from
-- wikis that were rolled back — while the read API serves the restored one. Two places
-- answering the same question differently is the failure this column exists to prevent.

ALTER TABLE knowledge_sources
  -- indexed_build_id records which build the retrieval index currently reflects. It is
  -- deliberately separate from active_build_id: indexing costs embedding round trips and
  -- cannot run inside the pointer move, so the two legitimately differ for a while. What
  -- must not happen is that difference being invisible — search filters on the active
  -- build, so a stale index returns fewer hits rather than wrong ones, and the catalog
  -- reports the gap.
  ADD COLUMN indexed_build_id UUID,
  ADD COLUMN wiki_indexed_at TIMESTAMPTZ;

ALTER TABLE knowledge_sources
  ADD CONSTRAINT knowledge_sources_indexed_build_fkey
  FOREIGN KEY (indexed_build_id, account_id)
  REFERENCES knowledge_build_revisions(id, account_id) ON DELETE SET NULL;
