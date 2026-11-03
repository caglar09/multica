CREATE OR REPLACE FUNCTION autonomous_project_change_request_history()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    approval_actor_type TEXT := current_setting('multica.approval_actor_type', true);
    approval_actor_ref TEXT := current_setting('multica.approval_actor_ref', true);
    event_actor_type TEXT;
    event_actor_ref TEXT;
BEGIN
    event_actor_type := CASE WHEN approval_actor_type = 'human' THEN 'human' ELSE 'system' END;
    event_actor_ref := CASE WHEN event_actor_type = 'human' THEN NULLIF(approval_actor_ref, '') ELSE NULL END;

    IF TG_OP = 'INSERT' THEN
        INSERT INTO autonomous_project_change_request_event
            (change_request_id, from_state, to_state, actor_type, actor_ref, details)
        VALUES (
            NEW.id, NULL, NEW.state, event_actor_type, event_actor_ref,
            jsonb_build_object('source', NEW.source, 'request_type', NEW.request_type)
        );
    ELSIF NEW.state IS DISTINCT FROM OLD.state THEN
        INSERT INTO autonomous_project_change_request_event
            (change_request_id, from_state, to_state, actor_type, actor_ref, details)
        VALUES (NEW.id, OLD.state, NEW.state, event_actor_type, event_actor_ref, '{}'::jsonb);
    END IF;
    RETURN NEW;
END;
$$;
