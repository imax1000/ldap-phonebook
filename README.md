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















