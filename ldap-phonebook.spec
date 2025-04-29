Name:           ldap-phonebook
Version:        0.7
Release:        1%{?dist}
Summary:        LDAP Phonebook Application

License:        MIT X11 license
URL:            https://github.com/imax1000/ldap-phonebook
Source0:        %{name}.tar.gz

#BuildRequires:  golang
#Requires:       

%description
Program for searching contact data in LDAP.

%prep
tar -xzf %{SOURCE0}
#mkdir -p %{name}-%{version}
#tar -C %{name}-%{version} -xzf %{SOURCE0}
#mkdir -p %{name}-%{version}

%build
#go build -o %{name}

%install
mkdir -p %{buildroot}/opt/filial/bin
mkdir -p %{buildroot}/etc/ldap-phonebook
mkdir -p %{buildroot}/usr/share/icons
mkdir -p %{buildroot}/usr/share/applications

install -m 755 %{name} %{buildroot}/opt/filial/bin/%{name}
install -m 644 %{name}.json %{buildroot}/etc/ldap-phonebook/
install -m 644 %{name}.ico %{buildroot}/usr/share/icons/

# Create desktop file
cat > %{buildroot}/usr/share/applications/%{name}.desktop <<EOF
[Desktop Entry]
Name=LDAP Phonebook
Comment=LDAP Phone Directory
Exec=/opt/filial/bin/%{name}
Icon=/usr/share/icons/%{name}.ico
Terminal=false
Type=Application
Categories=Office;Utility;Network;
EOF

%postun
# Удаляем пустые директории после удаления пакета
if [ $1 -eq 0 ]; then
    # Проверяем и удаляем /opt/filial/bin если он пустой
    if [ -d "/opt/filial/bin" ] && [ -z "$(ls -A /opt/filial/bin)" ]; then
        rmdir /opt/filial/bin
        # Если родительская директория тоже пуста, удаляем и её
        if [ -d "/opt/filial" ] && [ -z "$(ls -A /opt/filial)" ]; then
            rmdir /opt/filial
        fi
    fi
    
    # Проверяем и удаляем /etc/ldap-phonebook если он пустой
    if [ -d "/etc/ldap-phonebook" ] && [ -z "$(ls -A /etc/ldap-phonebook)" ]; then
        rmdir /etc/ldap-phonebook
    fi
fi

%posttrans
# Дополнительная проверка после всех транзакций
if [ $1 -eq 0 ]; then
    # Повторяем проверку для /opt/filial/bin
    if [ -d "/opt/filial/bin" ] && [ -z "$(ls -A /opt/filial/bin)" ]; then
        rmdir /opt/filial/bin
        if [ -d "/opt/filial" ] && [ -z "$(ls -A /opt/filial)" ]; then
            rmdir /opt/filial
        fi
    fi
    
    # Повторяем проверку для /etc/ldap-phonebook
    if [ -d "/etc/ldap-phonebook" ] && [ -z "$(ls -A /etc/ldap-phonebook)" ]; then
        rmdir /etc/ldap-phonebook
    fi
fi


%files
%attr(755,root,root) /opt/filial/bin/%{name}
%attr(644,root,root) /etc/ldap-phonebook/%{name}.json
%attr(644,root,root) /usr/share/icons/%{name}.ico
%attr(644,root,root) /usr/share/applications/%{name}.desktop

%changelog
* Thu May 01 2025 Maxim Izvekov <maximizvekov@yandex.ru> - %{version}-1
- Initial package
