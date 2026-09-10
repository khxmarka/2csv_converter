-- Технические ID не дают зачёт даже вместе с одной PII-колонкой.
INSERT INTO t (user_id, email) VALUES (1, 'a@example.test');
INSERT INTO t (customer_uuid, phone) VALUES ('x', '555');
