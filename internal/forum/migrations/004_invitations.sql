ALTER TABLE invitations RENAME TO invitations_legacy;
CREATE TABLE invitations (
	id INTEGER PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	prefix TEXT NOT NULL DEFAULT '',
	label TEXT NOT NULL DEFAULT '' CHECK(length(label) <= 64),
	created_by INTEGER NOT NULL REFERENCES users(id),
	created_at INTEGER NOT NULL,
	max_uses INTEGER NOT NULL DEFAULT 1 CHECK(max_uses BETWEEN 0 AND 10000),
	uses INTEGER NOT NULL DEFAULT 0 CHECK(uses >= 0),
	expires_at INTEGER,
	revoked_at INTEGER
);
INSERT INTO invitations(token_hash, created_by, created_at, expires_at)
	SELECT token_hash, created_by, expires_at - 604800, expires_at FROM invitations_legacy;
DROP TABLE invitations_legacy;
