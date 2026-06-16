-- Install gen_uuid_v7() in public schema for use by all tenant schemas.
-- Compatible with PostgreSQL < 17 (PG 17 has native UUIDv7 support).
-- gen_random_bytes() requires pgcrypto; install it here.
-- Byte layout: [0-5]=ms_timestamp [6]=0x7|rand_a[0:3] [7]=rand_a[4:11]
--              [8]=0x80|rand_b[0:5] [9-15]=rand_b continuation
CREATE EXTENSION IF NOT EXISTS pgcrypto;

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
