CREATE TABLE IF NOT EXISTS public.oauth_exchange_codes (
  code_hash          TEXT        PRIMARY KEY,
  payload_ciphertext TEXT        NOT NULL,
  expires_at         TIMESTAMPTZ NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_oauth_exchange_expires
  ON public.oauth_exchange_codes(expires_at);
