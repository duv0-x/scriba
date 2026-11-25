-- +goose Up
ALTER TABLE agent_tokens ADD COLUMN client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tokens ADD COLUMN expires INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_tokens ADD COLUMN refresh_hash BLOB;
ALTER TABLE agent_tokens ADD COLUMN prev_refresh_hash BLOB;
ALTER TABLE agent_tokens ADD COLUMN refresh_expires INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE agent_tokens DROP COLUMN refresh_expires;
ALTER TABLE agent_tokens DROP COLUMN prev_refresh_hash;
ALTER TABLE agent_tokens DROP COLUMN refresh_hash;
ALTER TABLE agent_tokens DROP COLUMN expires;
ALTER TABLE agent_tokens DROP COLUMN client_id;
