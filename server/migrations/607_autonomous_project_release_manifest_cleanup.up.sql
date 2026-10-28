CREATE OR REPLACE FUNCTION reject_autonomous_project_release_manifest_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       AND current_setting('multica.allow_release_manifest_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'autonomous project release manifests are immutable';
END;
$$;
