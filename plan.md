# Plan: фильтр PII в SQL и догон через `converted.txt`

План для исполнителя-ИИ. Норматив: `CONSTRAINTS_AND_POLICY.md` (после пункта 4 — обновить его под эти правила). Excel-фича из `plan_new.md` уже сделана и здесь не переоткрывается.

Язык: **Go**. Работа **только по пунктам**. К следующему не переходить без явного апрува. Если план и политика расходятся — остановиться и спросить, не импровизировать.

---

## Цель

Две фичи поверх уже работающего конвертера SQL + Excel:

1. **Фильтр SQL.** В CSV попадают только `INSERT ... VALUES`, у которых имя таблицы или хотя бы одна колонка совпали со словарём персональных данных (PII). Остальное тихо пропускать.
2. **Догон.** `{корень}\converted.txt` — накопительный список уже обработанных **верхних папок**. При следующем запуске эти папки не обходить заново.

---

## Контекст кода (не ломать)

| Слой | Где | Сейчас | Что менять |
|---|---|---|---|
| Парсер INSERT | `internal/insert` | Отдаёт `Meta.Table` и `Meta.Columns` после `cleanIdent` | Не переписывать |
| Запись CSV | `internal/convert` → `csvout` | Каждый успешный INSERT → CSV; чужой файл → `(n)` | Фильтр до `Create`; при первом создании ключа — **затирать** целевой CSV |
| Обход / пул | `internal/scan`, `internal/app` | `.sql` / `.xlsx` / `.xls` | Пропуск файлов из готовых верхних папок |
| `converted.txt` | `internal/converted` | Перезапись списка папок в конце запуска | Читать в начале, **дописывать** по завершении папки, не затирать целиком |
| Excel | `internal/xlsconv` | Листы → CSV (§13) | PII-фильтр **не** применять; книги в готовой папке не трогать; при повторе папки — затирать CSV |

Сломаются тесты вроде `TestTwoInsertsDifferentTables` (`orders` без PII) — поправить под фильтр.

---

## Пункт 0. Принятые правила

Код не писать. Это зафиксированные ответы заказчика. Ниже — рабочая спецификация для пунктов 1–4.

### 0.1. Какие SQL-INSERT писать в CSV

Писать INSERT, если верно **хотя бы одно**:

- имя таблицы (без схемы, после `cleanIdent`) прошло матчер;
- или хотя бы одна колонка из списка колонок прошла матчер.

Иначе — пропустить тихо (как `INSERT ... SELECT` / `SET`). Соседние таблицы того же `.sql` это не спасают.

Примеры:

| SQL | Результат |
|---|---|
| `INSERT INTO users (id, email) …` + `INSERT INTO settings (key, value) …` | CSV только у `users` |
| `INSERT INTO users (id, status) …` + `INSERT INTO users (id, email) …` | **Оба** пишутся (имя таблицы `users`) |
| `INSERT INTO users VALUES (…)` без списка колонок | Писать без шапки (фильтр только по имени таблицы) |
| `INSERT INTO settings VALUES (…)` | Не писать |
| `INSERT INTO users (id, created_at) …` | Писать (имя таблицы) |

Шапка CSV и склейка нескольких INSERT одной таблицы **в одном запуске** — как §5: шапка от верхнего успешного INSERT, дальше строки по порядку значений.

**Excel:** фильтр PII не применяется. Книги обрабатываются как сейчас (≤5 листов, пустой лист без CSV, имя `{книга}_{лист}.csv`).

### 0.2. Матчер имени таблицы / колонки

1. Нижний регистр Unicode. Обрамление SQL уже снято (`cleanIdent`).
2. Совпадение — **подстрока**: любой терм из словарей приложений A и B встречается внутри строки. Регистр не важен (`NAMA`, `UserName`, `email1`).
3. Голые короткие термы ловить и как целое имя, и как подстроку: `name`, `pass`, `hash`, `salt`, `tel`, `cp`, `mail`, `state`, `city`, `hp`, `sex`, `dob` и остальные из словаря.
4. Явно ловить `user_id` / `userid` / `user-id` / `user_ID`.
5. Колонки с хвостом-цифрой ловить через подстроку: `email1`, `phone_2`, `address_line1`.
6. **Стоп-список** (проверять до «да, PII»). После lower и удаления `_` `-` `.` пробелов компактное имя равно одному из:

   `productname`, `filename`, `displayname`, `orderstate`, `filehash`

   То же для исходных `product_name`, `file_name`, `file-name`, `display_name`, `order_state`, `file_hash`. Их **не** ловить, даже если внутри есть `name` / `state` / `hash`.
7. Однобуквенные иглы вроде `u` и голый `id` в словарь **не** класть.
8. Языки: EN, ES, RU, UK, ID, ZH + PT, FR, DE, IT, малайский. Плюс документы / нац.ID (приложение B). IP и возраст **не** добавлять без отдельной просьбы.

Словари — приложения A и B. Новые стемы сверх них не выдумывать.

### 0.3. Догон через `converted.txt`

| Правило | Значение |
|---|---|
| Файл | `{выбранный корень}\converted.txt` (новое имя не вводить) |
| Строка | только имя верхней папки (`Foo`, не `Foo\Bar`, не путь к `.sql`) |
| Формат | UTF-8 без BOM, LF, одно имя на строку |
| Старт | прочитать файл; нет файла → пустой набор |
| Skip | файлы внутри папок из набора не открывать и не конвертить (SQL и Excel) |
| Допись | когда верхняя папка полностью завершена — дописать имя в конец, если его ещё нет |
| Без CSV | папку всё равно дописывать (все INSERT отсеялись / книга >5 листов / пустые листы) |
| Прерывание | имени оборванной папки в файле нет → следующий запуск обработает её снова |
| Перезапись файла | **запрещена**: не затирать весь список в конце запуска |
| Сброс | удалить `converted.txt` руками; флага `--force` нет |
| Файлы в корне | как §3/§7: в `converted.txt` не попадают, каждый запуск видны снова |

### 0.4. Перезапись CSV

Правило «чужой CSV не трогать → `{name}(n).csv`» **отменяется**.

При первом создании ключа в запуске (папка + таблица SQL, или Excel-лист), если целевой файл уже на диске — **затереть** и писать заново. Второй INSERT той же таблицы в том же запуске по-прежнему **дописывает** в тот же файл.

### 0.5. Лог

Пропуск INSERT из‑за фильтра PII в консоль **не** писать. Остаётся §8: статус папки и критические ошибки.

### 0.6. Статус пункта 0

**Стоп:** апрув этих правил, затем пункт 1.

---

## Пункт 1. Словарь и матчер (без диска)

**Сделать:**

- пакет (например `internal/pii`): термы A+B, стоп-список из §0.2, подстрока после lower;
- `Match(name string) bool`, `MatchAny(names []string) bool`;
- юнит-тесты без `C:\Source\db` — позитивы и негативы из приложения C;
- без NLP и сторонних PII-библиотек.

**Не делать:** `convert.File`, обход, Excel, `converted.txt`, CLI.

**Проверка:** `go test` пакета.

**Стоп:** ждать апрув пункта 1.

---

## Пункт 2. Фильтр в SQL-конвертации

**Сделать:**

- в `convert.session.begin`: если ни таблица, ни колонки не прошли матчер — не вызывать `csvout.Create`, INSERT как skip, без лога;
- нет списка колонок — решать только по имени таблицы;
- Excel-писатель матчер не вызывать;
- поправить тесты `internal/convert`: без PII → нет CSV; `users` → CSV; смешанный файл — только прошедшие таблицы;
- склейка одной таблицы в одном запуске как §5.

**Не делать:** догон `converted.txt` (пункт 3).

**Проверка:** `go test ./...` в части SQL.

**Стоп:** ждать апрув пункта 2.

---

## Пункт 3. Догон через `converted.txt`

**Сделать:**

- читать `converted.txt` в начале `app.Run`; нет файла — пустой набор;
- не брать в работу файлы, чья верхняя папка уже в наборе;
- по завершении верхней папки — **дописать** имя (не `Write` всего списка с нуля); строка целиком; блокировка из‑за воркеров;
- прерывание посреди папки — имени нет;
- первое создание ключа в запуске **затирает** существующий CSV с тем же именем, без `(n)`; дописывание второго INSERT — как сейчас;
- тесты: второй запуск не заходит в готовую папку; после «обрыва» повтор затирает CSV; папка с нулём CSV всё равно дописывается;
- файлы прямо в корне — как §3 (в список не попадают).

**Не делать:** `--force`, новое имя файла списка, фильтр колонок Excel.

**Проверка:** `go test` app/converted.

**Стоп:** ждать апрув пункта 3.

---

## Пункт 4. Политика, README, сборка

**Сделать:**

- `CONSTRAINTS_AND_POLICY.md`: фильтр SQL (§0.1–0.2); затирать целевой CSV вместо `(n)` (§0.4); `converted.txt` накопительный, skip готовых папок (§0.3); критерий готовности;
- README: коротко то же;
- `go build -o 2csv.exe .`
- ручные SQL-фикстуры в `testfiles/`: есть PII / нет / смешанный. Не класть дампы из `C:\Source\*`.

**Стоп:** ждать апрув пункта 4. На этом фича закрыта.

---

## Что не трогать

- формат ячейки CSV §6 (кавычки, запятая, LF, без BOM);
- склейка нескольких INSERT одной таблицы **в одном запуске** в один CSV;
- пропуски `INSERT ... SELECT` / `SET` / битый INSERT;
- корни только `C:\Source\db` и `C:\Source\combo`;
- лог §8 (кроме тишины фильтра PII);
- Excel §13, кроме skip готовой папки и затирания CSV при повторе;
- исходные `.sql` / `.xlsx` / `.xls` не менять;
- не ходить выше выбранного корня;
- не добавлять `--force`.

---

## Приложение A. Словарь PII (основные языки)

Спецификация покрытия. Новые стемы сверх A+B не добавлять. Регистр любой. Имя таблицы — тем же матчером, что колонка.

Префиксы ниже нужны для компаундов (`username`, `useremail`, …). Однобуквенный `u` как иглу подстроки **не** класть.

```
user usr usuario pengguna customer cust client cliente
person people member account acc profile prof employee emp staff
admin author owner contact billing shipping guest student patient
passenger applicant holder buyer vendor visitor subscriber
пользователь юзер клиент сотрудник контакт покупатель пассажир
пациент гость ученик студент аккаунт профиль
pengguna pelanggan karyawan nasabah
用户 会员 客户 联系人 员工 病人 学生 乘客 账户 帐号
```

Разделители в именах: пусто, `_`, `-`, `.`, пробел, `__`. Примеры, которые обязаны ловиться: `user_lastname`, `user-lastname`, `user.lastname`, `user lastname`, `userlastname`, `User_LastName`.

### A1. Имя / ФИО

```
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
contactname personname people_name namalengkap
firstname1 firstname2 lastname1 lastname2
f_name l_name m_name

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
```

Компаунды: `username`, `userfullname`, `userfirstname`, `userlastname`, `usernama`, `usernombre`, `customername`, `clientname`, `contactname`, `employeename`, `membername`, `accountname`, `profilename`, `patientname`, `studentname`, `guestname`.

`displayname` / `display_name` — **стоп-список**, не ловить.

### A2. Email

```
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
useremail customeremail contactemail accountemail loginmail
```

### A3. Пароль / hash / salt

```
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
```

### A4. Телефон

```
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
```

`fax` не включать.

### A5. ZIP / postal / CP

```
zip zipc zipcode zip_code zipcd
postal postalcode postal_code postcode post_code pcode po_box pobox
cp cpostal codigopostal codigo_postal código_postal codpostal cod_postal
pin pincode pin_code
индекс инд почтовыйиндекс почтовый_индекс почт_индекс
поштовийіндекс поштовий_індекс
kodepos kode_pos kodpos poskod
邮编 郵編 邮政编码 郵政編碼 郵遞區號 邮递区号
youbian you_bian
```

### A6. Адрес

```
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
```

`order_state` / `orderstate` — стоп-список.

### A7. Паспорт

```
passport passports passportno passport_no passportnum passport_number passportnbr passno pass_no
pasaporte pasaporte_no nropasaporte
паспорт паспорта серияпаспорта серия_паспорта номерпаспорта номер_паспорта паспортсерия паспортномер
paspor pasporno nomorpaspor nomor_paspor nopaspor
护照 護照 护照号 护照号码 huzhao hu_zhao
visa visano
```

### A8. Дата рождения / DOB

```
dob dateofbirth date_of_birth dateob d_o_b
birthdate birth_date birthday birth_day birth birthyear birth_year yearofbirth yob
born borndate dateborn
fecha_nacimiento fechanacimiento fecha_de_nacimiento fecnac fec_nac
датарождения дата_рождения датаррождения др датанародження дата_народження
tanggallahir tanggal_lahir tgl_lahir tgllahir tglahir lahir
生日 出生日期 出生年月 出生 出生年 shengri chu_sheng chushengriqi
birthplace birth_place placeofbirth place_of_birth
месторождения место_рождения tempatlahir tempat_lahir 出生地
```

### A9. Username / login

```
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
```

Голый терм `id` не добавлять.

### A10. Gender / sex

```
gender genders genderid gender_id gendercode gender_code
sex sexo sexo_id genero género generoid
пол пол_id кодпола стать
jeniskelamin jenis_kelamin jekel jk kelamin
性别 性別 男女 xingbie xing_bie
```

### A11. Прочее по смыслу ФИО

```
patronymic отчество побатькові maiden девичья nickname ник 昵称
contact_name contactname
```

### A12. Имена таблиц

```
user users usuario usuarios customer customers cliente clientes
member members pengguna пользователь пользователи клієнт клієнти
用户 会员 客户
```

---

## Приложение B. PT / FR / DE / IT / малайский и документы

Тот же матчер. IP и возраст не входят.

### B1. Португальский

```
nome nomes sobrenome apelido nomecompleto nome_completo primeironome primeiro_nome
correio correioeletronico email senha palavrapasse palavra_passe palavrachave
telefone telemovel telemóvel celular
morada endereco endereço rua cidade pais país estado
cep codigopostal codigo_postal código_postal
passaporte datanascimento data_nascimento datadenascimento
utilizador usuario usuário genero gênero sexo
```

### B2. Французский

```
prenom prénom nom nomdefamille nom_de_famille nomfamille
courriel email motdepasse mot_de_passe mdp
telephone téléphone portable adresse rue ville pays etat état
codepostal code_postal passeport
datenaissance date_de_naissance datedenaissance
utilisateur identifiant genre sexe
```

### B3. Немецкий

```
vorname nachname familienname vollname geburtsname
email passwort kennwort telefon handy mobilfunk
strasse straße adresse stadt ort land bundesland
plz postleitzahl reisepass geburtsdatum geburtstag
benutzername nutzername anmeldename geschlecht
```

### B4. Итальянский

```
nome cognome nomecompleto email posta
password parolachiave parola_dordine
telefono cellulare indirizzo via citta città paese stato
cap codicepostale codice_postale passaporto
datanascita data_di_nascita datadinascita
utente genere sesso
```

### B5. Малайский

```
nama namapenuh nama_penuh namapertama namaakhir
emel emel_pengguna katalaluan kata_laluan katalaluanpengguna
telefon no_telefon notel alamat poskod pos_kod
pasport tarikhlahir tarikh_lahir namapengguna nama_pengguna jantina
```

### B6. Документы / нац.ID

```
ssn tin itin nino nhs aadhaar aadhar pan pesel cnic
nik ktp npwp ktpno ktp_no nikktp
dni nie cif curp rfc iin inn инн снилс огрн
іпн ипн рнокпп rnokpp
nationalid national_id natid
idcard id_card identitycard identity_card
driverlicense driver_licence drivinglicence
dlnumber dl_number licenceno sim_no nosim cedula cédula
ein
```

Не добавлять: `ip`, `ipv4`, `age`, `возраст`, `edad`.

---

## Приложение C. Примеры для тестов

**Должны совпасть:**

```
email user_email UserEmail EMAIL email1
correo_electronico почта surel 邮箱
password kata_sandi пароль 密码 senha motdepasse passwort
phone no_hp telefono телефон 手机 phone_2
zip_code kode_pos codigo_postal индекс 邮编 cep plz cap
address alamat direccion адрес 地址 address_line1
passport paspor паспорт 护照 passaporte
date_of_birth tanggal_lahir дата_рождения 生日 dob
username nama_pengguna логин 用户名
gender jenis_kelamin пол 性别
nama NAMA nombre apellido фамилия ім'я
firstname last_name user_lastname user-name ufn
user_id user_ID userid users
ssn инн nik dni снилс
```

**Не должны совпасть:**

```
product_name filename file_name display_name
order_state file_hash
id created_at key value status
```
