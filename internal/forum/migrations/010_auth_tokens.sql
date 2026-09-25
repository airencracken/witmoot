-- One-time tokens, used today for password resets.
--
-- Tokens are stored as a SHA-256 digest, exactly like sessions and invitation
-- codes, so a database leak does not hand over working reset links. Consumption
-- is recorded rather than deleted, so a replay can be told apart from a token
-- that never existed. Only the newest token for a person and purpose is kept.

CREATE TABLE auth_tokens (
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	purpose TEXT NOT NULL CHECK(purpose IN ('password_reset')),
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	used_at INTEGER
);
CREATE INDEX auth_tokens_user ON auth_tokens(user_id, purpose);
CREATE INDEX auth_tokens_expiry ON auth_tokens(expires_at);
