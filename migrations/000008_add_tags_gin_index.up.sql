CREATE INDEX IF NOT EXISTS idx_todos_tags ON todos USING GIN(tags);
CREATE INDEX IF NOT EXISTS idx_notes_tags ON notes USING GIN(tags);
