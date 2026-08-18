ALTER TABLE public.models DROP COLUMN IF EXISTS sampling_params;
ALTER TABLE public.models DROP COLUMN IF EXISTS max_temperature;
ALTER TABLE public.providers DROP COLUMN IF EXISTS extra_headers;
ALTER TABLE public.providers DROP COLUMN IF EXISTS default_sampling;
