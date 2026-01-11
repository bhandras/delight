-- Session turn state (durable turn boundaries)
--
-- This table tracks whether a session currently has an in-flight turn and the
-- most recent start/end timestamps. It exists so clients can recover correct
-- busy/thinking state after reconnecting (e.g. phone background/sleep) even if
-- transient websocket "ephemeral" events were missed.

CREATE TABLE IF NOT EXISTS session_turn_states (
    session_id TEXT PRIMARY KEY,
    open INTEGER NOT NULL DEFAULT 0,
    started_at_ms INTEGER,
    completed_at_ms INTEGER,
    updated_at_ms INTEGER NOT NULL DEFAULT 0,

    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_session_turn_states_open ON session_turn_states(open, updated_at_ms);

