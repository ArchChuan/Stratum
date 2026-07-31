-- 028_guest_fk_cascade.down.sql
-- Restore original FK constraints without ON DELETE clauses.

ALTER TABLE public.tenant_invitations
    DROP CONSTRAINT IF EXISTS tenant_invitations_invited_by_fkey,
    ADD CONSTRAINT tenant_invitations_invited_by_fkey
        FOREIGN KEY (invited_by) REFERENCES public.users(id);

ALTER TABLE public.tenant_invitations
    DROP CONSTRAINT IF EXISTS tenant_invitations_consumed_by_fkey,
    ADD CONSTRAINT tenant_invitations_consumed_by_fkey
        FOREIGN KEY (consumed_by) REFERENCES public.users(id);

ALTER TABLE public.tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_invited_by_fkey,
    ADD CONSTRAINT tenant_members_invited_by_fkey
        FOREIGN KEY (invited_by) REFERENCES public.users(id);
