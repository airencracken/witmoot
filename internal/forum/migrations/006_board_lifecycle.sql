ALTER TABLE boards ADD COLUMN archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1));

-- Keep deleted URLs retired, including when the highest numbered row is removed.
CREATE TABLE object_sequences (
	kind TEXT PRIMARY KEY,
	last_id INTEGER NOT NULL
);
INSERT INTO object_sequences VALUES
	('boards', (SELECT coalesce(max(id), 0) FROM boards)),
	('topics', (SELECT coalesce(max(id), 0) FROM topics)),
	('posts', (SELECT coalesce(max(id), 0) FROM posts)),
	('attachments', (SELECT coalesce(max(id), 0) FROM attachments));
CREATE TRIGGER boards_sequence AFTER INSERT ON boards BEGIN
	UPDATE object_sequences SET last_id = max(last_id, NEW.id) WHERE kind = 'boards';
END;
CREATE TRIGGER topics_sequence AFTER INSERT ON topics BEGIN
	UPDATE object_sequences SET last_id = max(last_id, NEW.id) WHERE kind = 'topics';
END;
CREATE TRIGGER posts_sequence AFTER INSERT ON posts BEGIN
	UPDATE object_sequences SET last_id = max(last_id, NEW.id) WHERE kind = 'posts';
END;
CREATE TRIGGER attachments_sequence AFTER INSERT ON attachments BEGIN
	UPDATE object_sequences SET last_id = max(last_id, NEW.id) WHERE kind = 'attachments';
END;
