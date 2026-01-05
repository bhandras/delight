-- Session UI event log
--
-- Stores best-effort UI events (thinking/tool) so mobile clients can reconnect
-- and replay transient progress updates that were emitted while the app was in
-- the background.
CREATE TABLE IF NOT EXISTS session_ui_events (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_session_ui_events_account_at
    ON session_ui_events(account_id, at_ms);

CREATE INDEX IF NOT EXISTS idx_session_ui_events_session_at
    ON session_ui_events(session_id, at_ms);
