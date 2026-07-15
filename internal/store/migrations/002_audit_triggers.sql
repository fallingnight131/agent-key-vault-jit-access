CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE FUNCTION write_akv_audit_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb := to_jsonb(NEW);
    request_ref uuid;
    grant_ref uuid;
    execution_ref uuid;
    metadata_value jsonb := '{}'::jsonb;
BEGIN
    IF TG_OP = 'UPDATE' AND payload ? 'status' AND to_jsonb(OLD)->>'status' = payload->>'status' THEN
        RETURN NEW;
    END IF;
    IF payload ? 'status' THEN
        metadata_value := jsonb_build_object('status', payload->>'status');
    END IF;
    IF TG_TABLE_NAME = 'authorization_requests' THEN
        request_ref := NEW.id;
    ELSIF TG_TABLE_NAME = 'approvals' THEN
        request_ref := NEW.request_id;
    ELSIF TG_TABLE_NAME = 'operation_grants' THEN
        request_ref := NEW.request_id;
        grant_ref := NEW.id;
    ELSIF TG_TABLE_NAME = 'executions' THEN
        grant_ref := NEW.grant_id;
        execution_ref := NEW.id;
        SELECT request_id INTO request_ref FROM operation_grants WHERE id = grant_ref;
    ELSIF TG_TABLE_NAME = 'reclaims' THEN
        execution_ref := NEW.execution_id;
        SELECT g.id, g.request_id INTO grant_ref, request_ref
        FROM executions e JOIN operation_grants g ON g.id = e.grant_id
        WHERE e.id = execution_ref;
    END IF;
    INSERT INTO audit_events (
        id, event_type, actor_type, request_id, approval_id, grant_id,
        execution_id, reclaim_id, metadata
    ) VALUES (
        gen_random_uuid(), TG_TABLE_NAME || '.' || lower(TG_OP), 'SYSTEM',
        request_ref,
        CASE WHEN TG_TABLE_NAME = 'approvals' THEN NEW.id ELSE NULL END,
        grant_ref, execution_ref,
        CASE WHEN TG_TABLE_NAME = 'reclaims' THEN NEW.id ELSE NULL END,
        metadata_value
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_users AFTER INSERT OR UPDATE ON users FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_agents AFTER INSERT OR UPDATE ON agents FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_agent_tokens AFTER INSERT OR UPDATE ON agent_tokens FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_tasks AFTER INSERT OR UPDATE ON tasks FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_targets AFTER INSERT OR UPDATE ON targets FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_credentials AFTER INSERT OR UPDATE ON credentials FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_authorization_requests AFTER INSERT OR UPDATE ON authorization_requests FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_approvals AFTER INSERT ON approvals FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_operation_grants AFTER INSERT OR UPDATE ON operation_grants FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_executions AFTER INSERT OR UPDATE ON executions FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_reclaims AFTER INSERT OR UPDATE ON reclaims FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
CREATE TRIGGER audit_security_incidents AFTER INSERT OR UPDATE ON security_incidents FOR EACH ROW EXECUTE FUNCTION write_akv_audit_event();
