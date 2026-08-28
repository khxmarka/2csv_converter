-- Эти INSERT должны быть пропущены (CSV нет):
INSERT INTO t SELECT id FROM users;
INSERT INTO t SET name = 'nope';
INSERT INTO t VALUES (1);

-- Этот должен пройти → ok.csv
INSERT INTO ok (id, label) VALUES (1, 'survived');
