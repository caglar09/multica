ALTER TABLE deployment_operation DROP COLUMN IF EXISTS source_revision, DROP COLUMN IF EXISTS environment, DROP COLUMN IF EXISTS operation_kind, DROP COLUMN IF EXISTS approval_reference;
