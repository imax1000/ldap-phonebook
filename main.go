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
	appVersion  = "1.0"
	defaultIcon = "system-users"
	configFile  = "ldap-phonebook.json"
	socketFile  = "/tmp/ldap-phonebook.sock"
)

type App struct {
	window      *gtk.Window
	indicator   *appindicator.Indicator
	isInTray    bool
	ldapConn    *ldap.Conn
	searchEntry *gtk.Entry
	listStore   *gtk.ListStore
	config      Config
	listener    net.Listener
	builder     *gtk.Builder
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
	app.createAppIndicator()
	app.startSocketServer()

	gtk.Main()

	// Очистка при выходе
	if app.listener != nil {
		app.listener.Close()
		os.Remove(socketFile)
	}
}

func (app *App) loadGladeUI() bool {
	// Пробуем найти файл Glade в разных местах
	gladePaths := []string{
		app.config.GladeFile,
		filepath.Join("/usr/share", appName, "ui.glade"),
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
		app.searchEntry, err = app.getEntry("search_entry")
		if err != nil {
			log.Println("Не найден search_entry:", err)
		}

		// Подключаем сигналы
		app.builder.ConnectSignals(map[string]interface{}{
			"on_window_delete_event": app.onWindowDelete,
			"on_connect_clicked":     app.onConnectClicked,
			"on_search_clicked":      app.onSearchClicked,
		})
	} else {
		// Стандартный интерфейс, если Glade не загружен
		app.createMainWindow()
		//	app.createDefaultUI()
	}

	app.window.SetTitle(fmt.Sprintf("LDAP Телефонный Справочник v%s", appVersion))
	app.window.SetDefaultSize(app.config.WindowWidth, app.config.WindowHeight)
	app.window.SetPosition(gtk.WIN_POS_CENTER)
	app.setWindowIcon()
	app.createAppIndicator()
	app.window.ShowAll()
}

func (app *App) getWindow(name string) (*gtk.Window, error) {
	obj, err := app.builder.GetObject(name)
	if err != nil {
		return nil, err
	}
	return obj.(*gtk.Window), nil
}

func (app *App) getEntry(name string) (*gtk.Entry, error) {
	obj, err := app.builder.GetObject(name)
	if err != nil {
		return nil, err
	}
	return obj.(*gtk.Entry), nil
}

func (app *App) onWindowDelete() bool {
	app.minimizeToTray()
	return true
}

func (app *App) onConnectClicked() {
	// Реализация обработчика кнопки подключения
}

func (app *App) createDefaultUI() {
	var err error
	app.window, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Не удалось создать окно:", err)
	}

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	app.window.Add(box)

	// Создаем стандартные элементы интерфейса
	// ... [как в предыдущих примерах] ...
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

			if string(buf[:n-1]) == "activate\n" {
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

	app.createConnectionPanel(box)
	app.createSearchPanel(box)
	app.createResultsView(box)
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

	app.listStore, _ = gtk.ListStoreNew(
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
		fmt.Sprintf("(&(objectClass=person)(|(cn=*%s*)(sn=*%s*)))", searchTerm, searchTerm),
		[]string{"cn", "title", "ou", "telephoneNumber"},
		nil,
	)

	result, err := app.ldapConn.Search(searchRequest)
	if err != nil {
		app.showError("Ошибка поиска", err.Error())
		return
	}

	for _, entry := range result.Entries {
		name := entry.GetAttributeValue("cn")
		title := entry.GetAttributeValue("title")
		dept := entry.GetAttributeValue("ou")
		phone := entry.GetAttributeValue("telephoneNumber")

		iter := app.listStore.Append()
		app.listStore.Set(iter,
			[]int{0, 1, 2, 3},
			[]interface{}{name, title, dept, phone},
		)
	}

	app.showMessage(fmt.Sprintf("Найдено %d записей", len(result.Entries)))
}

func (app *App) createAppIndicator() {
	iconName := defaultIcon
	if app.config.IconPath != "" {
		iconName = filepath.Base(app.config.IconPath)
	}

	app.indicator = appindicator.New(appName, iconName, appindicator.CategoryApplicationStatus)
	app.indicator.SetStatus(appindicator.StatusActive)

	menu, _ := gtk.MenuNew()

	showItem, _ := gtk.MenuItemNewWithLabel("Показать")
	showItem.Connect("activate", app.restoreFromTray)
	menu.Append(showItem)

	separator, _ := gtk.SeparatorMenuItemNew()
	menu.Append(separator)

	exitItem, _ := gtk.MenuItemNewWithLabel("Выход")
	exitItem.Connect("activate", func() {
		if app.listener != nil {
			app.listener.Close()
			os.Remove(socketFile)
		}
		app.saveConfig()
		gtk.MainQuit()
	})
	menu.Append(exitItem)

	menu.ShowAll()
	app.indicator.SetMenu(menu)
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

		//	glib.TimeoutAdd(100, func() bool {
		app.window.SetKeepAbove(false)
		//		return false
		//		})

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
