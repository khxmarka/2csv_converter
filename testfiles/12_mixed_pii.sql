-- Проходит по имени таблицы.
INSERT INTO users (id, status)
VALUES (1, 'active');

-- Не проходит: нет PII ни в таблице, ни в колонках.
INSERT INTO settings (id, value)
VALUES (1, 'dark');

-- Проходит только по колонке.
INSERT INTO audit_log (id, email1)
VALUES (2, 'audit@example.test');

-- Не проходит и не спасается соседними INSERT.
INSERT INTO orders (id, created_at)
VALUES (3, '2026-09-09');
