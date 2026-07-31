-- 账号密码登录：username 用于登录凭证，password_hash 存储 bcrypt 哈希。
ALTER TABLE public.users ADD COLUMN IF NOT EXISTS username TEXT UNIQUE;
ALTER TABLE public.users ADD COLUMN IF NOT EXISTS password_hash TEXT;
