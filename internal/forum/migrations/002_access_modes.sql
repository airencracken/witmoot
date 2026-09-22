CREATE TABLE settings (
	id INTEGER PRIMARY KEY CHECK(id = 1),
	mode TEXT NOT NULL CHECK(mode IN ('personal', 'private', 'open'))
);
INSERT INTO settings (id, mode) VALUES (1, 'private');
ALTER TABLE topics ADD COLUMN audience TEXT NOT NULL DEFAULT 'members' CHECK(audience IN ('owners', 'members', 'public'));
CREATE INDEX topics_audience_activity ON topics(audience, updated_at DESC, id DESC);
