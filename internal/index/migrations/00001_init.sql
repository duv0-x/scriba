-- +goose Up
CREATE TABLE notes (
  path TEXT PRIMARY KEY, title TEXT NOT NULL, type TEXT NOT NULL, date TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '', words INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL
);
CREATE TABLE links (src TEXT NOT NULL, raw TEXT NOT NULL, dst TEXT, PRIMARY KEY (src, raw));
CREATE INDEX links_dst ON links(dst);
CREATE VIRTUAL TABLE notes_fts USING fts5(path UNINDEXED, title, body, tags, tokenize = 'unicode61 remove_diacritics 2');
-- +goose Down
DROP TABLE notes_fts;
DROP TABLE links;
DROP TABLE notes;
