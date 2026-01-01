-- Initial schema for Delight Server (minimal features)
-- Compatible with Delight iOS app protocol

-- Accounts (users)
CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    public_key TEXT NOT NULL UNIQUE,
    seq INTEGER NOT NULL DEFAULT 0,
    feed_seq INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- User settings (encrypted)
    settings TEXT,
    settings_version INTEGER NOT NULL DEFAULT 0,

    -- Profile
    first_name TEXT,
    last_name TEXT,
    username TEXT UNIQUE,
    avatar_url TEXT,
    avatar_width INTEGER,
    avatar_height INTEGER,
    avatar_thumbhash TEXT
);

CREATE INDEX idx_accounts_public_key ON accounts(public_key);
CREATE INDEX idx_accounts_username ON accounts(username);

-- Terminal authentication requests (QR code auth)
CREATE TABLE terminal_auth_requests (
    id TEXT PRIMARY KEY,
    public_key TEXT NOT NULL UNIQUE,
    response TEXT,
    response_account_id TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (response_account_id) REFERENCES accounts(id)
);

CREATE INDEX idx_terminal_auth_public_key ON terminal_auth_requests(public_key);
CREATE INDEX idx_terminal_auth_created_at ON terminal_auth_requests(created_at);

-- Account authentication requests (account-to-account pairing)
CREATE TABLE account_auth_requests (
    id TEXT PRIMARY KEY,
    public_key TEXT NOT NULL UNIQUE,
    response TEXT,
    response_account_id TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (response_account_id) REFERENCES accounts(id)
);

CREATE INDEX idx_account_auth_public_key ON account_auth_requests(public_key);

-- Push notification tokens
CREATE TABLE account_push_tokens (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    token TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    UNIQUE(account_id, token)
);

CREATE INDEX idx_push_tokens_account_id ON account_push_tokens(account_id);

-- Sessions (Claude Code sessions)
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    tag TEXT NOT NULL,
    account_id TEXT NOT NULL,

    -- Encrypted data
    metadata TEXT NOT NULL,
    metadata_version INTEGER NOT NULL DEFAULT 0,
    agent_state TEXT,
    agent_state_version INTEGER NOT NULL DEFAULT 0,
    data_encryption_key BLOB,

    -- State
    seq INTEGER NOT NULL DEFAULT 0,
    active INTEGER NOT NULL DEFAULT 1,
    last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    UNIQUE(account_id, tag)
);

CREATE INDEX idx_sessions_account_id ON sessions(account_id);
CREATE INDEX idx_sessions_account_updated ON sessions(account_id, updated_at DESC);
CREATE INDEX idx_sessions_active ON sessions(active, last_active_at);

-- Session messages
CREATE TABLE session_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    local_id TEXT,
    seq INTEGER NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    UNIQUE(session_id, local_id)
);

CREATE INDEX idx_messages_session_id ON session_messages(session_id);
CREATE INDEX idx_messages_session_seq ON session_messages(session_id, seq);

-- Machines (CLI/daemon instances)
CREATE TABLE machines (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,

    -- Encrypted data
    metadata TEXT NOT NULL,
    metadata_version INTEGER NOT NULL DEFAULT 0,
    daemon_state TEXT,
    daemon_state_version INTEGER NOT NULL DEFAULT 0,
    data_encryption_key BLOB,

    -- State
    seq INTEGER NOT NULL DEFAULT 0,
    active INTEGER NOT NULL DEFAULT 1,
    last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX idx_machines_account_id ON machines(account_id);
CREATE INDEX idx_machines_active ON machines(active, last_active_at);

-- Key-value store for user data
CREATE TABLE user_kv_store (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT,
    version INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    UNIQUE(account_id, key)
);

CREATE INDEX idx_kv_account_id ON user_kv_store(account_id);
CREATE INDEX idx_kv_account_key ON user_kv_store(account_id, key);

-- Artifacts
CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    header BLOB NOT NULL,
    header_version INTEGER NOT NULL DEFAULT 0,
    body BLOB NOT NULL,
    body_version INTEGER NOT NULL DEFAULT 0,
    data_encryption_key BLOB NOT NULL,
    seq INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX idx_artifacts_account_id ON artifacts(account_id);
CREATE INDEX idx_artifacts_account_updated_at ON artifacts(account_id, updated_at);

-- Access keys
CREATE TABLE access_keys (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    machine_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    data TEXT NOT NULL,
    data_version INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (account_id, machine_id) REFERENCES machines(account_id, id) ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    UNIQUE(account_id, machine_id, session_id)
);

CREATE INDEX idx_access_keys_account_id ON access_keys(account_id);
CREATE INDEX idx_access_keys_session_id ON access_keys(session_id);
CREATE INDEX idx_access_keys_machine_id ON access_keys(machine_id);

-- Feed items
CREATE TABLE user_feed_items (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    counter INTEGER NOT NULL DEFAULT 0,
    repeat_key TEXT,
    body TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (user_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX idx_feed_user ON user_feed_items(user_id);
CREATE INDEX idx_feed_user_counter ON user_feed_items(user_id, counter);

-- Triggers to automatically update updated_at timestamps
CREATE TRIGGER update_accounts_updated_at
    AFTER UPDATE ON accounts
    FOR EACH ROW
BEGIN
    UPDATE accounts SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER update_sessions_updated_at
    AFTER UPDATE ON sessions
    FOR EACH ROW
BEGIN
    UPDATE sessions SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER update_session_messages_updated_at
    AFTER UPDATE ON session_messages
    FOR EACH ROW
BEGIN
    UPDATE session_messages SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER update_machines_updated_at
    AFTER UPDATE ON machines
    FOR EACH ROW
BEGIN
    UPDATE machines SET updated_at = CURRENT_TIMESTAMP WHERE account_id = NEW.account_id AND id = NEW.id;
END;

CREATE TRIGGER update_terminal_auth_updated_at
    AFTER UPDATE ON terminal_auth_requests
    FOR EACH ROW
BEGIN
    UPDATE terminal_auth_requests SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER update_account_auth_updated_at
    AFTER UPDATE ON account_auth_requests
    FOR EACH ROW
BEGIN
    UPDATE account_auth_requests SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER update_kv_updated_at
    AFTER UPDATE ON user_kv_store
    FOR EACH ROW
BEGIN
    UPDATE user_kv_store SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
