package pii

import "testing"

func TestMatchApprovedExamples(t *testing.T) {
	tests := []string{
		"email", "user_email", "UserEmail", "EMAIL", "email1",
		"correo_electronico", "почта", "surel", "邮箱",
		"password", "kata_sandi", "пароль", "密码", "senha", "motdepasse", "passwort",
		"phone", "no_hp", "telefono", "телефон", "手机", "phone_2",
		"zip_code", "kode_pos", "codigo_postal", "индекс", "邮编", "cep", "plz", "cap",
		"address", "alamat", "direccion", "адрес", "地址", "address_line1",
		"passport", "paspor", "паспорт", "护照", "passaporte",
		"date_of_birth", "tanggal_lahir", "дата_рождения", "生日", "dob",
		"username", "nama_pengguna", "логин", "用户名",
		"gender", "jenis_kelamin", "пол", "性别",
		"nama", "NAMA", "nombre", "apellido", "фамилия", "ім'я",
		"firstname", "last_name", "user_lastname", "user-name", "ufn",
		"user_id", "user_ID", "userid", "users",
		"ssn", "инн", "nik", "dni", "снилс",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			if !Match(name) {
				t.Fatalf("Match(%q) = false, want true", name)
			}
		})
	}
}

func TestMatchRejectsApprovedNegatives(t *testing.T) {
	tests := []string{
		"product_name", "productname",
		"filename", "file_name", "file-name", "file.name", "file name",
		"display_name", "displayname",
		"order_state", "orderstate",
		"file_hash", "filehash",
		"settings", "id", "created_at", "key", "value", "status",
		"ip", "ipv4", "age", "возраст", "edad",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			if Match(name) {
				t.Fatalf("Match(%q) = true, want false", name)
			}
		})
	}
}

func TestMatchExpandedSeparatorConfigurations(t *testing.T) {
	tests := []string{
		"userlastname",
		"user_lastname",
		"user-lastname",
		"user.lastname",
		"user lastname",
		"user__lastname",
		"User_LastName",
		"user.full.name",
		"user full name",
		"date-of-birth",
		"date.of.birth",
		"national-id",
		"identity.card",
		"driver licence",
		"address__line1",
		"phone-number",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			if !Match(name) {
				t.Fatalf("Match(%q) = false, want true", name)
			}
		})
	}
}

func TestMatchUsesSubstringForSuffixesAndPrefixes(t *testing.T) {
	tests := []string{
		"backup_email1_value",
		"customerPhone_2",
		"legacy_address_line3",
		"tbl_users_archive",
	}
	for _, name := range tests {
		if !Match(name) {
			t.Errorf("Match(%q) = false, want true", name)
		}
	}
}

func TestMatchColumnsThresholdAndDedup(t *testing.T) {
	if MatchColumns(nil) || MatchColumns([]string{}) || MatchColumns([]string{"email"}) {
		t.Fatal("меньше двух зачётных колонок не должно проходить")
	}
	if MatchColumns([]string{"id", "created_at", "status"}) {
		t.Fatal("служебные колонки не должны проходить")
	}
	if !MatchColumns([]string{"email", "phone"}) {
		t.Fatal("две разные PII-колонки должны проходить")
	}
	if MatchColumns([]string{"email", "EMAIL"}) {
		t.Fatal("email и EMAIL — одна колонка")
	}
	if !MatchColumns([]string{"email", "EMAIL", "phone"}) {
		t.Fatal("после дедупликации email/EMAIL остаётся phone")
	}
	if !MatchColumns([]string{" email ", "phone"}) {
		t.Fatal("пробелы по краям не должны ломать совпадение")
	}
	if !MatchColumns([]string{"email", "e_mail"}) {
		t.Fatal("email и e_mail — разные cleaned-имена")
	}
	if MatchColumns([]string{"product_name", "email"}) {
		t.Fatal("стоп-список и одна PII-колонка не должны проходить")
	}
}

func TestMatchColumnsIgnoresTechnicalIdentifiers(t *testing.T) {
	technical := []string{
		"user_id", "user-id", "user.id", "user id", "userId", "userID", "userid",
		"user_identifier", "user-identifier", "user.identifier", "user identifier",
		"userIdentifier", "useridentifier",
		"gender_id", "email_id", "loginIdentifier",
		"user_id1", "customer_id_2", "user_uuid", "customerGuid",
		"user_email_id", "full_name_id", "user_phone_uuid",
	}
	for _, name := range technical {
		t.Run(name, func(t *testing.T) {
			if MatchColumns([]string{name, "email"}) {
				t.Fatalf("технический идентификатор %q не должен давать зачёт", name)
			}
		})
	}
	if MatchColumns([]string{"userid", "email"}) {
		t.Fatal("слитный userid при известной основе — технический ID")
	}
	if !MatchColumns([]string{"emailvalid", "phone"}) {
		t.Fatal("emailvalid не должен считаться техническим ID")
	}
	if !MatchColumns([]string{"email_valid", "phone"}) {
		t.Fatal("email_valid не должен считаться техническим ID")
	}
	if !MatchColumns([]string{"user_ids", "email"}) {
		t.Fatal("множественное ids не технический суффикс, user_ids даёт зачёт вместе с email")
	}
}

func TestMatchColumnsCountsDocumentIdentifiers(t *testing.T) {
	if !MatchColumns([]string{"national_id", "email"}) {
		t.Fatal("national_id должен давать зачёт")
	}
	if !MatchColumns([]string{"passport_id", "phone"}) {
		t.Fatal("passport_id должен давать зачёт")
	}
	if !MatchColumns([]string{"id_card", "email"}) {
		t.Fatal("id_card должен давать зачёт")
	}
	if !MatchColumns([]string{"driverLicenseId", "email"}) {
		t.Fatal("driverLicenseId должен давать зачёт")
	}
	if !MatchColumns([]string{"ssn_id", "email"}) {
		t.Fatal("ssn_id должен давать зачёт")
	}
	if !MatchColumns([]string{"tin_id", "phone"}) {
		t.Fatal("tin_id должен давать зачёт")
	}
	if !MatchColumns([]string{"passport_uuid", "email"}) {
		t.Fatal("документный uuid должен давать зачёт")
	}
	if !MatchColumns([]string{"ssn_guid", "email"}) {
		t.Fatal("документный guid должен давать зачёт")
	}
	if !MatchColumns([]string{"national_id2", "email"}) {
		t.Fatal("national_id2 должен давать зачёт")
	}
	if !MatchColumns([]string{"xxssn_id", "phone"}) {
		t.Fatal("аббревиатура сразу перед ID-суффиксом должна давать зачёт даже без известного префикса")
	}
	if !MatchColumns([]string{"customerssnid", "phone"}) {
		t.Fatal("customerssnid должен давать зачёт")
	}
	if !MatchColumns([]string{"sim_id", "email"}) {
		t.Fatal("короткий документный терм sim_id должен давать зачёт")
	}
	if MatchColumns([]string{"имяId", "email"}) {
		t.Fatal("camelCase ID-суффикс после не-ASCII основы остаётся техническим")
	}
	if !MatchColumns([]string{"passport_id", "ssn_id"}) {
		t.Fatal("две документные колонки должны проходить")
	}
	if MatchColumns([]string{"company_id", "email"}) {
		t.Fatal("company_id не должен защищаться подстрокой pan")
	}
	if MatchColumns([]string{"settings_id", "email"}) {
		t.Fatal("settings_id не должен защищаться подстрокой tin")
	}
	if MatchColumns([]string{"winning_id", "email"}) {
		t.Fatal("winning_id не должен защищаться подстрокой inn")
	}
}

func TestMatchStillAcceptsTechnicalNamesForTables(t *testing.T) {
	for _, name := range []string{"user_id", "userid", "gender_id", "national_id", "users"} {
		if !Match(name) {
			t.Fatalf("Match(%q) = false, таблица с таким именем должна проходить", name)
		}
	}
}
