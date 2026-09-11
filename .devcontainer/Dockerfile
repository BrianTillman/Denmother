# Keep this version aligned with go.mod; post-create.sh checks for drift.
FROM golang:1.26.8-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates git openssl python3 python3-yaml \
    && rm -rf /var/lib/apt/lists/*
