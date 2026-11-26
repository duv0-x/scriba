-- +goose Up
-- Varias KBs por instancia (ADR-013). El slug por defecto no se conoce aquí (sale del repo de GitHub):
-- las filas existentes quedan con kb = '' y Store.AdoptKB las asigna en una transacción.
ALTER TABLE agent_tokens ADD COLUMN kb TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN kb TEXT NOT NULL DEFAULT '';
CREATE TABLE reads_kb (
  kb TEXT NOT NULL, user TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, content TEXT NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY (kb, user, path)
);
INSERT INTO reads_kb(kb, user, path, hash, content, at) SELECT '', user, path, hash, content, at FROM reads;
DROP TABLE reads;
ALTER TABLE reads_kb RENAME TO reads;
-- Repo destino y estado del push por KB; la cuenta y el secreto siguen en connectors.
CREATE TABLE kb_connectors (
  name TEXT NOT NULL, kb TEXT NOT NULL,
  repo TEXT NOT NULL DEFAULT '', push INTEGER NOT NULL DEFAULT 1,
  last_push TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (name, kb)
);
INSERT INTO kb_connectors(name, kb, repo, push, last_push, last_error)
  SELECT name, '', repo, push, last_push, last_error FROM connectors WHERE name = 'github';
UPDATE connectors SET repo = '', push = 1, last_push = '', last_error = '' WHERE name = 'github';

-- +goose Down
UPDATE connectors SET
  repo = COALESCE((SELECT repo FROM kb_connectors k WHERE k.name = connectors.name ORDER BY k.kb LIMIT 1), repo),
  push = COALESCE((SELECT push FROM kb_connectors k WHERE k.name = connectors.name ORDER BY k.kb LIMIT 1), push)
  WHERE name = 'github';
DROP TABLE kb_connectors;
CREATE TABLE reads_old (
  user TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, content TEXT NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY (user, path)
);
INSERT OR IGNORE INTO reads_old(user, path, hash, content, at) SELECT user, path, hash, content, at FROM reads;
DROP TABLE reads;
ALTER TABLE reads_old RENAME TO reads;
ALTER TABLE activity DROP COLUMN kb;
ALTER TABLE agent_tokens DROP COLUMN kb;
