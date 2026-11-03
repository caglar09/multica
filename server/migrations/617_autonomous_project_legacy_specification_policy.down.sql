BEGIN;
SET LOCAL multica.approval_purge = 'on';

DELETE FROM autonomous_project_approval_event
WHERE policy_ref = 'legacy_unapproved'
  AND source_endpoint = 'migration:617_autonomous_project_legacy_specification_policy';

COMMIT;
