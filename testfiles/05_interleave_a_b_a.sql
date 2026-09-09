-- Подряд: 2 INSERT в A, 2 INSERT в B, снова 1 INSERT в A.
-- Ожидание рядом с этим файлом:
--   A.csv — заголовок от первого INSERT в A ("id","name"), три строки данных;
--   B.csv — заголовок от первого INSERT в B ("code","email"), две строки данных.
-- Пятый INSERT в A с другими именами колонок не пишет второй заголовок и не создаёт A(1).csv.
INSERT INTO A (id, name) VALUES (1, 'a1');
INSERT INTO A (id, name) VALUES (2, 'a2');
INSERT INTO B (code, email) VALUES ('b1', 10);
INSERT INTO B (code, email) VALUES ('b2', 20);
INSERT INTO A (x, email) VALUES (3, 'a3');
