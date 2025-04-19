# Добавить в секцию install:
install -m 644 ldap-phonebook-go.desktop %{buildroot}%{_datadir}/applications/
install -m 644 ldap-phonebook.png %{buildroot}%{_datadir}/icons/hicolor/64x64/apps/

# Добавить в секцию files:
%{_datadir}/applications/ldap-phonebook-go.desktop
%{_datadir}/icons/hicolor/64x64/apps/ldap-phonebook.png
