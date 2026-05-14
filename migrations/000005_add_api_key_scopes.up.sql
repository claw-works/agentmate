ALTER TABLE api_keys ADD COLUMN scopes TEXT[] NOT NULL DEFAULT '{}';
COMMENT ON COLUMN api_keys.scopes IS 'Empty array means full access. Examples: todos:rw, todos:r, notes:rw, notes:r, manage_keys';
