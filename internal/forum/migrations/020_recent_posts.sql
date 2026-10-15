-- Page through messages in posting order without sorting the whole board.
CREATE INDEX posts_recent ON posts(created_at DESC, id DESC) WHERE removed = 0;
