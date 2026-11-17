-- +goose Up
CREATE TABLE agent_tokens (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, hash BLOB NOT NULL UNIQUE,
  created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0, revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE activity (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, agent TEXT NOT NULL, action TEXT NOT NULL, path TEXT NOT NULL
);
CREATE INDEX activity_ts ON activity(ts);

-- +goose Down
DROP INDEX activity_ts;
DROP TABLE activity;
DROP TABLE agent_tokens;
