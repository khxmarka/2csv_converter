-- Подряд: 2 INSERT в A, 2 INSERT в B, снова 1 INSERT в A.
-- Ожидание рядом с этим файлом:
--   A.csv — заголовок от первого INSERT в A ("id","name","email"), три строки данных;
--   B.csv — заголовок от первого INSERT в B ("code","email","phone"), две строки данных.
-- Пятый INSERT в A с другими именами колонок не пишет второй заголовок и не создаёт A(1).csv.
INSERT INTO A (id, name, email) VALUES (1, 'a1', 'a1@example.test');
INSERT INTO A (id, name, email) VALUES (2, 'a2', 'a2@example.test');
INSERT INTO B (code, email, phone) VALUES ('b1', 10, '555');
INSERT INTO B (code, email, phone) VALUES ('b2', 20, '556');
INSERT INTO A (x, email, phone) VALUES (3, 'a3', '557');
