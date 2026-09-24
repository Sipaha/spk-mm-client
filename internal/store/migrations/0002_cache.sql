-- cache_entries is the write-behind snapshot of each server's hot state
-- (internal/state): metadata, post windows, users. Separate from servers so
-- "reset cache" and a rebuilt snapshot never sign the user out; rows go away
-- with their server.
CREATE TABLE cache_entries (
    server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind      TEXT    NOT NULL,
    key       TEXT    NOT NULL,
    data      BLOB    NOT NULL,
    PRIMARY KEY (server_id, kind, key)
) WITHOUT ROWID;
