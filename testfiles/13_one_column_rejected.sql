-- Одна PII-колонка без PII-имени таблицы — пропуск.
INSERT INTO t (email) VALUES ('a@example.test');
INSERT INTO t (phone) VALUES ('555');
