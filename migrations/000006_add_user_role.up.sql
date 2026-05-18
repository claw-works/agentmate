ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(20) NOT NULL DEFAULT 'user';
UPDATE users SET role = 'admin' WHERE email = 'wellxie@local.dev';
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
