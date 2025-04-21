package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/dawidd6/go-appindicator"
	"github.com/godbus/dbus/v5"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"gopkg.in/ldap.v2"
)

type Config struct {
	LDAPServer    string `json:"ldap_server"`
	BindDN        string `json:"bind_dn"`
	User          string `json:"user"`
	Password      string `json:"password"`
	DefaultSearch string `json:"default_search"`
	WindowWidth   int    `json:"window_width"`
	WindowHeight  int    `json:"window_height"`
	IconPath      string `json:"icon_path"`
	GladeFile     string `json:"glade_file"`
}

const (
	appName     = "ldap-phonebook"
	appVersion  = "0.0.2"
	defaultIcon = "system-users"
	configFile  = "ldap-phonebook.json"
	socketFile  = "/tmp/ldap-phonebook.sock"
)

type App struct {
	window        *gtk.Window
	indicator     *appindicator.Indicator
	isInTray      bool
	ldapConn      *ldap.Conn
	serverEntry   *gtk.Entry
	bindEntry     *gtk.Entry
	userEntry     *gtk.Entry
	passwordEntry *gtk.Entry
	searchEntry   *gtk.Entry
	listStore     *gtk.ListStore
	config        Config
	listener      net.Listener
	builder       *gtk.Builder
	dbusConn      *dbus.Conn
}

// Employee представляет запись сотрудника в телефонной книге
type Employee struct {
	DN           string
	FullName     string
	Department   string
	Organization string
}

// DepartmentNode представляет узел дерева отделов
type DepartmentNode struct {
	Name      string
	Employees []Employee
	Children  []*DepartmentNode
}

func main() {
	// Проверяем запущен ли уже экземпляр
	if isInstanceRunning() {
		log.Println("Приложение уже запущено. Активируем существующее окно...")
		activateExistingInstance()
		os.Exit(0)
	}

	gtk.Init(&os.Args)

	app := &App{}
	app.loadConfig()

	if !app.loadGladeUI() {
		return
	}

	app.setupUI()
	app.startSocketServer()
	//	app.createAppIndicator()
	app.startSocketServer()
	app.ldapConnect(false)
	if app.ldapConn != nil {

		btn, _ := app.getButton("connect_button")
		btn.SetSensitive(false)
	}

	gtk.Main()

	if app.dbusConn != nil {
		app.dbusConn.Close()
	}

	// Очистка при выходе
	if app.listener != nil {
		app.listener.Close()
		os.Remove(socketFile)
	}
}

func (app *App) loadGladeUI() bool {
	// Пробуем найти файл Glade в разных местах
	gladePaths := []string{
		//		app.config.GladeFile,
		//		filepath.Join("/usr/share", appName, appName+".glade"),
		filepath.Join(filepath.Dir(os.Args[0]), "ui.glade"),
	}

	var builder *gtk.Builder
	//	var err error

	for _, path := range gladePaths {
		if _, err := os.Stat(path); err == nil {
			builder, err = gtk.BuilderNewFromFile(path)
			if err == nil {
				app.builder = builder
				return true
			}
			log.Printf("Ошибка загрузки Glade файла %s: %v\n", path, err)
		}
	}

	log.Println("Не удалось загрузить файл интерфейса. Используется стандартный интерфейс.")
	return false
}

func (app *App) setupUI() {
	var err error

	if app.builder != nil {
		// Загружаем главное окно из Glade
		app.window, err = app.getWindow("main_window")
		if err != nil {
			log.Fatal("Ошибка загрузки главного окна:", err)
		}

		// Получаем остальные элементы
		app.bindEntry, err = app.getEntry("bind_entry")
		if err != nil {
			log.Fatal("Не найден bind_entry:", err)
		}
		app.bindEntry.SetText(app.config.BindDN)

		app.serverEntry, err = app.getEntry("server_entry")
		if err != nil {
			log.Fatal("Не найден user_entry:", err)
		}
		app.serverEntry.SetText(app.config.LDAPServer)

		app.userEntry, err = app.getEntry("user_entry")
		if err != nil {
			log.Fatal("Не найден user_entry:", err)
		}
		app.userEntry.SetText(app.config.User)

		app.passwordEntry, err = app.getEntry("password_entry")
		if err != nil {
			log.Fatal("Не найден password_entry:", err)
		}
		app.passwordEntry.SetText(app.config.Password)

		app.searchEntry, err = app.getEntry("search_entry")
		if err != nil {
			log.Fatal("Не найден search_entry:", err)
		}
		app.searchEntry.SetText(app.config.DefaultSearch)

		// Получаем TreeView для результатов
		resultsView, err := app.getTreeView("results_view")
		if err != nil {
			log.Fatal("Не удалось получить TreeView:", err)
		}

		// Создаем модель данных для TreeView
		app.listStore, err = gtk.ListStoreNew(
			glib.TYPE_BOOLEAN, // Избранное
			glib.TYPE_STRING,  // ФИО
			glib.TYPE_STRING,  // Должность
			glib.TYPE_STRING,  // Должность
			glib.TYPE_STRING,  // Отдел
			glib.TYPE_STRING,  // Телефон
			glib.TYPE_STRING,  // Организация
		)
		if err != nil {
			log.Fatal("Не удалось создать ListStore:", err)
		}

		// Устанавливаем модель для TreeView
		resultsView.SetModel(app.listStore)

		// Добавляем колонки
		app.addColumn(resultsView, "Избранное", 0)
		app.addColumn(resultsView, "ФИО", 1)
		app.addColumn(resultsView, "EMail", 2)
		app.addColumn(resultsView, "Телефон", 3)
		app.addColumn(resultsView, "Отдел", 4)
		app.addColumn(resultsView, "Должность", 5)
		app.addColumn(resultsView, "Организация", 6)

		// Подключаем сигналы
		app.builder.ConnectSignals(map[string]interface{}{
			"on_window_delete_event": app.onWindowDelete,
			"on_connect_clicked":     app.onConnectClicked,
			"on_search_clicked":      app.onSearchClicked,
			"on_save_clicked":        app.onSaveClicked,
			"on_exit_clicked":        app.onExitClicked,
		})
	} else {
		// Стандартный интерфейс, если Glade не загружен
		//	app.createMainWindow()
		//	app.createDefaultUI()
	}

	app.window.SetTitle(fmt.Sprintf("LDAP Телефонный Справочник v%s", appVersion))
	app.window.SetDefaultSize(app.config.WindowWidth, app.config.WindowHeight)
	app.window.SetPosition(gtk.WIN_POS_CENTER)
	app.setWindowIcon()
	app.createAppIndicator()
	app.window.ShowAll()
}

// Вспомогательные методы для получения виджетов из билдера
func (app *App) getTreeView(name string) (*gtk.TreeView, error) {
	obj, err := app.builder.GetObject(name)
	if err != nil {
		return nil, err
	}
	return obj.(*gtk.TreeView), nil
}

func (app *App) getEntry(name string) (*gtk.Entry, error) {
	obj, err := app.builder.GetObject(name)
	if err != nil {
		return nil, err
	}
	return obj.(*gtk.Entry), nil
}

func (app *App) getButton(name string) (*gtk.Button, error) {
	obj, err := app.builder.GetObject(name)
	if err != nil {
		return nil, err
	}
	return obj.(*gtk.Button), nil
}

func (app *App) getWindow(name string) (*gtk.Window, error) {
	obj, err := app.builder.GetObject(name)
	if err != nil {
		return nil, err
	}
	return obj.(*gtk.Window), nil
}

func (app *App) addColumn(treeView *gtk.TreeView, title string, id int) {
	renderer, err := gtk.CellRendererTextNew()
	if err != nil {
		log.Printf("Ошибка создания рендерера для колонки %s: %v\n", title, err)
		return
	}

	column, err := gtk.TreeViewColumnNewWithAttribute(title, renderer, "text", id)
	if err != nil {
		log.Printf("Ошибка создания колонки %s: %v\n", title, err)
		return
	}

	column.SetResizable(true)
	column.SetSortColumnID(id)
	treeView.AppendColumn(column)
}

func (app *App) onWindowDelete() bool {
	app.minimizeToTray()
	return true
}
func (app *App) onExitClicked() {
	// Реализация обработчика кнопки
	if app.listener != nil {
		app.listener.Close()
		os.Remove(socketFile)
	}
	gtk.MainQuit()
}

func (app *App) onSaveClicked() {
	// Реализация обработчика кнопки подключения
	app.config.LDAPServer, _ = app.serverEntry.GetText()
	app.config.BindDN, _ = app.bindEntry.GetText()
	app.config.User, _ = app.userEntry.GetText()
	app.config.Password, _ = app.passwordEntry.GetText()
	app.config.DefaultSearch, _ = app.searchEntry.GetText()

	app.saveConfig()
}

func (app *App) onConnectClicked() {
	// Реализация обработчика кнопки подключения
	app.config.LDAPServer, _ = app.serverEntry.GetText()
	app.config.BindDN, _ = app.bindEntry.GetText()
	app.config.User, _ = app.userEntry.GetText()
	app.config.Password, _ = app.passwordEntry.GetText()

	app.ldapConnect(true)
	if app.ldapConn != nil {

		btn, _ := app.getButton("connect_button")
		btn.SetSensitive(false)
	}
}

func isInstanceRunning() bool {
	// Пытаемся подключиться к существующему сокету
	conn, err := net.Dial("unix", socketFile)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

func activateExistingInstance() {
	conn, err := net.Dial("unix", socketFile)
	if err != nil {
		log.Println("Ошибка подключения к сокету:", err)
		return
	}
	defer conn.Close()

	_, err = conn.Write([]byte("activate\n"))
	if err != nil {
		log.Println("Ошибка отправки команды:", err)
	}
}

func (app *App) startSocketServer() {
	// Удаляем старый сокет если существует
	os.Remove(socketFile)

	listener, err := net.Listen("unix", socketFile)
	if err != nil {
		log.Fatal("Ошибка создания сокета:", err)
	}
	app.listener = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !strings.Contains(err.Error(), "use of closed network connection") {
					log.Println("Ошибка принятия соединения:", err)
				}
				return
			}

			buf := make([]byte, 1024)
			n, err := conn.Read(buf)
			if err != nil {
				conn.Close()
				continue
			}

			if string(buf[:n]) == "activate\n" {
				glib.IdleAdd(func() bool {
					app.restoreFromTray()
					return false
				})
			}

			conn.Close()
		}
	}()
}

func (app *App) loadConfig() {
	configPaths := []string{
		filepath.Join("/etc", configFile),
		filepath.Join(os.Getenv("HOME"), ".config", appName, configFile),
		filepath.Join(filepath.Dir(os.Args[0]), configFile),
	}

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			data, err := os.ReadFile(path)
			if err != nil {
				log.Printf("Ошибка чтения конфига %s: %v\n", path, err)
				continue
			}

			if err := json.Unmarshal(data, &app.config); err != nil {
				log.Printf("Ошибка разбора конфига %s: %v\n", path, err)
				continue
			}

			log.Printf("Конфигурация загружена из %s\n", path)
			return
		}
	}

	// Значения по умолчанию
	app.config = Config{
		LDAPServer:    "localhost",
		BindDN:        "dc=example,dc=org",
		User:          "admin",
		Password:      "123456",
		DefaultSearch: "",
		WindowWidth:   800,
		WindowHeight:  600,
		IconPath:      "system-users",
	}
}

func (app *App) createMainWindow() {
	var err error

	app.window, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Не удалось создать окно:", err)
	}

	app.window.SetTitle(fmt.Sprintf("LDAP Телефонный Справочник v%s", appVersion))
	app.window.SetDefaultSize(app.config.WindowWidth, app.config.WindowHeight)
	app.window.SetPosition(gtk.WIN_POS_CENTER)

	app.setWindowIcon()

	app.window.Connect("delete-event", func() bool {
		app.minimizeToTray()
		return true
	})

	app.createUI()
	app.window.ShowAll()
}

func (app *App) setWindowIcon() {
	// Сначала пробуем загрузить иконку из указанного в конфиге пути
	/*	if app.config.IconPath != "" {
			if _, err := os.Stat(app.config.IconPath); err == nil {
				loader, err := gtk.PixbufLoaderNew()
				if err != nil {
					log.Printf("Ошибка создания загрузчика иконки: %v\n", err)
					goto useDefaultIcon
				}
				defer loader.Close()

				data, err := os.ReadFile(app.config.IconPath)
				if err != nil {
					log.Printf("Ошибка чтения файла иконки: %v\n", err)
					goto useDefaultIcon
				}

				if _, err := loader.Write(data); err != nil {
					log.Printf("Ошибка загрузки данных иконки: %v\n", err)
					goto useDefaultIcon
				}

				if err := loader.Close(); err != nil {
					log.Printf("Ошибка завершения загрузки иконки: %v\n", err)
					goto useDefaultIcon
				}

				pixbuf := loader.GetPixbuf()
				if pixbuf != nil {
					app.window.SetIcon(pixbuf)
					return
				}
			}
		}
	*/
	//useDefaultIcon:
	// Используем иконку из темы, если не удалось загрузить из файла
	app.window.SetIconName("system-users")
	// app.window.SetIconName(defaultIcon)
}

func (app *App) createUI() {
	box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	if err != nil {
		log.Fatal("Не удалось создать контейнер:", err)
	}
	app.window.Add(box)

	// app.createConnectionPanel(box)
	// app.createSearchPanel(box)
	// app.createResultsView(box)
}

func (app *App) createConnectionPanel(box *gtk.Box) {
	frame, err := gtk.FrameNew("Подключение к LDAP серверу")
	if err != nil {
		log.Fatal("Не удалось создать фрейм:", err)
	}
	box.PackStart(frame, false, false, 0)

	grid, err := gtk.GridNew()
	if err != nil {
		log.Fatal("Не удалось создать сетку:", err)
	}
	grid.SetColumnSpacing(6)
	grid.SetRowSpacing(8)
	grid.SetMarginTop(6)
	grid.SetMarginBottom(8)
	grid.SetMarginStart(6)
	grid.SetMarginEnd(6)
	frame.Add(grid)

	serverEntry, _ := gtk.EntryNew()
	serverEntry.SetText(app.config.LDAPServer)

	bindEntry, _ := gtk.EntryNew()
	bindEntry.SetText(app.config.BindDN)

	userEntry, _ := gtk.EntryNew()
	userEntry.SetText(app.config.User)

	passwordEntry, _ := gtk.EntryNew()
	passwordEntry.SetText(app.config.Password)
	passwordEntry.SetVisibility(false)

	connectBtn, _ := gtk.ButtonNewWithLabel("Подключиться")
	connectBtn.Connect("clicked", func() {

		app.config.LDAPServer, _ = serverEntry.GetText()
		app.config.BindDN, _ = bindEntry.GetText()
		app.config.Password, _ = passwordEntry.GetText()

		app.ldapConnect(true)
	})
	app.ldapConnect(false)
	connectBtn.SetSensitive(app.ldapConn == nil)

	grid.Attach(createLabel("Сервер:"), 0, 0, 1, 1)
	grid.Attach(serverEntry, 1, 0, 1, 1)
	grid.Attach(createLabel("BaseDN:"), 0, 1, 1, 1)
	grid.Attach(bindEntry, 1, 1, 1, 1)
	grid.Attach(createLabel("Учетная запись:"), 0, 2, 1, 1)
	grid.Attach(userEntry, 1, 2, 1, 1)
	grid.Attach(createLabel("Пароль:"), 0, 3, 1, 1)
	grid.Attach(passwordEntry, 1, 3, 1, 1)
	grid.Attach(connectBtn, 0, 4, 2, 1)
}
func (app *App) ldapConnect(showMessage bool) {
	server := app.config.LDAPServer
	bindDN := "cn=" + app.config.User + ", " + app.config.BindDN
	password := app.config.Password

	conn, err := ldap.Dial("tcp", server)
	if err != nil {
		app.showError("Ошибка подключения", err.Error())
		return
	}

	err = conn.Bind(bindDN, password)
	if err != nil {
		app.showError("Ошибка аутентификации", err.Error())
		return
	}

	app.ldapConn = conn
	if showMessage {
		app.showMessage("Успешно подключено к LDAP серверу")
	}

}

func (app *App) createSearchPanel(box *gtk.Box) {
	frame, _ := gtk.FrameNew("Поиск в справочнике")
	box.PackStart(frame, false, false, 0)

	hbox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
	hbox.SetMarginTop(6)
	hbox.SetMarginBottom(6)
	hbox.SetMarginStart(6)
	hbox.SetMarginEnd(6)
	frame.Add(hbox)

	app.searchEntry, _ = gtk.EntryNew()
	app.searchEntry.SetPlaceholderText("Введите имя или номер")
	if app.config.DefaultSearch != "" {
		app.searchEntry.SetText(app.config.DefaultSearch)
	}

	searchBtn, _ := gtk.ButtonNewWithLabel("Найти")
	searchBtn.Connect("clicked", app.onSearchClicked)

	hbox.PackStart(app.searchEntry, true, true, 0)
	hbox.PackStart(searchBtn, false, false, 0)
}

func (app *App) createResultsView(box *gtk.Box) {
	scrolled, _ := gtk.ScrolledWindowNew(nil, nil)
	box.PackStart(scrolled, true, true, 0)

	if app.listStore != nil {
		return
	}

	app.listStore, _ = gtk.ListStoreNew(
		glib.TYPE_STRING,
		glib.TYPE_STRING,
		glib.TYPE_STRING,
		glib.TYPE_STRING,
		glib.TYPE_STRING,
	)

	treeView, _ := gtk.TreeViewNewWithModel(app.listStore)
	treeView.SetHeadersVisible(true)
	scrolled.Add(treeView)

	addColumn(treeView, "ФИО", 0)
	addColumn(treeView, "Должность", 1)
	addColumn(treeView, "Отдел", 2)
	addColumn(treeView, "Телефон", 3)
	addColumn(treeView, "Орг-я", 4)
}

func (app *App) onSearchClicked() {
	if app.ldapConn == nil {
		app.showError("Ошибка", "Сначала подключитесь к LDAP серверу")
		return
	}

	searchTerm, _ := app.searchEntry.GetText()
	if searchTerm == "" {
		app.showError("Ошибка", "Введите поисковый запрос")
		return
	}

	app.listStore.Clear()

	searchRequest := ldap.NewSearchRequest(
		app.config.BindDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(&(objectClass=person)(|(cn=*%s*)(sn=*%s*)(telephoneNumber=*%s*)))", searchTerm, searchTerm, searchTerm),
		[]string{"cn", "mail", "o", "title", "ou", "telephoneNumber"},
		nil,
	)

	result, err := app.ldapConn.Search(searchRequest)
	if err != nil {
		app.showError("Ошибка поиска", err.Error())
		return
	}

	for _, entry := range result.Entries {
		name := entry.GetAttributeValue("cn")
		mail := entry.GetAttributeValue("mail")
		org := entry.GetAttributeValue("o")
		title := entry.GetAttributeValue("title")
		dept := entry.GetAttributeValue("ou")
		phone := entry.GetAttributeValue("telephoneNumber")
		org = strings.Replace(org, "lamb", "wolf", -1)

		iter := app.listStore.Append()
		app.listStore.Set(iter,
			[]int{1, 2, 3, 4, 5, 6},
			[]interface{}{name, mail, phone, dept, title, org},
		)
	}
	if len(result.Entries) == 0 {
		app.showMessage(fmt.Sprintf("Найдено %d записей", len(result.Entries)))
	}
}
func (app *App) createAppIndicator() {
	iconName := defaultIcon
	if app.config.IconPath != "" {
		iconName = filepath.Base(app.config.IconPath)
	}

	// Пробуем разные имена для индикатора, чтобы избежать конфликтов
	indicatorNames := []string{
		appName + "_" + fmt.Sprintf("%d", os.Getpid()),
		appName,
	}

	var indicator *appindicator.Indicator
	var err error

	for _, name := range indicatorNames {
		indicator = appindicator.New(name, iconName, appindicator.CategoryApplicationStatus)
		err = app.registerDBusService(name)
		if err == nil {
			break
		}
		log.Printf("Не удалось зарегистрировать индикатор с именем %s: %v\n", name, err)
	}

	if err != nil {
		log.Fatal("Не удалось создать индикатор:", err)
	}

	app.indicator = indicator
	app.indicator.SetStatus(appindicator.StatusActive)

	// Создаем отдельное окно для обработки событий
	eventWindow, _ := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	eventWindow.Connect("button-press-event", func(win *gtk.Window, event *gdk.Event) {
		btnEvent := gdk.EventButtonNewFromEvent(event)
		if btnEvent.Button() == 1 && btnEvent.Type() == gdk.EVENT_2BUTTON_PRESS {
			if app.isInTray {
				app.restoreFromTray()
			} else {
				app.minimizeToTray()
			}
		}
	})

	// Создаем контекстное меню
	menu, _ := gtk.MenuNew()

	// Добавляем обработчик двойного клика через меню
	menu.Connect("button-press-event", func(menu *gtk.Menu, event *gdk.Event) {
		btnEvent := gdk.EventButtonNewFromEvent(event)
		if btnEvent.Button() == 1 && btnEvent.Type() == gdk.EVENT_2BUTTON_PRESS {
			if app.isInTray {
				app.restoreFromTray()
			} else {
				app.minimizeToTray()
			}
		}
	})

	showItem, _ := gtk.MenuItemNewWithLabel("Показать")
	showItem.Connect("activate", app.restoreFromTray)
	menu.Append(showItem)

	separator, _ := gtk.SeparatorMenuItemNew()
	menu.Append(separator)

	exitItem, _ := gtk.MenuItemNewWithLabel("Выход")
	exitItem.Connect("activate", func() {
		app.onExitClicked()
	})
	menu.Append(exitItem)

	menu.ShowAll()
	app.indicator.SetMenu(menu)
}
func (app *App) registerDBusService(name string) error {
	if app.dbusConn != nil {
		app.dbusConn.Close()
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("не удалось подключиться к D-Bus: %v", err)
	}

	// Освобождаем имя если оно уже занято
	_, err = conn.ReleaseName("org.kde.StatusNotifierItem." + name)
	if err != nil {
		log.Printf("Не удалось освободить имя сервиса: %v\n", err)
	}

	_, err = conn.RequestName("org.kde.StatusNotifierItem."+name, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return fmt.Errorf("не удалось зарегистрировать имя сервиса: %v", err)
	}

	app.dbusConn = conn
	return nil
}
func (app *App) minimizeToTray() {
	app.window.Hide()
	app.isInTray = true
}

func (app *App) restoreFromTray() {
	if app.window != nil {
		app.window.Present()
		app.window.Show()
		app.window.Deiconify()
		app.window.SetKeepAbove(true)

		glib.TimeoutAdd(100, func() bool {
			app.window.SetKeepAbove(false)
			return false
		})

		app.isInTray = false
	}
}

func createLabel(text string) *gtk.Label {
	label, err := gtk.LabelNew(text)
	if err != nil {
		log.Fatal("Не удалось создать метку:", err)
	}
	label.SetHAlign(gtk.ALIGN_START)
	return label
}

func addColumn(treeView *gtk.TreeView, title string, id int) {
	renderer, _ := gtk.CellRendererTextNew()
	column, _ := gtk.TreeViewColumnNewWithAttribute(title, renderer, "text", id)
	column.SetResizable(true)
	column.SetSortColumnID(id)
	treeView.AppendColumn(column)
}

func (app *App) showError(title, message string) {
	dialog := gtk.MessageDialogNew(
		app.window,
		gtk.DIALOG_MODAL,
		gtk.MESSAGE_ERROR,
		gtk.BUTTONS_OK,
		"%s", message,
	)
	dialog.SetTitle(title)
	dialog.Run()
	dialog.Destroy()
}

func (app *App) showMessage(message string) {
	dialog := gtk.MessageDialogNew(
		app.window,
		gtk.DIALOG_MODAL,
		gtk.MESSAGE_INFO,
		gtk.BUTTONS_OK,
		"%s", message,
	)
	dialog.SetTitle("Сообщение")
	dialog.Run()
	dialog.Destroy()
}
func (app *App) saveConfig() error {
	configDir := filepath.Join(os.Getenv("HOME"), ".config", appName)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configPath := filepath.Join(configDir, configFile)
	data, err := json.MarshalIndent(app.config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}
