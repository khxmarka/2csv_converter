-- Эти INSERT должны быть пропущены (CSV нет):
INSERT INTO t SELECT id FROM users;
INSERT INTO t SET name = 'nope';

-- Этот должен пройти → ok.csv (две PII-колонки)
INSERT INTO ok (email, phone) VALUES ('survived@example.test', '555');
