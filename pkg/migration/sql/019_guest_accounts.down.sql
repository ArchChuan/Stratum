DROP INDEX IF EXISTS public.idx_users_guest_expires;
ALTER TABLE public.users DROP COLUMN IF EXISTS expires_at;
ALTER TABLE public.users DROP COLUMN IF EXISTS is_guest;
