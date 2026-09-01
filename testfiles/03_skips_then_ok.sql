-- Эти INSERT должны быть пропущены (CSV нет):
INSERT INTO t SELECT id FROM users;
INSERT INTO t SET name = 'nope';

-- Этот должен пройти → ok.csv
INSERT INTO ok (id, label) VALUES (1, 'survived');
