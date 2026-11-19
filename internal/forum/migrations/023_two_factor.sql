CREATE TABLE IF NOT EXISTS twofactor_secrets (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 ciphertext BLOB NOT NULL CHECK(length(ciphertext)=60),
 key_id TEXT NOT NULL CHECK(length(key_id)=64),
 last_step INTEGER CHECK(last_step>=0),
 draft_session TEXT NOT NULL CHECK(length(draft_session) IN (0,64)),
 draft_password TEXT NOT NULL CHECK(length(draft_password) IN (0,64)),
 draft_until INTEGER NOT NULL,
 challenge_attempts INTEGER NOT NULL DEFAULT 0 CHECK(challenge_attempts BETWEEN 0 AND 12),
 challenge_end INTEGER NOT NULL DEFAULT 0,
 CHECK((last_step IS NULL AND length(draft_session)=64 AND length(draft_password)=64 AND draft_until>0)
       OR (last_step IS NOT NULL AND draft_session='' AND draft_password='' AND draft_until=0))
);
CREATE TABLE IF NOT EXISTS twofactor_recovery (
 user_id INTEGER NOT NULL REFERENCES twofactor_secrets(user_id) ON DELETE CASCADE,
 digest TEXT NOT NULL CHECK(length(digest)=64),
 PRIMARY KEY(user_id,digest)
);
CREATE TABLE IF NOT EXISTS twofactor_challenges (
 digest TEXT PRIMARY KEY CHECK(length(digest)=64),
 user_id INTEGER NOT NULL REFERENCES twofactor_secrets(user_id) ON DELETE CASCADE,
 browser TEXT NOT NULL CHECK(length(browser)=64),
 password TEXT NOT NULL CHECK(length(password)=64),
 expires INTEGER NOT NULL,
 remaining INTEGER NOT NULL CHECK(remaining BETWEEN 0 AND 5),
 UNIQUE(user_id,browser)
);
CREATE INDEX IF NOT EXISTS twofactor_challenges_expiry ON twofactor_challenges(expires);

-- An account deletion leaves a tombstone for posts, but erases its factors.
CREATE TRIGGER IF NOT EXISTS twofactor_deleted_member AFTER UPDATE OF deleted ON users
WHEN OLD.deleted=0 AND NEW.deleted=1
BEGIN
 DELETE FROM twofactor_secrets WHERE user_id=NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS twofactor_invalidated_login AFTER UPDATE OF password_hash,suspended ON users
WHEN OLD.password_hash IS NOT NEW.password_hash OR OLD.suspended IS NOT NEW.suspended
BEGIN
 DELETE FROM twofactor_challenges WHERE user_id=NEW.id;
 DELETE FROM twofactor_secrets WHERE user_id=NEW.id AND last_step IS NULL;
END;
