-- Эти INSERT должны быть пропущены (CSV нет):
INSERT INTO t SELECT id FROM users;
INSERT INTO t SET name = 'nope';

-- Этот должен пройти → ok.csv
INSERT INTO ok (email, label) VALUES ('survived@example.test', 'survived');
