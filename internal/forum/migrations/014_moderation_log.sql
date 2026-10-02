-- The owner activity log links each removed message, records a replaced
-- conversation title, and covers avatar removals. SQLite cannot change a CHECK
-- constraint in place, so the table is rebuilt with its rows.
CREATE TABLE community_events_next (
	id INTEGER PRIMARY KEY,
	actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	actor_name TEXT NOT NULL,
	action TEXT NOT NULL CHECK(action IN ('suspend', 'restore', 'remove-message', 'remove-avatar')),
	subject TEXT NOT NULL,
	post_id INTEGER REFERENCES posts(id) ON DELETE SET NULL,
	title_replaced INTEGER NOT NULL DEFAULT 0 CHECK(title_replaced IN (0, 1) AND (title_replaced = 0 OR action = 'remove-message')),
	created_at INTEGER NOT NULL
);

INSERT INTO community_events_next(id, actor_id, actor_name, action, subject, post_id, created_at)
SELECT e.id, e.actor_id, e.actor_name, e.action,
	CASE WHEN e.action = 'remove-message' AND e.subject LIKE 'Message %' THEN 'message' || substr(e.subject, 8) ELSE e.subject END,
	CASE WHEN e.action = 'remove-message' AND e.subject LIKE 'Message % in conversation %'
		THEN (SELECT p.id FROM posts p WHERE p.id = CAST(substr(e.subject, 9, instr(e.subject, ' in conversation ') - 9) AS INTEGER)) END,
	e.created_at
FROM community_events e;

DROP TABLE community_events;
ALTER TABLE community_events_next RENAME TO community_events;
