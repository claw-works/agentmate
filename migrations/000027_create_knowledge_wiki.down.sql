ALTER TABLE knowledge_sources DROP CONSTRAINT IF EXISTS knowledge_sources_active_build_fkey;
ALTER TABLE knowledge_sources DROP COLUMN IF EXISTS active_build_id;

DROP TABLE IF EXISTS knowledge_build_events;
DROP TABLE IF EXISTS knowledge_page_citations;
DROP TABLE IF EXISTS knowledge_page_links;
DROP TABLE IF EXISTS knowledge_pages;
DROP TABLE IF EXISTS knowledge_build_revisions;
DROP TABLE IF EXISTS knowledge_profile_versions;
DROP TABLE IF EXISTS knowledge_profiles;
