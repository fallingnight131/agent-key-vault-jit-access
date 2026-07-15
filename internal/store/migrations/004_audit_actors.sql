CREATE OR REPLACE FUNCTION write_akv_audit_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb := to_jsonb(NEW);
    request_ref uuid;
    grant_ref uuid;
    execution_ref uuid;
    actor_type_value text := 'SYSTEM';
    actor_id_value uuid;
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
        actor_type_value := 'AGENT';
        actor_id_value := NEW.agent_id;
    ELSIF TG_TABLE_NAME = 'approvals' THEN
        request_ref := NEW.request_id;
        actor_type_value := 'USER';
        actor_id_value := NEW.approver_user_id;
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
        id, event_type, actor_type, actor_id, request_id, approval_id, grant_id,
        execution_id, reclaim_id, metadata
    ) VALUES (
        gen_random_uuid(), TG_TABLE_NAME || '.' || lower(TG_OP), actor_type_value,
        actor_id_value, request_ref,
        CASE WHEN TG_TABLE_NAME = 'approvals' THEN NEW.id ELSE NULL END,
        grant_ref, execution_ref,
        CASE WHEN TG_TABLE_NAME = 'reclaims' THEN NEW.id ELSE NULL END,
        metadata_value
    );
    RETURN NEW;
END;
$$;
