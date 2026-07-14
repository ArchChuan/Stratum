-- 访客账户：在 public.users 上标记临时账户及其过期时间。
-- 访客与 GitHub 用户共用默认租户、共享数据，仅通过 is_guest / expires_at 区分与回收。
ALTER TABLE public.users ADD COLUMN IF NOT EXISTS is_guest    BOOL        NOT NULL DEFAULT false;
ALTER TABLE public.users ADD COLUMN IF NOT EXISTS expires_at  TIMESTAMPTZ;

-- 回收器只扫描过期的访客，部分索引避免全表扫描。
CREATE INDEX IF NOT EXISTS idx_users_guest_expires
  ON public.users(expires_at)
  WHERE is_guest = true;
