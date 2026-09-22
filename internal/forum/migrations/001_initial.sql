CREATE TABLE users (
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK(length(username) BETWEEN 3 AND 24),
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('member', 'owner')),
	created_at INTEGER NOT NULL
);
CREATE TABLE boards (
	id INTEGER PRIMARY KEY,
	category TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	position INTEGER NOT NULL UNIQUE
);
CREATE TABLE topics (
	id INTEGER PRIMARY KEY,
	board_id INTEGER NOT NULL REFERENCES boards(id),
	author_id INTEGER NOT NULL REFERENCES users(id),
	title TEXT NOT NULL CHECK(length(title) BETWEEN 3 AND 160),
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE posts (
	id INTEGER PRIMARY KEY,
	topic_id INTEGER NOT NULL REFERENCES topics(id),
	author_id INTEGER NOT NULL REFERENCES users(id),
	body TEXT NOT NULL CHECK(length(body) BETWEEN 1 AND 20000),
	created_at INTEGER NOT NULL
);
CREATE TABLE sessions (
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE INDEX topics_board_activity ON topics(board_id, updated_at DESC, id DESC);
CREATE INDEX topics_activity ON topics(updated_at DESC, id DESC);
CREATE INDEX posts_topic ON posts(topic_id, id);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE invitations (
	token_hash TEXT PRIMARY KEY,
	created_by INTEGER NOT NULL REFERENCES users(id),
	expires_at INTEGER NOT NULL
);
INSERT INTO boards (category, name, description, position) VALUES
	('Make yourself at home', 'The kitchen table', 'Little updates, big questions, and whatever is on your mind.', 1),
	('Make yourself at home', 'Plans & get-togethers', 'Sunday lunch, the next trip, or just a good excuse to catch up.', 2),
	('Pass it around', 'Things we are making', 'A half-finished project is a perfectly good thing to share.', 3),
	('Pass it around', 'Good finds', 'Recipes, books, music, and things you thought we would like.', 4),
	('Our little corner', 'Around here', 'Say hello, ask for a hand, or suggest something for this place.', 5);
