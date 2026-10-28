CREATE OR REPLACE FUNCTION reject_autonomous_project_release_manifest_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'autonomous project release manifests are immutable';
END;
$$;
