CREATE TABLE api_logs (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
  method VARCHAR(10) NOT NULL,
  path VARCHAR(500) NOT NULL,
  status_code INT NOT NULL,
  latency_ms INT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_api_logs_key_id ON api_logs(key_id, created_at DESC);
CREATE INDEX idx_api_logs_user_id ON api_logs(user_id, created_at DESC);
CREATE INDEX idx_api_logs_created_at ON api_logs(created_at DESC);
