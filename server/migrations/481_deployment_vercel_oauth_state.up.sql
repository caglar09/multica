CREATE TABLE IF NOT EXISTS deployment_vercel_oauth_state (
    state_hash TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    user_id UUID NOT NULL,
    code_verifier_ciphertext TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
