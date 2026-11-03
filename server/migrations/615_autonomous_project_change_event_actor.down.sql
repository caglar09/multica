CREATE OR REPLACE FUNCTION autonomous_project_change_request_history()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO autonomous_project_change_request_event(change_request_id, from_state, to_state, details)
        VALUES (NEW.id, NULL, NEW.state, jsonb_build_object('source', NEW.source, 'request_type', NEW.request_type));
    ELSIF NEW.state IS DISTINCT FROM OLD.state THEN
        INSERT INTO autonomous_project_change_request_event(change_request_id, from_state, to_state, details)
        VALUES (NEW.id, OLD.state, NEW.state, '{}'::jsonb);
    END IF;
    RETURN NEW;
END;
$$;
