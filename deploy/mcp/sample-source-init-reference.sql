-- Additional synthetic data shared by the reference MySQL and PostgreSQL sources.
CREATE TABLE order_items (
    id BIGINT NOT NULL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders(id),
    item VARCHAR(128) NOT NULL,
    quantity INTEGER NOT NULL,
    discount DECIMAL(10, 2)
);

INSERT INTO order_items (id, order_id, item, quantity, discount) VALUES
    (1, 1, 'widget', 2, NULL),
    (2, 1, 'cable', 1, 1.25),
    (3, 2, 'widget', 3, 0.00);

CREATE TABLE private_orders (
    id BIGINT NOT NULL PRIMARY KEY,
    note VARCHAR(128) NOT NULL
);

INSERT INTO private_orders (id, note) VALUES (1, 'synthetic private value');
