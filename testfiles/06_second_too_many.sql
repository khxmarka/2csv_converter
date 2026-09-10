-- Второй INSERT в t с большим числом значений, чем колонок заголовка.
-- Ожидание: t.csv от первого INSERT, без t(1).csv; второй INSERT пропускается без CSV.
INSERT INTO t (id, name, email) VALUES (1, 'ok', 'a@example.test');
INSERT INTO t (id, name, email, extra) VALUES (2, 'x', 'y', 'z');
