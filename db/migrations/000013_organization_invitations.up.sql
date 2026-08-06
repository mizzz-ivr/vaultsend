CREATE TYPE organization_invitation_status AS ENUM ('pending', 'accepted', 'revoked');

CREATE TABLE organization_invitations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email varchar(320) NOT NULL,
    email_normalized varchar(320) NOT NULL,
    role organization_role NOT NULL,
    token_hash char(64) NOT NULL UNIQUE,
    status organization_invitation_status NOT NULL DEFAULT 'pending',
    invited_by_user_id uuid NOT NULL REFERENCES users(id),
    accepted_by_user_id uuid NULL REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    last_sent_at timestamptz NULL,
    accepted_at timestamptz NULL,
    revoked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organization_invitations_role_check CHECK (role IN ('admin', 'member')),
    CONSTRAINT organization_invitations_acceptance_check CHECK (
        (status = 'accepted' AND accepted_by_user_id IS NOT NULL AND accepted_at IS NOT NULL)
        OR (status <> 'accepted' AND accepted_by_user_id IS NULL AND accepted_at IS NULL)
    )
);

CREATE UNIQUE INDEX uq_organization_invitations_pending_email
    ON organization_invitations (organization_id, email_normalized)
    WHERE status = 'pending';

CREATE INDEX idx_organization_invitations_org_created
    ON organization_invitations (organization_id, created_at DESC);

CREATE INDEX idx_organization_invitations_expires
    ON organization_invitations (expires_at)
    WHERE status = 'pending';

CREATE TRIGGER trg_organization_invitations_set_updated_at
    BEFORE UPDATE ON organization_invitations
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
