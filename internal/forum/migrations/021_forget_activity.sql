-- Activity metrics keep only counts a server log would show. Drop the table
-- of per-member sign-ins and per-message and per-image opens that earlier
-- versions kept. The shared library does the same on startup.
DROP TABLE IF EXISTS comfy_analytics_activity;
