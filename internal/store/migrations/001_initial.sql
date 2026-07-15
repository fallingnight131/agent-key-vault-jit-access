CREATE TABLE users (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    is_admin boolean NOT NULL DEFAULT false,
    approve_all boolean NOT NULL DEFAULT false,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_single_admin ON users ((is_admin)) WHERE is_admin;

CREATE TABLE web_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id, name)
);

CREATE TABLE agent_tokens (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id),
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX agent_tokens_one_unrevoked ON agent_tokens (agent_id) WHERE revoked_at IS NULL;

CREATE TABLE tasks (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'COMPLETED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'AGENT_LOST')),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    CHECK ((status = 'ACTIVE' AND ended_at IS NULL) OR (status <> 'ACTIVE' AND ended_at IS NOT NULL))
);

CREATE TABLE targets (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    connector_type text NOT NULL CHECK (connector_type IN ('HTTP', 'POSTGRESQL')),
    connection_config jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE credentials (
    id uuid PRIMARY KEY,
    target_id uuid NOT NULL REFERENCES targets(id),
    alias text NOT NULL,
    credential_type text NOT NULL CHECK (credential_type IN ('API_KEY', 'ACCESS_TOKEN', 'USERNAME_PASSWORD', 'CERTIFICATE', 'TRANSIT_KEY', 'POSTGRESQL_DYNAMIC')),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'DISABLED')),
    vault_provider text NOT NULL CHECK (vault_provider = 'OPENBAO'),
    vault_path text NOT NULL,
    vault_version integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (target_id, alias)
);

ALTER TABLE targets ADD COLUMN default_credential_id uuid REFERENCES credentials(id);

CREATE TABLE authorization_requests (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id),
    task_id uuid NOT NULL REFERENCES tasks(id),
    target_id uuid NOT NULL REFERENCES targets(id),
    credential_id uuid NOT NULL REFERENCES credentials(id),
    operation text NOT NULL,
    parameters jsonb NOT NULL,
    operation_hash bytea NOT NULL,
    reason text NOT NULL CHECK (length(trim(reason)) > 0),
    status text NOT NULL CHECK (status IN ('PENDING_APPROVAL', 'REJECTED', 'APPROVAL_EXPIRED', 'APPROVED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    approval_deadline timestamptz NOT NULL,
    CHECK (approval_deadline > created_at)
);

CREATE TABLE approvals (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL UNIQUE REFERENCES authorization_requests(id),
    approver_user_id uuid NOT NULL REFERENCES users(id),
    decision text NOT NULL CHECK (decision IN ('APPROVED', 'REJECTED')),
    decided_at timestamptz NOT NULL DEFAULT now(),
    grant_expires_at timestamptz,
    CHECK ((decision = 'APPROVED' AND grant_expires_at IS NOT NULL) OR (decision = 'REJECTED' AND grant_expires_at IS NULL))
);

CREATE TABLE operation_grants (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL UNIQUE REFERENCES authorization_requests(id),
    agent_id uuid NOT NULL REFERENCES agents(id),
    task_id uuid NOT NULL REFERENCES tasks(id),
    target_id uuid NOT NULL REFERENCES targets(id),
    credential_id uuid NOT NULL REFERENCES credentials(id),
    operation_hash bytea NOT NULL,
    approved_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('APPROVED', 'REVOKED', 'GRANT_EXPIRED', 'EXECUTING', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT', 'RECLAIMING', 'RECLAIMED', 'RECLAIM_FAILED')),
    claimed_at timestamptz,
    completed_at timestamptz,
    revoked_at timestamptz,
    CHECK (expires_at > approved_at),
    CHECK (status = 'APPROVED' OR claimed_at IS NOT NULL OR status IN ('REVOKED', 'GRANT_EXPIRED'))
);

CREATE TABLE executions (
    id uuid PRIMARY KEY,
    grant_id uuid NOT NULL UNIQUE REFERENCES operation_grants(id),
    status text NOT NULL CHECK (status IN ('EXECUTING', 'SUCCEEDED', 'FAILED', 'CANCELLED', 'TIMED_OUT')),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    response_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_code text
);

CREATE TABLE reclaims (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions(id),
    status text NOT NULL CHECK (status IN ('RECLAIMING', 'RECLAIMED', 'RECLAIM_FAILED')),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    attempt integer NOT NULL CHECK (attempt > 0),
    error_code text,
    UNIQUE (execution_id, attempt)
);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    event_type text NOT NULL,
    actor_type text NOT NULL,
    actor_id uuid,
    request_id uuid REFERENCES authorization_requests(id),
    approval_id uuid REFERENCES approvals(id),
    grant_id uuid REFERENCES operation_grants(id),
    execution_id uuid REFERENCES executions(id),
    reclaim_id uuid REFERENCES reclaims(id),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_created_at ON audit_events (created_at);
CREATE INDEX audit_events_request_id ON audit_events (request_id);

CREATE TABLE security_incidents (
    id uuid PRIMARY KEY,
    reclaim_id uuid REFERENCES reclaims(id),
    status text NOT NULL CHECK (status IN ('OPEN', 'RESOLVED')),
    error_code text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);

CREATE FUNCTION reject_request_snapshot_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.agent_id, OLD.task_id, OLD.target_id, OLD.credential_id, OLD.operation,
        OLD.parameters, OLD.operation_hash, OLD.reason, OLD.created_at, OLD.approval_deadline)
       IS DISTINCT FROM
       (NEW.agent_id, NEW.task_id, NEW.target_id, NEW.credential_id, NEW.operation,
        NEW.parameters, NEW.operation_hash, NEW.reason, NEW.created_at, NEW.approval_deadline) THEN
        RAISE EXCEPTION 'authorization request snapshot is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER authorization_requests_immutable_snapshot
BEFORE UPDATE ON authorization_requests
FOR EACH ROW EXECUTE FUNCTION reject_request_snapshot_update();
