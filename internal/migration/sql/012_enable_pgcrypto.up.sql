-- gen_random_bytes() lives in pgcrypto; install if absent then rebuild gen_uuid_v7().
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Recreate after extension install so the symbol resolves at call time.
CREATE OR REPLACE FUNCTION public.gen_uuid_v7() RETURNS uuid AS $$
DECLARE
    v_time BIGINT := (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT;
    v_rand BYTEA  := gen_random_bytes(10);
BEGIN
    RETURN encode(
        set_byte(
            set_byte(
                substr(int8send(v_time), 3, 6) || v_rand,
                6, (get_byte(v_rand, 0) & 15) | 112
            ),
            8, (get_byte(v_rand, 2) & 63) | 128
        ),
        'hex'
    )::uuid;
END;
$$ LANGUAGE plpgsql VOLATILE;
