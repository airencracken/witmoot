-- Optional member email addresses.
--
-- Witmoot does not need an address to run: accounts are made by invitation and
-- recovered by an owner handing over a reset link. An address is only used when
-- the operator has configured an SMTP relay, so a member can receive that link
-- themselves. Empty means no address on file.

ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
