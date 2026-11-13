-- Activity metrics keep only counts a server log would show. Remove the
-- per-member sign-ins and per-message and per-image opens stored before.
DELETE FROM comfy_analytics_activity WHERE member <> '' OR item <> '';
