// Package pii matches SQL table and column names against the approved
// multilingual personal-data dictionary.
package pii

import (
	"strings"
	"unicode"
)

var stopNames = map[string]struct{}{
	"productname": {},
	"filename":    {},
	"displayname": {},
	"orderstate":  {},
	"filehash":    {},
	"settings":    {},
}

var terms = buildTerms()

// Match reports whether name contains an approved PII term.
// Matching is Unicode case-insensitive and intentionally uses substrings,
// including short approved terms such as name, tel, cp, hp, sex, and dob.
func Match(name string) bool {
	lower := strings.ToLower(name)
	if _, stopped := stopNames[compact(lower)]; stopped {
		return false
	}
	for _, term := range terms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// MatchAny reports whether at least one name matches the PII dictionary.
func MatchAny(names []string) bool {
	for _, name := range names {
		if Match(name) {
			return true
		}
	}
	return false
}

func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == '.' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func buildTerms() []string {
	set := make(map[string]struct{})
	addWords := func(raw string) {
		for _, term := range strings.Fields(raw) {
			set[strings.ToLower(term)] = struct{}{}
		}
	}

	addWords(coreTerms)
	addWords(documentTerms)

	// Every approved underscore/hyphen form also supports the separators
	// required by the specification: none, _, -, ., space, and __.
	for term := range set {
		if strings.ContainsAny(term, "_-") {
			parts := strings.FieldsFunc(term, func(r rune) bool {
				return r == '_' || r == '-'
			})
			if len(parts) > 1 {
				for _, separator := range []string{"", "_", "-", ".", " ", "__"} {
					set[strings.Join(parts, separator)] = struct{}{}
				}
			}
		}
	}

	// Approved compounds that are listed in compact form are expanded into
	// the same separator configurations without introducing new stems.
	for _, parts := range compoundParts {
		for _, separator := range []string{"", "_", "-", ".", " ", "__"} {
			set[strings.Join(parts, separator)] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for term := range set {
		if term != "" {
			out = append(out, term)
		}
	}
	return out
}

var compoundParts = [][]string{
	{"user", "name"},
	{"user", "full", "name"},
	{"user", "first", "name"},
	{"user", "last", "name"},
	{"user", "email"},
	{"user", "mail"},
	{"user", "password"},
	{"user", "id"},
	{"customer", "name"},
	{"customer", "email"},
	{"client", "name"},
	{"contact", "name"},
	{"contact", "email"},
	{"employee", "name"},
	{"member", "name"},
	{"account", "name"},
	{"account", "email"},
	{"profile", "name"},
	{"patient", "name"},
	{"student", "name"},
	{"guest", "name"},
	{"first", "name"},
	{"last", "name"},
	{"middle", "name"},
	{"full", "name"},
	{"family", "name"},
	{"legal", "name"},
	{"real", "name"},
	{"preferred", "name"},
	{"email", "address"},
	{"phone", "number"},
	{"mobile", "number"},
	{"postal", "code"},
	{"zip", "code"},
	{"address", "line"},
	{"address", "line1"},
	{"address", "line2"},
	{"address", "line3"},
	{"passport", "number"},
	{"date", "of", "birth"},
	{"birth", "date"},
	{"birth", "place"},
	{"place", "of", "birth"},
	{"gender", "id"},
	{"national", "id"},
	{"id", "card"},
	{"identity", "card"},
	{"driver", "license"},
	{"driver", "licence"},
}

const coreTerms = `
user usr usuario pengguna customer cust client cliente
person people member account acc profile prof employee emp staff
admin author owner contact billing shipping guest student patient
passenger applicant holder buyer vendor visitor subscriber
пользователь юзер клиент сотрудник контакт покупатель пассажир
пациент гость ученик студент аккаунт профиль
pengguna pelanggan karyawan nasabah
用户 会员 客户 联系人 员工 病人 学生 乘客 账户 帐号

name names nm fname lname mname pname
firstname firstnames first_name given givenname forename forenames
lastname lastnames last_name surname familyname family_name
middlename middle_name midname secondname second_name thirdname
fullname full_name fullnames legalname legal_name
nickname nick nick_name nname
maiden maidenname maiden_name birthname birth_name
patronymic patronym patronymicname matronymic
ufn userfullname user_full_name usefullname
realname real_name officialname preferredname preferred_name
contactname contact_name personname people_name namalengkap
firstname1 firstname2 lastname1 lastname2
f_name l_name m_name
username userfirstname userlastname usernama usernombre
customername clientname employeename membername accountname profilename
patientname studentname guestname

nombre nombres nomb apellido apellidos ape apell
primernombre primer_nombre segundo_nombre
nombrepila nombre_pila nombrecompleto nombre_completo
apellidopaterno apellido_paterno apellidomaterno apellido_materno
primerapellido segundoapellido nombredeusuario nombre_de_usuario nombreusuario

имя имена фамилия фамилии фам отчество отч фио
полноеимя полное_имя полноимя девичьяфамилия девичья_фамилия
ник никнейм имяфамилия имя_фамилия

імя ім'я ім’я прізвище прізвища
побатькові по_батькові по-батькові батькові
піб повнеімя повне_імя повнеім'я нік нікнейм

nama namalengkap nama_lengkap namadepan nama_depan
namabelakang nama_belakang namatengah nama_tengah
namapanggilan nama_panggilan namapengguna nama_pengguna
namauser nama_user namakeluarga nama_keluarga namakecil nama_kecil

姓名 名字 名 姓 姓氏 全名 大名 小名 昵称 暱稱 真实姓名 真實姓名
会员姓名 客户姓名 联系人姓名 中文名 英文名
xingming xing_ming mingzi ming_zi xingshi

email e_mail e-mail emai emal eml
mail mailbox mailid mail_id mails
emailid email_id emailaddr email_addr emailaddress email_address
usermail user_mail useremail user_email user_e_mail
contactemail contact_email primaryemail primary_email
secondaryemail secondary_email recoveryemail
correo correoelectronico correo_electronico correoe emailusuario email_usuario
почта элпочта эл_почта электроннаяпочта электронная_почта
емейл имейл емеил emailадрес емайл
пошта елпошта ел_пошта електроннапошта електронна_пошта імейл
surel surelpengguna alamatemail alamat_email alamatsurel
邮箱 電子邮箱 电子邮件 電子郵件 电邮 電郵 邮件地址 用戶郵箱 用户邮箱
youxiang you_xiang dianziyoujian
customeremail accountemail loginmail

pass passwd password passwords passw pswd psw pwd
passphrase passcode passkey
userpass user_pass userpassword user_password
passhash pass_hash passwordhash password_hash pwdhash pwd_hash
passsalt pass_salt passwordsalt password_salt pwdsalt
hash hashes hasher hashvalue hash_value
salt salts salted saltvalue salt_value
md5 sha sha1 sha256 sha512 bcrypt argon argon2 scrypt crypt
encryptedpassword encpassword passwordenc
contrasena contraseña contrasenha clave clavedeacceso claveacceso
пароль пароли пассворд хеш хэш соль парольхеш пароль_хеш парольсоль пароль_соль
kata_sandi katasandi sandi sandipengguna passwordpengguna password_pengguna
密码 密碼 口令 哈希 散列 盐 鹽 盐值 密碼哈希 密码哈希
mima mi_ma kouling senha

tel tele telephone telephony telno tel_no telnum telnumber
phone phones phoneno phone_no phonenum phone_num phonenumber phone_number phonenbr
mobile mobiles mobileno mobile_no mobilenum mobile_number
cell cells cellphone cell_phone cellno cellular celular
msisdn msisdnno whatsapp wa_number wanumber
telefono teléfono telefonos telefono_movil telefonomovil
movil móvil celularno numerocelular numero_celular
телефон тел номер_телефона номертелефона мобильный моб сотовый сот телефон1 телефон2
telepon telpon telp tlp nohp no_hp hp handphone
nomortelepon nomor_telepon nomortelp nomor_hp nomorhp notelp no_telp notelpon
电话 电话号码 手機 手机 手机号 手機號 联系电话 聯繫電話 座机 移動電話
dianhua shouji shou_ji

zip zipc zipcode zip_code zipcd
postal postalcode postal_code postcode post_code pcode po_box pobox
cp cpostal codigopostal codigo_postal código_postal codpostal cod_postal
pin pincode pin_code
индекс инд почтовыйиндекс почтовый_индекс почт_индекс
поштовийіндекс поштовий_індекс
kodepos kode_pos kodpos poskod
邮编 郵編 邮政编码 郵政編碼 郵遞區號 邮递区号
youbian you_bian

address addresses addr adres adresse addr1 addr2 addr3
address1 address2 address3 address_1 address_line addressline
addressline1 address_line1 line1 line2 line3
billingaddress billing_address shippingaddress shipping_address
homeaddress home_address officeaddress workaddress
street streets streetname street_name streetno street_no street1 street2 stname
city cities town village locality suburb
district region province county municipality
state states staten
country countries ctry country_name countryname
direccion dirección domicilio calle ciudad pais país estado colonia barrio municipio provincia
адрес адреса улица город страна область край район дом квартира корпус строение офис
населенныйпункт населённый_пункт
адреса вулиця місто країна область район будинок квартира
alamat alamat1 alamat2 jalan kota negara provinsi kabupaten kecamatan kelurahan rt rw
地址 住址 详细地址 街道 街 路 城市 国家 國家 省 市 区 區 县 门牌 楼号 房间
dizhi jiedao chengshi guojia

passport passports passportno passport_no passportnum passport_number passportnbr passno pass_no
pasaporte pasaporte_no nropasaporte
паспорт паспорта серияпаспорта серия_паспорта номерпаспорта номер_паспорта паспортсерия паспортномер
paspor pasporno nomorpaspor nomor_paspor nopaspor
护照 護照 护照号 护照号码 huzhao hu_zhao
visa visano

dob dateofbirth date_of_birth dateob d_o_b
birthdate birth_date birthday birth_day birth birthyear birth_year yearofbirth yob
born borndate dateborn
fecha_nacimiento fechanacimiento fecha_de_nacimiento fecnac fec_nac
датарождения дата_рождения датаррождения др датанародження дата_народження
tanggallahir tanggal_lahir tgl_lahir tgllahir tglahir lahir
生日 出生日期 出生年月 出生 出生年 shengri chu_sheng chushengriqi
birthplace birth_place placeofbirth place_of_birth
месторождения место_рождения tempatlahir tempat_lahir 出生地

username user_name user-name usrname usr_name uname u_name
userid user_id user-id
login loginname login_name loginid login_id
screenname screen_name nickname handle alias
accountname account_name accname
nombreusuario nombre_usuario usuario
логин логин_имя имяпользователя имя_пользователя юзернейм
namapengguna nama_pengguna userpengguna
用户名 用戶名 账号 帳號 帐户 登錄名 登录名
yonghuming yong_hu_ming zhanghao

gender genders genderid gender_id gendercode gender_code
sex sexo sexo_id genero género generoid
пол пол_id кодпола стать
jeniskelamin jenis_kelamin jekel jk kelamin
性别 性別 男女 xingbie xing_bie

users usuarios customers clientes members
пользователи клієнт клієнти
`

const documentTerms = `
nome nomes sobrenome apelido nomecompleto nome_completo primeironome primeiro_nome
correio correioeletronico email senha palavrapasse palavra_passe palavrachave
telefone telemovel telemóvel celular
morada endereco endereço rua cidade pais país estado
cep codigopostal codigo_postal código_postal
passaporte datanascimento data_nascimento datadenascimento
utilizador usuario usuário genero gênero sexo

prenom prénom nom nomdefamille nom_de_famille nomfamille
courriel email motdepasse mot_de_passe mdp
telephone téléphone portable adresse rue ville pays etat état
codepostal code_postal passeport
datenaissance date_de_naissance datedenaissance
utilisateur identifiant genre sexe

vorname nachname familienname vollname geburtsname
email passwort kennwort telefon handy mobilfunk
strasse straße adresse stadt ort land bundesland
plz postleitzahl reisepass geburtsdatum geburtstag
benutzername nutzername anmeldename geschlecht

nome cognome nomecompleto email posta
password parolachiave parola_dordine
telefono cellulare indirizzo via citta città paese stato
cap codicepostale codice_postale passaporto
datanascita data_di_nascita datadinascita
utente genere sesso

nama namapenuh nama_penuh namapertama namaakhir
emel emel_pengguna katalaluan kata_laluan katalaluanpengguna
telefon no_telefon notel alamat poskod pos_kod
pasport tarikhlahir tarikh_lahir namapengguna nama_pengguna jantina

ssn tin itin nino nhs aadhaar aadhar pan pesel cnic
nik ktp npwp ktpno ktp_no nikktp
dni nie cif curp rfc iin inn инн снилс огрн
іпн ипн рнокпп rnokpp
nationalid national_id natid
idcard id_card identitycard identity_card
driverlicense driver_licence drivinglicence
dlnumber dl_number licenceno sim_no nosim cedula cédula
ein
`
