# ldap-phonebook


Установите зависимости для CentOS 7:
```bash
sudo dnf install -y gcc git make pkgconfig gtk3-devel libappindicator-gtk3-devel
```

Установите зависимости Go:
```bash
go get github.com/gotk3/gotk3@v0.6.1
go get github.com/dawidd6/go-appindicator
go get gopkg.in/ldap.v2
```

Соберите программу:
```bash
go build -o ldap-phonebook
```



# Инструкция по сборке RPM

Создайте структуру директорий для сборки:
```bash
mkdir -p ~/rpmbuild/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
```
Создайте архив с вашими файлами и поместите его в SOURCES:
```bash
tar czvf ~/rpmbuild/SOURCES/ldap-phonebook.tar.gz \
    ldap-phonebook ldap-phonebook.ico ldap-phonebook.json
```
Поместите файл ldap-phonebook.spec в ~/rpmbuild/SPECS/ldap-phonebook.spec

Соберите RPM-пакет:
```bash
rpmbuild -bb ~/rpmbuild/SPECS/ldap-phonebook.spec
```
Готовый RPM-пакет будет находиться в ~/rpmbuild/RPMS/<архитектура>/



# Доп. настройки для LDAP-сервера


- Увеличение лимита
```
dn: cn=config
changetype: modify
replace: olcSizeLimit
olcSizeLimit: unlimited

dn: olcDatabase={1}mdb,cn=config
changetype: modify
replace: olcLimits
olcLimits: {0} * size=unlimited
```
Подробнее по ссылке https://www.openldap.org/doc/admin26/limits.html

- Анонимный доступ
```
dn: cn=config
changetype: modify
add: olcAllows
olcAllows: bind_anon_cred bind_anon_dn update_anon

dn: olcDatabase={1}mdb,cn=config
changetype: modify
replace: olcAccess
olcAccess: {2}to * by * read
```
Подробнее по ссылкам:

https://www.openldap.org/doc/admin24/security.html

https://www.openldap.org/doc/admin24/access-control.html


- База поиска
```
dn: olcDatabase={1}mdb,cn=config
changetype: modify
replace: olcDefaultSearchBase
olcDefaultSearchBase: ou=abook,dc=mail,dc=local
```

- Внесение настроек
1. Первый вариант
```bash
ldapmodify -Y EXTERNAL -H ldapi:/// <<EOF
dn:           cn=config
changetype:   modify
replace:      olcLogLevel
olcLogLevel:  256
EOF
```

2. Второй вариант
```bash
ldapmodify -h localhost -p 389 -D cn=admin,cn=config -w config_pass -f config.ldif
```





