-- servers holds the user's Mattermost servers and their session tokens.
-- Kept apart from cache tables (added in stage 2) so "reset cache" and a
-- rebuilt cache snapshot never sign the user out.
CREATE TABLE servers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    url        TEXT    NOT NULL UNIQUE,
    site_url   TEXT    NOT NULL DEFAULT '',
    name       TEXT    NOT NULL,
    sort       INTEGER NOT NULL DEFAULT 0,
    token      TEXT    NOT NULL DEFAULT '',
    user_id    TEXT    NOT NULL DEFAULT '',
    username   TEXT    NOT NULL DEFAULT '',
    gitlab     INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);
