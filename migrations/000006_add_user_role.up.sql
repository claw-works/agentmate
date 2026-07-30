ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(20) NOT NULL DEFAULT 'user';

-- This migration used to promote one hardcoded developer address to admin. The
-- address is gone: it named a person, and a schema migration is the wrong place to
-- grant a privilege to an individual.
--
-- Removing it changes nothing anywhere. Users are created at runtime by registration
-- while this migration runs immediately after the table exists, so on any database
-- built from scratch the UPDATE matched zero rows regardless of the address. It only
-- ever did anything on the one development database where that account happened to
-- predate it, and that database has long since applied this migration and will not
-- apply it again.
--
-- Grant admin explicitly instead:
--   UPDATE users SET role = 'admin' WHERE email = '<address>';

CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
