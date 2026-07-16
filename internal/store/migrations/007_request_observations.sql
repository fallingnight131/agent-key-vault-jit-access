CREATE TABLE request_observation_capture (
    request_id uuid PRIMARY KEY REFERENCES authorization_requests(id),
    started_at timestamptz NOT NULL
);

CREATE TABLE request_observation_events (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES request_observation_capture(request_id),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    event_type text NOT NULL CHECK (event_type IN (
        'MANUAL_HANDOFF',
        'APPROVAL_FOLLOWUP',
        'REVIEW_STARTED',
        'REVIEW_COMPLETED'
    )),
    review_session_id uuid,
    idempotency_key uuid NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (event_type IN ('MANUAL_HANDOFF', 'APPROVAL_FOLLOWUP')
            AND review_session_id IS NULL)
        OR
        (event_type = 'REVIEW_STARTED'
            AND review_session_id = id)
        OR
        (event_type = 'REVIEW_COMPLETED'
            AND review_session_id IS NOT NULL)
    ),
    UNIQUE (request_id, actor_user_id, idempotency_key)
);

CREATE INDEX request_observation_events_request
    ON request_observation_events (request_id, occurred_at, id);

CREATE UNIQUE INDEX request_observation_review_start_once
    ON request_observation_events (request_id)
    WHERE event_type = 'REVIEW_STARTED';

CREATE UNIQUE INDEX request_observation_review_completion_once
    ON request_observation_events (review_session_id)
    WHERE event_type = 'REVIEW_COMPLETED';

CREATE FUNCTION enroll_request_observation_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO request_observation_capture (request_id, started_at)
    VALUES (NEW.id, NEW.created_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER authorization_requests_observation_enrollment
AFTER INSERT ON authorization_requests
FOR EACH ROW EXECUTE FUNCTION enroll_request_observation_capture();

CREATE FUNCTION validate_request_observation_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    review_start_request_id uuid;
    review_start_actor_user_id uuid;
    review_started_at timestamptz;
BEGIN
    -- Observation time is always assigned by PostgreSQL. Callers cannot submit
    -- historical or future timestamps that would distort pilot measurements.
    NEW.occurred_at := clock_timestamp();

    IF NEW.event_type IN ('MANUAL_HANDOFF', 'APPROVAL_FOLLOWUP') THEN
        PERFORM 1
        FROM authorization_requests request_record
        WHERE request_record.id = NEW.request_id
          AND request_record.status = 'PENDING_APPROVAL'
          AND request_record.approval_deadline > NEW.occurred_at;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'observation is unavailable in the current request state'
                USING ERRCODE = '23514';
        END IF;
    ELSIF NEW.event_type = 'REVIEW_STARTED' THEN
        PERFORM 1
        FROM operation_grants grant_record
        JOIN executions execution_record ON execution_record.grant_id = grant_record.id
        WHERE grant_record.request_id = NEW.request_id
          AND execution_record.completed_at IS NOT NULL
          AND execution_record.completed_at >= (
              SELECT created_at
              FROM authorization_requests
              WHERE id = NEW.request_id
          );
        IF NOT FOUND THEN
            RAISE EXCEPTION 'review requires a completed execution result'
                USING ERRCODE = '23514';
        END IF;
    ELSIF NEW.event_type = 'REVIEW_COMPLETED' THEN
        SELECT start_event.request_id, start_event.actor_user_id, start_event.occurred_at
        INTO review_start_request_id, review_start_actor_user_id, review_started_at
        FROM request_observation_events start_event
        WHERE start_event.id = NEW.review_session_id
          AND start_event.event_type = 'REVIEW_STARTED';

        IF NOT FOUND
           OR review_start_request_id IS DISTINCT FROM NEW.request_id
           OR review_start_actor_user_id IS DISTINCT FROM NEW.actor_user_id
           OR NEW.occurred_at < review_started_at THEN
            RAISE EXCEPTION 'review completion does not match its start event'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER request_observation_events_validate
BEFORE INSERT ON request_observation_events
FOR EACH ROW EXECUTE FUNCTION validate_request_observation_event();

CREATE FUNCTION reject_request_observation_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'request observations are append-only' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER request_observation_capture_append_only
BEFORE UPDATE OR DELETE ON request_observation_capture
FOR EACH ROW EXECUTE FUNCTION reject_request_observation_mutation();

CREATE TRIGGER request_observation_events_append_only
BEFORE UPDATE OR DELETE ON request_observation_events
FOR EACH ROW EXECUTE FUNCTION reject_request_observation_mutation();
