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