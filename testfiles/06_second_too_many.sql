-- Второй INSERT в t с большим числом значений, чем колонок заголовка.
-- Ожидание: t.csv от первого INSERT, без t(1).csv; второй INSERT пропускается без CSV.
INSERT INTO t (id, name) VALUES (1, 'ok');
INSERT INTO t (id, name, extra) VALUES (2, 'x', 'y');
