-- 028_guest_fk_cascade.up.sql
-- Add ON DELETE clauses to FKs referencing users(id) that currently lack them,
-- so guest reaper DeleteUser does not fail with FK violations.
--
-- tenant_invitations.invited_by → CASCADE:
--   When the inviter is deleted, their invitations are meaningless and should be removed.
ALTER TABLE public.tenant_invitations
    DROP CONSTRAINT IF EXISTS tenant_invitations_invited_by_fkey,
    ADD CONSTRAINT tenant_invitations_invited_by_fkey
        FOREIGN KEY (invited_by) REFERENCES public.users(id) ON DELETE CASCADE;

-- tenant_invitations.consumed_by → SET NULL:
--   Keep the invitation record for audit but null the consumer reference.
ALTER TABLE public.tenant_invitations
    DROP CONSTRAINT IF EXISTS tenant_invitations_consumed_by_fkey,
    ADD CONSTRAINT tenant_invitations_consumed_by_fkey
        FOREIGN KEY (consumed_by) REFERENCES public.users(id) ON DELETE SET NULL;

-- tenant_members.invited_by → SET NULL:
--   Membership persists (owner or other members still exist); null the inviter reference.
ALTER TABLE public.tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_invited_by_fkey,
    ADD CONSTRAINT tenant_members_invited_by_fkey
        FOREIGN KEY (invited_by) REFERENCES public.users(id) ON DELETE SET NULL;
