CREATE TABLE user_groups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK(length(name) BETWEEN 1 AND 80),
	description TEXT NOT NULL DEFAULT '' CHECK(length(description) <= 500),
	revision INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE group_members (
	group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY(group_id, user_id)
);
CREATE INDEX group_members_user ON group_members(user_id, group_id);
CREATE TABLE board_groups (
	board_id INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
	group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	access TEXT NOT NULL CHECK(access IN ('read', 'write')),
	PRIMARY KEY(board_id, group_id)
);
CREATE INDEX board_groups_group ON board_groups(group_id, board_id);
CREATE TRIGGER board_groups_added AFTER INSERT ON board_groups BEGIN
	UPDATE user_groups SET revision = revision + 1 WHERE id = NEW.group_id;
END;
CREATE TRIGGER board_groups_removed AFTER DELETE ON board_groups BEGIN
	UPDATE user_groups SET revision = revision + 1 WHERE id = OLD.group_id;
END;

-- Existing individual grants become overrides. An absent row inherits group grants.
ALTER TABLE board_members RENAME TO old_board_members;
CREATE TABLE board_members (
	board_id INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	access TEXT NOT NULL CHECK(access IN ('none', 'read', 'write')),
	PRIMARY KEY(board_id, user_id)
);
INSERT INTO board_members SELECT * FROM old_board_members;
DROP TABLE old_board_members;
