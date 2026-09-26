-- downloads is the browser-like list of saved files: the latest
-- MaxDownloads entries (older ones are trimmed on insert), newest first by
-- started_at (raised when a saved file is asked for again). state is
-- downloading | done | failed; error holds the failure's API code. Not tied
-- to servers: the saved file outlives its server entry. Times are unix ms.
CREATE TABLE downloads (
    id          INTEGER PRIMARY KEY,
    server_id   INTEGER NOT NULL,
    file_id     TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    path        TEXT    NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    mime        TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0,
    state       TEXT    NOT NULL,
    error       TEXT    NOT NULL DEFAULT ''
);
