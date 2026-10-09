-- Existing PNG avatars remain unchanged. Viewer preferences are independent.
ALTER TABLE user_avatars ADD COLUMN animation BLOB NOT NULL DEFAULT X''
 CHECK(length(animation)=0 OR (length(animation) BETWEEN 6 AND 4194304 AND substr(animation,1,6) IN (X'474946383761',X'474946383961')));
CREATE TABLE avatar_preferences (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 animate INTEGER NOT NULL DEFAULT 1 CHECK(animate IN (0,1))
);
