-- K3.1/K3.2: the compiled wiki layer.
--
-- Design: docs/knowledge-wiki-compiler-k3-v0.1.md
--
-- The central constraint is that a wiki build is NOT reproducible. Compiling the
-- same sources twice with the same model yields different text, so unlike the
-- Skill compiled catalog this is not a derived cache that can be dropped and
-- rebuilt. It is customer data with provenance, which forces four properties
-- into the schema:
--
--   1. Builds are immutable and kept, never overwritten in place — one bad
--      compilation must not destroy the previous result.
--   2. Provenance is complete: raw package hash, profile version, compiler
--      version, model, prompt version. Missing any one of them makes a later
--      quality regression impossible to attribute.
--   3. Content hashes exist for diffing and incremental reuse only. They are
--      explicitly NOT an idempotency key: equal hashes do not prove logical
--      equivalence and unequal hashes do not prove the input changed.
--   4. Activation is a pointer move, so rollback is activating an older build.

-- ─── profiles: the declarative page conventions ───

CREATE TABLE knowledge_profiles (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  -- Matches the `profile` string in KNOWLEDGE.yaml, which until now was an
  -- uninterpreted label.
  name VARCHAR(160) NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (account_id, name),
  UNIQUE (id, account_id)
);

-- Profile versions are immutable: a profile change alters compiler output, so it
-- has to be part of build provenance the same way the prompt version is.
CREATE TABLE knowledge_profile_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  profile_id UUID NOT NULL,
  version INTEGER NOT NULL,

  -- Page kinds this profile allows, and the required frontmatter per kind.
  allowed_page_kinds TEXT[] NOT NULL DEFAULT ARRAY['summary','entity','concept','overview','synthesis','index','log']::TEXT[],
  allowed_link_types TEXT[] NOT NULL DEFAULT ARRAY['references','contradicts','supersedes','elaborates','mentions_entity']::TEXT[],
  -- 'required' makes an uncited factual claim a build failure, not a warning.
  citation_policy VARCHAR(32) NOT NULL DEFAULT 'required',

  -- Cost and shape ceilings, enforced by check.
  max_pages INTEGER NOT NULL DEFAULT 200,
  max_page_chars INTEGER NOT NULL DEFAULT 20000,
  max_build_tokens INTEGER NOT NULL DEFAULT 400000,
  -- Allowed drift in page count versus the parent build, as a fraction. A build
  -- that collapses 30 pages into 3 is far more likely to be a compiler failure
  -- than a legitimate rewrite.
  max_page_count_drift NUMERIC(4,2) NOT NULL DEFAULT 0.50,

  -- Free-form compilation guidance handed to the model.
  instructions TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_profile_versions_citation_policy_check
    CHECK (citation_policy IN ('required', 'preferred', 'optional')),
  CONSTRAINT knowledge_profile_versions_max_pages_check CHECK (max_pages BETWEEN 1 AND 5000),
  CONSTRAINT knowledge_profile_versions_drift_check CHECK (max_page_count_drift > 0 AND max_page_count_drift <= 1),

  FOREIGN KEY (profile_id, account_id)
    REFERENCES knowledge_profiles(id, account_id) ON DELETE CASCADE,
  UNIQUE (profile_id, version),
  UNIQUE (id, account_id)
);

-- ─── builds ───

CREATE TABLE knowledge_build_revisions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  source_id UUID NOT NULL,
  -- The immutable raw snapshot this build read.
  source_revision_id UUID NOT NULL,
  -- Denormalised so provenance survives raw-side cleanup.
  raw_package_hash VARCHAR(64) NOT NULL,
  profile_version_id UUID NOT NULL,

  -- Provenance of the generating side.
  compiler_version VARCHAR(64) NOT NULL,
  model VARCHAR(128) NOT NULL,
  prompt_version VARCHAR(64) NOT NULL,
  -- Provenance of the reviewing side. reviewer_independence records how separated
  -- the reviewer actually was, so collusion risk is visible in the data instead
  -- of depending on someone recalling the configuration.
  reviewer_model VARCHAR(128) NOT NULL DEFAULT '',
  reviewer_prompt_version VARCHAR(64) NOT NULL DEFAULT '',
  reviewer_independence VARCHAR(32) NOT NULL DEFAULT 'unavailable',

  -- Baseline for incremental compilation; NULL for a full build.
  parent_build_id UUID,
  mode VARCHAR(16) NOT NULL DEFAULT 'full',
  status VARCHAR(16) NOT NULL DEFAULT 'queued',

  -- check is the only gate; review only annotates and never blocks activation,
  -- because an unreproducible verdict must not sit on the blocking path.
  check_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  check_failures JSONB NOT NULL DEFAULT '[]'::jsonb,
  review_status VARCHAR(16) NOT NULL DEFAULT 'skipped',

  pages_written INTEGER NOT NULL DEFAULT 0,
  pages_reused INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cost_micros BIGINT NOT NULL DEFAULT 0,
  review_tokens INTEGER NOT NULL DEFAULT 0,
  review_cost_micros BIGINT NOT NULL DEFAULT 0,

  error TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_build_revisions_mode_check CHECK (mode IN ('full', 'incremental')),
  CONSTRAINT knowledge_build_revisions_status_check
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
  CONSTRAINT knowledge_build_revisions_check_status_check
    CHECK (check_status IN ('pending', 'passed', 'failed')),
  CONSTRAINT knowledge_build_revisions_review_status_check
    CHECK (review_status IN ('skipped', 'clean', 'flagged', 'failed')),
  CONSTRAINT knowledge_build_revisions_independence_check
    CHECK (reviewer_independence IN ('cross_provider', 'same_provider', 'same_model', 'unavailable')),

  FOREIGN KEY (source_id, account_id)
    REFERENCES knowledge_sources(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (source_revision_id, account_id)
    REFERENCES knowledge_source_revisions(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (profile_version_id, account_id)
    REFERENCES knowledge_profile_versions(id, account_id) ON DELETE RESTRICT,
  FOREIGN KEY (parent_build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE SET NULL,
  UNIQUE (id, account_id)
);

CREATE INDEX idx_knowledge_build_revisions_source
  ON knowledge_build_revisions(account_id, source_id, created_at DESC);
-- Input identity: repeating a build with the same inputs returns the existing
-- succeeded build instead of paying for a second compilation. Content hashes
-- cannot serve this purpose because LLM output is not reproducible.
CREATE INDEX idx_knowledge_build_revisions_input_identity
  ON knowledge_build_revisions(account_id, source_revision_id, profile_version_id,
                               compiler_version, model, prompt_version)
  WHERE status = 'succeeded';

-- The active wiki pointer lives on the source, mirroring active_revision_id for
-- raw. Rollback is therefore just moving this pointer to an older build.
ALTER TABLE knowledge_sources
  ADD COLUMN active_build_id UUID;
ALTER TABLE knowledge_sources
  ADD CONSTRAINT knowledge_sources_active_build_fkey
  FOREIGN KEY (active_build_id, account_id)
  REFERENCES knowledge_build_revisions(id, account_id) ON DELETE SET NULL;

-- ─── pages ───

CREATE TABLE knowledge_pages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  build_id UUID NOT NULL,

  path TEXT NOT NULL,
  kind VARCHAR(32) NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  frontmatter JSONB NOT NULL DEFAULT '{}'::jsonb,
  -- For diffing and incremental reuse only, never for idempotency.
  content_hash VARCHAR(64) NOT NULL,
  -- Set when the page was copied unchanged from the parent build, which is how
  -- incremental compilation keeps cost down.
  derived_from_build_id UUID,

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_pages_path_check CHECK (char_length(path) BETWEEN 1 AND 512),

  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (derived_from_build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE SET NULL,
  UNIQUE (build_id, path),
  UNIQUE (id, account_id)
);

CREATE INDEX idx_knowledge_pages_build ON knowledge_pages(account_id, build_id, path);
CREATE INDEX idx_knowledge_pages_kind ON knowledge_pages(account_id, build_id, kind);

-- ─── typed links ───

-- Links close within one build so a build is a self-consistent graph that can be
-- rolled back as a unit. Cross-package links are K5.
CREATE TABLE knowledge_page_links (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  build_id UUID NOT NULL,
  source_page_id UUID NOT NULL,
  -- NULL when the target path does not exist in this build. A dangling link is
  -- kept rather than dropped: it is a useful lint signal.
  target_page_id UUID,
  target_path TEXT NOT NULL,
  link_type VARCHAR(32) NOT NULL,
  -- Why the compiler drew this edge; for contradicts and supersedes this is the
  -- part a human actually needs.
  note TEXT NOT NULL DEFAULT '',

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_page_links_type_check
    CHECK (link_type IN ('references', 'contradicts', 'supersedes', 'elaborates', 'mentions_entity')),

  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (source_page_id, account_id)
    REFERENCES knowledge_pages(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (target_page_id, account_id)
    REFERENCES knowledge_pages(id, account_id) ON DELETE CASCADE,
  UNIQUE (build_id, source_page_id, target_path, link_type)
);

CREATE INDEX idx_knowledge_page_links_out
  ON knowledge_page_links(account_id, source_page_id, link_type);
CREATE INDEX idx_knowledge_page_links_in
  ON knowledge_page_links(account_id, target_page_id, link_type)
  WHERE target_page_id IS NOT NULL;
-- Orphan detection and contradiction lint scan by build.
CREATE INDEX idx_knowledge_page_links_build ON knowledge_page_links(build_id, link_type);

-- ─── citations ───

-- The credibility basis of the whole layer: a page is LLM-generated, so only a
-- citation makes a claim checkable.
CREATE TABLE knowledge_page_citations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  build_id UUID NOT NULL,
  page_id UUID NOT NULL,

  -- Anchor into the raw layer. document_id is NULL when the compiler cited a
  -- path that does not exist in the revision; check treats that as a failure,
  -- but the row is stored so the failure can be reported precisely.
  document_id UUID,
  document_path TEXT NOT NULL,
  heading_path TEXT NOT NULL DEFAULT '',
  chunk_key TEXT NOT NULL DEFAULT '',
  -- The claim this citation supports, so review can check one assertion against
  -- one source rather than a whole page against a whole document.
  claim TEXT NOT NULL DEFAULT '',
  excerpt TEXT NOT NULL DEFAULT '',

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (page_id, account_id)
    REFERENCES knowledge_pages(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (document_id, account_id)
    REFERENCES knowledge_documents(id, account_id) ON DELETE SET NULL
);

CREATE INDEX idx_knowledge_page_citations_page ON knowledge_page_citations(account_id, page_id);
CREATE INDEX idx_knowledge_page_citations_document
  ON knowledge_page_citations(account_id, document_id)
  WHERE document_id IS NOT NULL;
-- Stale-citation lint looks for citations whose document disappeared.
CREATE INDEX idx_knowledge_page_citations_build ON knowledge_page_citations(build_id);

-- ─── build events ───

-- Append-only structured log. The `log` page is a rendering of this, so the
-- timeline is queryable by SQL as well as readable by an agent.
CREATE TABLE knowledge_build_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  build_id UUID NOT NULL,
  sequence_no INTEGER NOT NULL,
  event_type VARCHAR(32) NOT NULL,
  page_path TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE CASCADE,
  UNIQUE (build_id, sequence_no)
);

CREATE INDEX idx_knowledge_build_events_build ON knowledge_build_events(build_id, sequence_no);
