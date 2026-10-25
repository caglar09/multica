ALTER TABLE deployment_provider_connection
    ADD COLUMN IF NOT EXISTS webhook_secret_ref TEXT;
