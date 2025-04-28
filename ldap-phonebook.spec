# Добавить в секцию install:
install -m 644 ldap-phonebook.desktop %{buildroot}%{_datadir}/applications/
install -m 644 ldap-phonebook.ico %{buildroot}%{_datadir}/icons/hicolor/64x64/apps/

# Добавить в секцию files:
%{_datadir}/applications/ldap-phonebook.desktop
%{_datadir}/icons/hicolor/64x64/apps/ldap-phonebook.ico
