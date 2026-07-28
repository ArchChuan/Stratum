DROP TABLE IF EXISTS public.tenant_invitations;
ALTER TABLE public.users DROP COLUMN IF EXISTS email_verified_at;
