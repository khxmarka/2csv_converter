-- Два .sql в одной папке оба пишут в t.
-- Ожидание: один t.csv, заголовок от a.sql (путь меньше, чем z.sql), обе строки данных.
INSERT INTO t (id, name) VALUES (1, 'from-a');
