ALTER TABLE targets
    ADD COLUMN config_version integer NOT NULL DEFAULT 1 CHECK (config_version > 0);

CREATE TABLE operation_sets (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (length(trim(name)) > 0),
    description text NOT NULL DEFAULT '',
    executor_type text NOT NULL CHECK (executor_type IN ('HTTP', 'POSTGRESQL', 'SIGN')),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_by_user_id uuid NOT NULL REFERENCES users(id),
    updated_by_user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE operations (
    id uuid PRIMARY KEY,
    operation_set_id uuid NOT NULL REFERENCES operation_sets(id),
    operation_key text NOT NULL CHECK (operation_key ~ '^[a-z][a-z0-9_]{0,63}$'),
    current_version integer CHECK (current_version > 0),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_by_user_id uuid NOT NULL REFERENCES users(id),
    updated_by_user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (operation_set_id, operation_key)
);

CREATE TABLE operation_versions (
    operation_id uuid NOT NULL REFERENCES operations(id),
    version integer NOT NULL CHECK (version > 0),
    name text NOT NULL CHECK (length(trim(name)) > 0),
    description text NOT NULL DEFAULT '',
    operation_kind text NOT NULL CHECK (operation_kind IN ('HTTP', 'POSTGRESQL_STATEMENT', 'POSTGRESQL_TRANSACTION', 'SIGN')),
    risk_level text NOT NULL CHECK (risk_level IN ('LOW', 'MEDIUM', 'HIGH')),
    arguments_schema jsonb NOT NULL,
    execution_template jsonb NOT NULL,
    definition_hash bytea NOT NULL CHECK (octet_length(definition_hash) = 32),
    created_by_user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operation_id, version)
);

ALTER TABLE operations
    ADD CONSTRAINT operations_current_version_fk
    FOREIGN KEY (id, current_version)
    REFERENCES operation_versions(operation_id, version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE target_operation_bindings (
    target_id uuid NOT NULL REFERENCES targets(id),
    operation_id uuid NOT NULL REFERENCES operations(id),
    version integer NOT NULL CHECK (version > 0),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by_user_id uuid NOT NULL REFERENCES users(id),
    updated_by_user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (target_id, operation_id),
    FOREIGN KEY (operation_id, version)
        REFERENCES operation_versions(operation_id, version)
);

CREATE INDEX target_operation_bindings_discovery
    ON target_operation_bindings (target_id, status, operation_id);

ALTER TABLE authorization_requests
    ADD COLUMN request_format smallint NOT NULL DEFAULT 1 CHECK (request_format IN (1, 2)),
    ADD COLUMN operation_id uuid REFERENCES operations(id),
    ADD COLUMN operation_version integer,
    ADD COLUMN arguments jsonb,
    ADD COLUMN definition_hash bytea,
    ADD COLUMN target_config_version integer;

ALTER TABLE authorization_requests
    ADD CONSTRAINT authorization_requests_operation_version_fk
        FOREIGN KEY (operation_id, operation_version)
        REFERENCES operation_versions(operation_id, version),
    ADD CONSTRAINT authorization_requests_safe_operation_shape CHECK (
        (request_format = 1
            AND operation_id IS NULL
            AND operation_version IS NULL
            AND arguments IS NULL
            AND definition_hash IS NULL
            AND target_config_version IS NULL)
        OR
        (request_format = 2
            AND operation_id IS NOT NULL
            AND operation_version IS NOT NULL
            AND operation_version > 0
            AND arguments IS NOT NULL
            AND definition_hash IS NOT NULL
            AND octet_length(definition_hash) = 32
            AND target_config_version IS NOT NULL
            AND target_config_version > 0)
    );

-- The safe-operation release deliberately fails closed for requests created
-- by the removed raw-operation API. Historical terminal rows remain readable
-- for audit, but no legacy request may be newly approved or executed.
UPDATE authorization_requests
SET status = 'APPROVAL_EXPIRED'
WHERE request_format = 1 AND status = 'PENDING_APPROVAL';

UPDATE operation_grants grant_record
SET status = 'REVOKED', revoked_at = now()
FROM authorization_requests request_record
WHERE grant_record.request_id = request_record.id
  AND request_record.request_format = 1
  AND grant_record.status = 'APPROVED';

UPDATE operation_grants grant_record
SET revoked_at = COALESCE(grant_record.revoked_at, now())
FROM authorization_requests request_record
WHERE grant_record.request_id = request_record.id
  AND request_record.request_format = 1
  AND grant_record.status = 'EXECUTING';

ALTER TABLE authorization_requests
    ALTER COLUMN request_format DROP DEFAULT;

CREATE OR REPLACE FUNCTION reject_request_snapshot_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.agent_id, OLD.task_id, OLD.target_id, OLD.credential_id, OLD.operation,
        OLD.parameters, OLD.operation_hash, OLD.reason, OLD.created_at, OLD.approval_deadline,
        OLD.request_format, OLD.operation_id, OLD.operation_version, OLD.arguments,
        OLD.definition_hash, OLD.target_config_version)
       IS DISTINCT FROM
       (NEW.agent_id, NEW.task_id, NEW.target_id, NEW.credential_id, NEW.operation,
        NEW.parameters, NEW.operation_hash, NEW.reason, NEW.created_at, NEW.approval_deadline,
        NEW.request_format, NEW.operation_id, NEW.operation_version, NEW.arguments,
        NEW.definition_hash, NEW.target_config_version) THEN
        RAISE EXCEPTION 'authorization request snapshot is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION reject_operation_version_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'published operation versions are immutable';
END;
$$;

CREATE TRIGGER operation_versions_immutable
BEFORE UPDATE OR DELETE ON operation_versions
FOR EACH ROW EXECUTE FUNCTION reject_operation_version_mutation();

CREATE FUNCTION write_operation_catalog_audit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb := to_jsonb(NEW);
    actor_id_value uuid;
    record_id_value text;
    metadata_value jsonb;
BEGIN
    actor_id_value := COALESCE(
        NULLIF(payload->>'updated_by_user_id', '')::uuid,
        NULLIF(payload->>'created_by_user_id', '')::uuid
    );
    record_id_value := COALESCE(payload->>'id', payload->>'operation_id', payload->>'target_id');
    metadata_value := jsonb_build_object(
        'table', TG_TABLE_NAME,
        'record_id', COALESCE(record_id_value, ''),
        'status', COALESCE(payload->>'status', ''),
        'version', COALESCE(payload->>'version', payload->>'current_version', '')
    );
    INSERT INTO audit_events (id, event_type, actor_type, actor_id, metadata)
    VALUES (gen_random_uuid(), 'operation_catalog.' || TG_TABLE_NAME || '.' || lower(TG_OP), 'USER', actor_id_value, metadata_value);
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_operation_sets
AFTER INSERT OR UPDATE ON operation_sets
FOR EACH ROW EXECUTE FUNCTION write_operation_catalog_audit();

CREATE TRIGGER audit_operations
AFTER INSERT OR UPDATE ON operations
FOR EACH ROW EXECUTE FUNCTION write_operation_catalog_audit();

CREATE TRIGGER audit_operation_versions
AFTER INSERT ON operation_versions
FOR EACH ROW EXECUTE FUNCTION write_operation_catalog_audit();

CREATE TRIGGER audit_target_operation_bindings
AFTER INSERT OR UPDATE ON target_operation_bindings
FOR EACH ROW EXECUTE FUNCTION write_operation_catalog_audit();
