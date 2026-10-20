-- Seed data for the quickstart sample source (#186). Runs once, when the
-- sample-source container initializes its data volume. The MySQL entrypoint
-- has already created database `sample` and user `sample_reader`; restrict
-- that user to read-only as defense in depth — the gateway also opens
-- read-only sessions on engines that support them (#20).
REVOKE ALL PRIVILEGES ON `sample`.* FROM 'sample_reader'@'%';
GRANT SELECT ON `sample`.* TO 'sample_reader'@'%';

USE sample;

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
