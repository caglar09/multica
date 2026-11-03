CREATE TABLE IF NOT EXISTS autonomous_project_approval_event (
    id               UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL,
    project_id       UUID NOT NULL,
    idempotency_key  TEXT NOT NULL CHECK (btrim(idempotency_key) <> ''),
    subject_type     TEXT NOT NULL CHECK (subject_type IN (
        'specification_revision', 'plan', 'team_draft', 'planning_start',
        'change_request', 'escalation', 'release_manifest', 'import_adoption',
        'manual_issue'
    )),
    subject_id       TEXT NOT NULL CHECK (btrim(subject_id) <> ''),
    subject_revision BIGINT,
    subject_digest   TEXT NOT NULL CHECK (subject_digest ~ '^[0-9a-f]{64}$'),
    decision         TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    actor_type       TEXT NOT NULL CHECK (actor_type IN ('human', 'system')),
    actor_id         UUID,
    actor_display    TEXT NOT NULL CHECK (btrim(actor_display) <> ''),
    policy_ref       TEXT,
    rationale        TEXT NOT NULL DEFAULT '',
    source_endpoint  TEXT NOT NULL CHECK (btrim(source_endpoint) <> ''),
    origin           TEXT NOT NULL DEFAULT 'local' CHECK (origin IN ('local', 'imported')),
    origin_ref       JSONB NOT NULL DEFAULT '{}'::jsonb,
    acted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (actor_type = 'human' AND (
            (origin = 'local' AND actor_id IS NOT NULL)
            OR (origin = 'imported' AND actor_id IS NULL)
        ))
        OR (actor_type = 'system' AND actor_id IS NULL AND NULLIF(btrim(policy_ref), '') IS NOT NULL)
    ),
    CHECK (origin = 'imported' OR btrim(rationale) <> '')
);

CREATE OR REPLACE FUNCTION autonomous_project_approval_event_guard()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'TRUNCATE'
       OR TG_OP = 'UPDATE'
       OR current_setting('multica.approval_purge', true) IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION 'autonomous project approval events are append-only'
            USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER autonomous_project_approval_event_append_only
BEFORE UPDATE OR DELETE ON autonomous_project_approval_event
FOR EACH ROW EXECUTE FUNCTION autonomous_project_approval_event_guard();

CREATE TRIGGER autonomous_project_approval_event_no_truncate
BEFORE TRUNCATE ON autonomous_project_approval_event
FOR EACH STATEMENT EXECUTE FUNCTION autonomous_project_approval_event_guard();
