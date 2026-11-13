-- The owner's site-wide color palette. Empty means Witmoot's default, sage.
ALTER TABLE instance_branding ADD COLUMN palette TEXT NOT NULL DEFAULT '';
