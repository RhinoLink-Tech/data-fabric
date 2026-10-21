#!/bin/bash
# Seed data for the PostgreSQL quickstart sample source (#180). Runs once,
# when the sample-source container initializes its data volume under
# docker-compose.postgres.yml. The entrypoint has already created database
# `sample`; this script creates the read-only reader account — the gateway
# additionally opens every connection with default_transaction_read_only=on
# (#20).
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE ROLE sample_reader LOGIN PASSWORD '$SAMPLE_READER_PASSWORD';
    GRANT CONNECT ON DATABASE sample TO sample_reader;

    CREATE TABLE orders (
        id BIGINT NOT NULL PRIMARY KEY,
        customer VARCHAR(128) NOT NULL,
        amount DECIMAL(10, 2) NOT NULL,
        status VARCHAR(16) NOT NULL DEFAULT 'new',
        created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
    );

    INSERT INTO orders (id, customer, amount, status, created_at) VALUES
        (1, 'Ada Lovelace', 250.00, 'shipped', '2026-10-01 09:15:00'),
        (2, 'Grace Hopper', 89.50, 'new', '2026-10-05 14:30:00'),
        (3, 'Alan Turing', 1200.00, 'shipped', '2026-10-12 08:05:00');

    GRANT USAGE ON SCHEMA public TO sample_reader;
    GRANT SELECT ON ALL TABLES IN SCHEMA public TO sample_reader;
    ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO sample_reader;
EOSQL
