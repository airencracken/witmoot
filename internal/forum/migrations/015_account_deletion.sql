-- Keep anonymous placeholders for conversation positions and foreign keys,
-- while removing every credential, profile detail and contribution body.
ALTER TABLE users ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0
 CHECK(deleted IN (0,1) AND (deleted=0 OR
  (role='member' AND suspended=1 AND password_hash='' AND email='' AND can_invite=0)));
