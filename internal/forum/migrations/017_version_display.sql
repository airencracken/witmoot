ALTER TABLE instance_branding ADD COLUMN show_version INTEGER NOT NULL DEFAULT 0
	CHECK(show_version IN (0, 1));
