CREATE TABLE imvault_connections (
	user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	server TEXT NOT NULL,
	username TEXT NOT NULL,
	token BLOB NOT NULL
);
CREATE TABLE attachments (
	id INTEGER PRIMARY KEY,
	post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	server TEXT NOT NULL,
	remote_id TEXT NOT NULL,
	credential_user_id INTEGER REFERENCES users(id),
	name TEXT NOT NULL,
	rendition TEXT NOT NULL CHECK(rendition IN ('preview', 'thumb')),
	UNIQUE(post_id, server, remote_id)
);
CREATE INDEX attachments_post ON attachments(post_id);
