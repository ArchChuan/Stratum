# PostgreSQL 16 + zhparser (Chinese full-text search).
#
# WHY: knowledge_chunks.tsv uses to_tsvector('public.chinese_zh', content) for
# CJK-aware keyword/hybrid RAG. The default 'simple' parser does not segment
# Chinese, so recall on Chinese content is near-zero. zhparser wraps SCWS
# (Simple Chinese Word Segmentation) as a PostgreSQL TEXT SEARCH PARSER.
#
# public_schema.sql degrades gracefully: if the zhparser extension is absent it
# falls back to COPY-ing pg_catalog.simple into public.chinese_zh, so CI
# (postgres:15) and alpine images still boot. This image is for local dev and
# prod where real segmentation matters.
#
# Based on Debian bookworm (postgres:16), NOT alpine: SCWS needs glibc + a full
# autotools toolchain that is painful to satisfy under musl.
FROM postgres:16

# Pin versions for reproducible builds.
ARG SCWS_VERSION=1.2.3
ARG ZHPARSER_REF=master

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        git \
        postgresql-server-dev-16; \
    \
    # --- build SCWS ---------------------------------------------------------
    cd /tmp; \
    curl -fsSL --connect-timeout 10 --max-time 120 --retry 4 --retry-delay 2 --retry-all-errors \
        "http://www.xunsearch.com/scws/down/scws-${SCWS_VERSION}.tar.bz2" -o scws.tar.bz2; \
    tar xjf scws.tar.bz2; \
    cd "scws-${SCWS_VERSION}"; \
    ./configure --prefix=/usr/local; \
    make -j"$(nproc)"; \
    make install; \
    ldconfig; \
    \
    # --- build zhparser extension ------------------------------------------
    cd /tmp; \
    git clone https://github.com/amutu/zhparser.git; \
    cd zhparser; \
    if [ "${ZHPARSER_REF}" != "master" ]; then git checkout "${ZHPARSER_REF}"; fi; \
    SCWS_HOME=/usr/local make; \
    make install; \
    \
    # --- strip build toolchain to keep the image lean ----------------------
    apt-get purge -y --auto-remove \
        build-essential \
        curl \
        git \
        postgresql-server-dev-16; \
    rm -rf /var/lib/apt/lists/* /tmp/scws* /tmp/zhparser

# SCWS dictionaries land under /usr/local/share/scws; zhparser resolves them via
# its default dicts. No extra ENV needed for the bundled rules/dict.
