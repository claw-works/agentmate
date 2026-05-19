CREATE TABLE reports (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title VARCHAR(500) NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  format VARCHAR(10) NOT NULL DEFAULT 'md',
  tags TEXT[] NOT NULL DEFAULT '{}',
  source VARCHAR(255) NOT NULL DEFAULT '',
  source_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reports_user_id ON reports(user_id);
CREATE INDEX idx_reports_created_at ON reports(user_id, created_at DESC);
CREATE INDEX idx_reports_tags ON reports USING GIN(tags);
CREATE INDEX idx_reports_search ON reports USING GIN(
  to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))
);
