INSERT INTO organization_members (organization_id, user_id, role)
SELECT id, owner_user_id, 'owner'
FROM organizations
ON CONFLICT (organization_id, user_id)
DO UPDATE SET role = 'owner';

UPDATE organization_members AS om
SET role = 'admin'
FROM organizations AS o
WHERE om.organization_id = o.id
  AND om.role = 'owner'
  AND om.user_id <> o.owner_user_id;

CREATE UNIQUE INDEX organization_members_single_owner_idx
    ON organization_members (organization_id)
    WHERE role = 'owner';
