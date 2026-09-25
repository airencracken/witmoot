-- Optional member avatars.
--
-- The bytes live in SQLite beside the branding assets so an operator backup is
-- still just "copy the data directory": there are no avatar files to sweep and
-- nothing to leak through a path. Images are normalized to a 256px PNG on the
-- way in, which also strips any embedded metadata.

CREATE TABLE user_avatars (
	user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	content BLOB NOT NULL,
	updated_at INTEGER NOT NULL
);
