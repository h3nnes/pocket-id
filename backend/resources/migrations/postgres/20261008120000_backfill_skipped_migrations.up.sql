-- Backfills the effects of upstream migrations that were silently skipped on databases
-- that recorded the old PKCE migration timestamp (20260726153900) before upstream renamed it.
-- golang-migrate ignores any migration whose version is below the recorded database version,
-- so every migration added upstream with a timestamp below that value never ran there.
-- See 20260726153900_fix_renamed_pkce_migration and 20260726153901_oauth_apis_missing_tables for the earlier fixes in this series.
-- The oauth-apis tables (20260707170000) are already ensured by 20260726153901 and are not repeated here.

-- 20260708130000_move_instance_id_to_kv and 20260718000000_freeze_config were skipped
-- The existence guard makes both backfills no-ops on databases where the config table was already frozen and dropped
DO $$
BEGIN
    IF to_regclass('app_config_variables') IS NOT NULL THEN
        INSERT INTO kv ("key", "value")
        SELECT 'instance_id', "value" FROM app_config_variables WHERE "key" = 'instanceId'
        ON CONFLICT ("key") DO NOTHING;

        DELETE FROM app_config_variables WHERE "key" = 'instanceId';

        INSERT INTO kv ("key", "value")
        SELECT 'config_migrated', json_object_agg("key", "value")::text
        FROM app_config_variables
        HAVING count(*) > 0;

        DROP TABLE app_config_variables;
    END IF;
END
$$;

-- 20260723000000_actor_tokens was skipped: freeze signup tokens into the kv table and drop the old tables
DROP TABLE IF EXISTS one_time_access_tokens;

DO $$
BEGIN
    IF to_regclass('signup_tokens') IS NOT NULL THEN
        INSERT INTO kv ("key", "value")
        SELECT 'signup_tokens_migrated', json_agg(json_build_object(
            'id', st.id,
            'token', st.token,
            'expiresAt', extract(epoch FROM st.expires_at)::bigint,
            'usageLimit', st.usage_limit,
            'usageCount', st.usage_count,
            'createdAt', extract(epoch FROM st.created_at)::bigint,
            'userGroupIds', COALESCE(
                (SELECT json_agg(stug.user_group_id) FROM signup_tokens_user_groups stug WHERE stug.signup_token_id = st.id),
                '[]'::json
            )
        ))::text
        FROM signup_tokens st
        HAVING count(*) > 0;

        DROP TABLE signup_tokens_user_groups;
        DROP TABLE signup_tokens;
    END IF;
END
$$;

-- 20260722120000_oauth_storage_export_normalization needs no backfill on PostgreSQL
-- The original migration only realigns SQLite column types, which already match on PostgreSQL

-- 20260708120000_add_oidc_client_description was skipped: add the missing column
ALTER TABLE oidc_clients ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
