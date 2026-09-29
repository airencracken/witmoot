ALTER TABLE users ADD COLUMN suspended INTEGER NOT NULL DEFAULT 0
	CHECK(suspended IN (0, 1) AND (suspended = 0 OR role = 'member'));
ALTER TABLE users ADD COLUMN suspension_revision INTEGER NOT NULL DEFAULT 0 CHECK(suspension_revision >= 0);
ALTER TABLE posts ADD COLUMN removed INTEGER NOT NULL DEFAULT 0
	CHECK(removed IN (0, 1) AND (removed = 0 OR body = 'This message was removed by a site owner.'));
ALTER TABLE instance_branding ADD COLUMN house_rules TEXT NOT NULL DEFAULT '' CHECK(length(house_rules) <= 5000);
ALTER TABLE instance_branding ADD COLUMN owner_contact TEXT NOT NULL DEFAULT '' CHECK(length(owner_contact) <= 500);

CREATE TABLE community_events (
	id INTEGER PRIMARY KEY,
	actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	actor_name TEXT NOT NULL,
	action TEXT NOT NULL CHECK(action IN ('suspend', 'restore', 'remove-message')),
	subject TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
