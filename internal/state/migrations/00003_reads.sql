-- +goose Up
CREATE TABLE reads (
  user TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, content TEXT NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY (user, path)
);

-- +goose Down
DROP TABLE reads;
