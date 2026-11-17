-- +goose Up
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE connectors (
  name TEXT PRIMARY KEY, account TEXT NOT NULL, secret BLOB NOT NULL,
  repo TEXT NOT NULL DEFAULT '', push INTEGER NOT NULL DEFAULT 1,
  last_push TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
  updated INTEGER NOT NULL
);

-- +goose Down
DROP TABLE connectors;
DROP TABLE settings;
