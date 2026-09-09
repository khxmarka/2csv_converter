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

func TestMatchAny(t *testing.T) {
	if MatchAny(nil) {
		t.Fatal("MatchAny(nil) = true")
	}
	if MatchAny([]string{"id", "created_at", "status"}) {
		t.Fatal("MatchAny(non-PII) = true")
	}
	if !MatchAny([]string{"id", "created_at", "recovery_email"}) {
		t.Fatal("MatchAny(with PII) = false")
	}
}
