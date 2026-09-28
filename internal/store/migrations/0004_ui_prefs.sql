-- ui_prefs holds small, app-wide UI preferences that are not tied to a
-- server (theme brief 2026-09-28: splitter widths) as a flat key-value
-- table. Read once at startup (never blocks it -- the UI asks after mount
-- and applies its own defaults until it answers) and written on commit
-- (e.g. a splitter's pointerup), not on every intermediate frame.
CREATE TABLE ui_prefs (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
