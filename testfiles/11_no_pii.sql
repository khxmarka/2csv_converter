-- Ни один INSERT не должен создавать CSV.
INSERT INTO orders (id, status, created_at)
VALUES (1, 'ready', '2026-09-09');

INSERT INTO settings
VALUES ('theme', 'dark');

-- Все потенциальные совпадения ниже входят в точный стоп-список.
INSERT INTO products (product_name, file_hash, order_state)
VALUES ('demo', 'abc123', 'ready');
