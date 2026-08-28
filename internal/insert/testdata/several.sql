-- несколько INSERT в одном файле; DDL и комментарии не должны мешать
CREATE TABLE alpha (id INT);
-- INSERT INTO ghost (id) VALUES (0);
INSERT INTO alpha (id, name) VALUES (1, 'one');
/* внутри комментария INSERT INTO ghost (x) VALUES (2); */
INSERT INTO beta (`code`, `label`) VALUES
	('a', 'alpha'),
	('b', 'beta');
