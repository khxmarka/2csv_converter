-- Два INSERT в одну таблицу в одном файле → один orders.csv
-- (заголовок от первого INSERT, строки данных от обоих).
-- Комментарии и строка, похожая на INSERT, не должны ломать разбор.
-- INSERT INTO ghost (id) VALUES (0);
INSERT INTO orders (id, item) VALUES (100, 'first');
/* INSERT INTO ghost (id) VALUES (1); */
INSERT INTO orders (id, item) VALUES (200, 'INSERT INTO x (id) VALUES (9)');
