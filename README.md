# Оглавление

## Оглавление

0. [О программе](#О-программе)
1. [Настройки для сборки](#ldap-phonebook)
2. [Настройки по сборке RPM](#Инструкция-по-сборке-RPM)
3. [Доп. настройки для LDAP-сервера](#Доп.настройки-для-LDAP-сервера)

# О программе

Назначение:
Программа представляет собой удобный графический интерфейс для просмотра и поиска контактов сотрудников, хранящихся на LDAP-сервере. Она объединяет функции классического телефонного справочника с возможностями LDAP-запросов, предоставляя пользователям интуитивно понятный инструмент для работы с корпоративными данными.

## Основной функционал:
1. Древовидная структура организаций

- Иерархия:

Отображает организации → отделы в виде дерева.

Автоматическая сортировка по алфавиту (организации и отделы).
Автоматическое построение оргструктуры.


- Контекстное меню:

«Развернуть все» / «Свернуть все» для управления отображением дерева.


- Поиск по дереву:

Показ сотрудников отдела


2. Поиск сотрудников
- Поиск по ФИО, email, телефону (достаточно части строки).
Запуск по кнопке «Поиск» или нажатию Enter.
Поддержка регистронезависимого поиска.
Возможность поиска при неправильной раскладке клавиатуры.

- Результаты поиска в виде таблица с колонками: ФИО, Email, Телефон, Отдел, Организация.
Возможность изменения ширины колонок перетаскиванием.

3. Детальная информация

При выборе сотрудника в таблице отображается подробная карточка.
Есть возможность скопировать эту информацию в буфер обмена

4. Управление через иконку в трее

- Контекстное меню: «Показать», «Выход».

5. Горячие клавиши
- Esc: Сворачивает основное окно программы в трей

- Tab: Перемещение между элементами:


6. Диалоговое окно О программе

- Отображает версию, разработчика, лицензию (кнопка «?»).

- Встроенная иконка приложения.

## Технические особенности
- Backend:

Работа с LDAP через библиотеку gopkg.in/ldap.v2.

- Интерфейс:

GTK3 (версия 3.24) + Go (модуль gotk3).

- Конфигурация:

Хранится в JSON-файле. Конфигурация ищется в трех местах. Имеется возможность как централизиованного указания настроек, так и персонального для конкретного пользователя

- Зависимости:

Только бинарный файл + конфиг (иконка встроена в код).

- Проверка запуска:

Блокировка повторного запуска через Unix-socket. Открывается ранее запущенное приложение

## Пример использования
Запуск:

Программа стартует и  открывает главное окно.

Поиск человека:

Ввести запрос в поле → нажать Enter → просмотреть карточку.

Поиск отдела:

Раскрыть дерево → выбрать отдел → в таблице появятся все сотрудники этого отдела.



## Преимущества
Минимализм: Один исполняемый файл + конфиг.

Гибкость: Настраиваемые колонки, сортировка, поиск.

Интеграция: Работает с любым LDAP-сервером (OpenLDAP, Active Directory).

Программа идеальна для корпоративных сред, где требуется быстрый доступ к контактам сотрудников без использования тяжеловесных решений вроде Outlook или веб-интерфейсов.


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



# Доп.настройки для LDAP-сервера


##Увеличение лимита

- Если openldap использует slapd.conf, добавить строку ниже в slapd.conf
```
sizelimit 5000
```
или
```
sizelimit unlimited
```

- Если openldap использует cn=config.ldif, добавить следующую конфигурацию

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

## Внесение настроек в конфигурацию LDAP-сервера
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

## Добавление скриптом
```
ldapadd -h localhost -p 389 -c -D cn=admin,dc=mail,dc=local  -w 123456 -f add.ldif
```

## Рекурсивное удаление
Пример удаления содержимого OU
```
ldapsearch -ZZ -W -D 'cn=Manager,dc=site,dc=fake' -b 'ou=people,dc=site,dc=fake' -s one  dn |\
 grep dn: | cut -b 5- | ldapdelete -ZZ -W -D 'cn=Manager,dc=site,dc=fake'
 ```


