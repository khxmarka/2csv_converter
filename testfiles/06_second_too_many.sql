-- Второй INSERT в t с большим числом значений, чем колонок заголовка ключа.
-- Ожидание: t.csv от первого INSERT; строки второго INSERT пропускаются (шире ключа), без t(1).csv.
INSERT INTO t (id, name, email) VALUES (1, 'ok', 'a@example.test');
INSERT INTO t (id, name, email, extra) VALUES (2, 'x', 'y', 'z');
