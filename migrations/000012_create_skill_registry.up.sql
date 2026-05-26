-- Skill 执行日志（学习信号来源）
CREATE TABLE skill_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,

  -- Skill 标识
  skill_name VARCHAR(100) NOT NULL,
  skill_version VARCHAR(20) DEFAULT 'unknown',

  -- 执行上下文
  agent_id VARCHAR(100) NOT NULL DEFAULT '',
  session_id VARCHAR(200) DEFAULT '',
  trigger_text TEXT DEFAULT '',

  -- 执行结果（SkillEvolver 学习信号）
  was_triggered BOOLEAN NOT NULL DEFAULT true,
  outcome VARCHAR(20) NOT NULL,
  failure_reason TEXT DEFAULT '',
  user_correction TEXT DEFAULT '',

  -- 执行详情
  tool_calls JSONB,
  duration_ms INT,

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_skill_logs_skill_name ON skill_logs(skill_name, created_at DESC);
CREATE INDEX idx_skill_logs_agent ON skill_logs(agent_id, created_at DESC);
CREATE INDEX idx_skill_logs_outcome ON skill_logs(skill_name, outcome);

-- Skill 版本注册表（留底 + 版本追踪）
CREATE TABLE skill_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,

  skill_name VARCHAR(100) NOT NULL,
  version VARCHAR(20) NOT NULL,
  content TEXT NOT NULL,
  content_hash VARCHAR(64) NOT NULL,

  agent_id VARCHAR(100) DEFAULT '',
  change_summary TEXT DEFAULT '',
  eval_pass_rate FLOAT,
  is_active BOOLEAN NOT NULL DEFAULT false,

  published_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_skill_versions_name ON skill_versions(skill_name, published_at DESC);
CREATE INDEX idx_skill_versions_active ON skill_versions(skill_name, is_active) WHERE is_active = true;
CREATE UNIQUE INDEX idx_skill_versions_hash ON skill_versions(skill_name, content_hash);
