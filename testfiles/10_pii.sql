-- Оба INSERT содержат PII и должны создать CSV.
-- Первый проходит по имени таблицы и расширенной конфигурации колонки.
INSERT INTO user_profiles (id, user__full__name, recovery_email)
VALUES (1, 'Анна Иванова', 'anna@example.test');

-- Второй проходит по многоязычным названиям колонок.
INSERT INTO archive (id, no_hp, дата_рождения, инн)
VALUES (2, '+62-000-000', '1990-01-01', '0000000000');
