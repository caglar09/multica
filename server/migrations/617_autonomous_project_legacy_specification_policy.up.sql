INSERT INTO autonomous_project_approval_event (
    workspace_id,
    project_id,
    idempotency_key,
    subject_type,
    subject_id,
    subject_revision,
    subject_digest,
    decision,
    actor_type,
    actor_id,
    actor_display,
    policy_ref,
    rationale,
    source_endpoint,
    origin,
    origin_ref
)
SELECT
    r.workspace_id,
    r.project_id,
    'legacy-unapproved-spec-v1:' || r.id::text || ':' || r.revision::text,
    'specification_revision',
    r.id::text,
    r.revision,
    autonomous_project_specification_digest(r.id),
    'rejected',
    'system',
    NULL::uuid,
    'Legacy policy backfill',
    'legacy_unapproved',
    'Existing active derived specification requires local human approval.',
    'migration:617_autonomous_project_legacy_specification_policy',
    'local',
    jsonb_build_object('policy_ref', 'legacy_unapproved')
FROM autonomous_project_specification_revision r
JOIN autonomous_project_specification_head h
  ON h.workspace_id = r.workspace_id
 AND h.project_id = r.project_id
 AND h.specification_revision_id = r.id
 AND h.revision = r.revision
WHERE r.source_kind = 'project_bootstrap'
  AND EXISTS (
      SELECT 1
      FROM autonomous_project_plan p
      WHERE p.workspace_id = r.workspace_id
        AND p.project_id = r.project_id
        AND p.specification_revision_id = r.id
        AND p.status IN ('active', 'blocked')
  )
  AND NOT autonomous_project_specification_approved(r.id)
  AND NOT EXISTS (
      SELECT 1
      FROM autonomous_project_approval_event e
      WHERE e.workspace_id = r.workspace_id
        AND e.project_id = r.project_id
        AND e.idempotency_key = 'legacy-unapproved-spec-v1:' || r.id::text || ':' || r.revision::text
  );
