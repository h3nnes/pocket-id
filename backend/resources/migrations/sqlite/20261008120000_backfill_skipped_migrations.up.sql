-- Backfills the effects of upstream migrations that were silently skipped on databases
-- that recorded the old PKCE migration timestamp (20260726153900) before upstream renamed it.
-- golang-migrate ignores any migration whose version is below the recorded database version,
-- so every migration added upstream with a timestamp below that value never ran there.
-- See 20260726153900_fix_renamed_pkce_migration and 20260726153901_oauth_apis_missing_tables for the earlier fixes in this series.
-- The oauth-apis tables (20260707170000) are already ensured by 20260726153901 and are not repeated here.
-- Every statement is idempotent: on databases where the original migrations ran, the guarded inserts copy zero rows and the rebuilds round-trip the data unchanged.
PRAGMA foreign_keys = OFF;
BEGIN;

-- 20260708130000_move_instance_id_to_kv was skipped: move the instance id into the kv table
-- The guard creates an empty table on databases where the config was already frozen, which turns the move into a no-op
CREATE TABLE IF NOT EXISTS "app_config_variables" ("key" TEXT NOT NULL PRIMARY KEY, "value" TEXT NOT NULL);
INSERT INTO kv ("key", "value") SELECT 'instance_id', "value" FROM app_config_variables WHERE "key" = 'instanceId' ON CONFLICT ("key") DO NOTHING;
DELETE FROM app_config_variables WHERE "key" = 'instanceId';

-- 20260718000000_freeze_config was skipped: freeze the remaining config rows into the kv table and drop the table
INSERT INTO kv SELECT 'config_migrated', json_group_object("key", "value") FROM app_config_variables HAVING count(*) > 0;
DROP TABLE app_config_variables;

-- 20260723000000_actor_tokens was skipped: freeze signup tokens into the kv table and drop the old tables
DROP TABLE IF EXISTS one_time_access_tokens;
CREATE TABLE IF NOT EXISTS signup_tokens (id TEXT NOT NULL PRIMARY KEY, created_at DATETIME NOT NULL, token TEXT NOT NULL UNIQUE, expires_at DATETIME NOT NULL, usage_limit INTEGER NOT NULL DEFAULT 1, usage_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS signup_tokens_user_groups (signup_token_id TEXT NOT NULL, user_group_id TEXT NOT NULL, PRIMARY KEY (signup_token_id, user_group_id), FOREIGN KEY (signup_token_id) REFERENCES signup_tokens (id) ON DELETE CASCADE, FOREIGN KEY (user_group_id) REFERENCES user_groups (id) ON DELETE CASCADE);
INSERT INTO kv SELECT 'signup_tokens_migrated', json_group_array(json_object('id', st.id, 'token', st.token, 'expiresAt', st.expires_at, 'usageLimit', st.usage_limit, 'usageCount', st.usage_count, 'createdAt', st.created_at, 'userGroupIds', json((SELECT COALESCE(json_group_array(stug.user_group_id), json_array()) FROM signup_tokens_user_groups stug WHERE stug.signup_token_id = st.id)))) FROM signup_tokens st HAVING count(*) > 0;
DROP TABLE signup_tokens_user_groups;
DROP TABLE signup_tokens;

-- 20260722120000_oauth_storage_export_normalization was skipped: align timestamp and JSON column types with the export format used for PostgreSQL
-- The rebuilds keep the column names identical, so they are idempotent on databases where the original migration already ran
-- oauth2_sessions is intentionally NOT rebuilt here: later migrations already moved it to a newer shape, and rebuilding it at the old shape would lose columns

CREATE TABLE reauthentication_tokens_new (
    id TEXT PRIMARY KEY,
    created_at DATETIME NOT NULL,
    token TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    user_id TEXT NOT NULL REFERENCES users ON DELETE CASCADE
);

INSERT INTO reauthentication_tokens_new (
    id,
    created_at,
    token,
    expires_at,
    user_id
)
SELECT
    id,
    created_at,
    token,
    expires_at,
    user_id
FROM reauthentication_tokens;

DROP TABLE reauthentication_tokens;
ALTER TABLE reauthentication_tokens_new RENAME TO reauthentication_tokens;

CREATE INDEX IF NOT EXISTS idx_reauthentication_tokens_token ON reauthentication_tokens (token);
CREATE INDEX IF NOT EXISTS idx_reauthentication_tokens_expires_at ON reauthentication_tokens (expires_at);

CREATE TABLE oauth2_jtis_new (
    id TEXT NOT NULL PRIMARY KEY,
    created_at DATETIME NOT NULL,
    jti TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL
);

INSERT INTO oauth2_jtis_new (
    id,
    created_at,
    jti,
    expires_at
)
SELECT
    id,
    created_at,
    jti,
    expires_at
FROM oauth2_jtis;

DROP TABLE oauth2_jtis;
ALTER TABLE oauth2_jtis_new RENAME TO oauth2_jtis;

CREATE INDEX IF NOT EXISTS idx_oauth2_jtis_expires_at ON oauth2_jtis (expires_at);

CREATE TABLE interaction_sessions_new (
    id TEXT NOT NULL PRIMARY KEY,
    created_at DATETIME NOT NULL,
    consent_required BOOLEAN NOT NULL DEFAULT FALSE,
    reauthentication_required BOOLEAN NOT NULL DEFAULT FALSE,
    authentication_required BOOLEAN NOT NULL DEFAULT FALSE,
    account_selection_required BOOLEAN NOT NULL DEFAULT FALSE,
    scopes BLOB NOT NULL DEFAULT X'5B5D',
    client_id TEXT NOT NULL REFERENCES oidc_clients(id) ON DELETE CASCADE,
    user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    requested_at DATETIME NOT NULL,
    reauthenticated_at DATETIME,
    parameters BLOB NOT NULL DEFAULT X'7B7D'
);

INSERT INTO interaction_sessions_new (
    id,
    created_at,
    consent_required,
    reauthentication_required,
    authentication_required,
    account_selection_required,
    scopes,
    client_id,
    user_id,
    requested_at,
    reauthenticated_at,
    parameters
)
SELECT
    id,
    created_at,
    consent_required,
    reauthentication_required,
    authentication_required,
    account_selection_required,
    CAST(scopes AS BLOB),
    client_id,
    user_id,
    requested_at,
    reauthenticated_at,
    CAST(parameters AS BLOB)
FROM interaction_sessions;

DROP TABLE interaction_sessions;
ALTER TABLE interaction_sessions_new RENAME TO interaction_sessions;

CREATE INDEX IF NOT EXISTS idx_interaction_sessions_client_id ON interaction_sessions (client_id);
CREATE INDEX IF NOT EXISTS idx_interaction_sessions_user_id ON interaction_sessions (user_id);

COMMIT;
PRAGMA foreign_keys = ON;
