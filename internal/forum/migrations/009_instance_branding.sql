CREATE TABLE instance_branding (
	id INTEGER PRIMARY KEY CHECK(id = 1),
	name TEXT NOT NULL DEFAULT '',
	source_url TEXT NOT NULL DEFAULT '',
	welcome_title TEXT NOT NULL DEFAULT '',
	welcome_text TEXT NOT NULL DEFAULT ''
);

CREATE TABLE branding_assets (
	name TEXT PRIMARY KEY CHECK(name IN ('favicon', 'mascot')),
	content BLOB NOT NULL
);
