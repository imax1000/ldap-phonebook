package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/dawidd6/go-appindicator"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"gopkg.in/ldap.v2"
)

const (
	appName    = "ldap-phonebook"
	appVersion = "1.0"
	lockFile   = "/tmp/ldap-phonebook.lock"
	socketFile = "/tmp/ldap-phonebook.sock"
)

type App struct {
	window      *gtk.Window
	indicator   *appindicator.Indicator
	isInTray    bool
	ldapConn    *ldap.Conn
	searchEntry *gtk.Entry
	listStore   *gtk.ListStore
}

func main() {
	// Проверка на уже запущенный экземпляр
	if isAlreadyRunning() {
		log.Println("Приложение уже запущено. Активируем существующее окно...")
		activateExistingInstance()
		os.Exit(0)
	}

	// Создаем lock-файл
	if err := createLockFile(); err != nil {
		log.Fatal("Не удалось создать lock-файл:", err)
	}
	defer os.Remove(lockFile)

	gtk.Init(&os.Args)

	app := &App{}
	app.createMainWindow()
	app.createAppIndicator()
	app.setWindowIcon()

	gtk.Main()
}

//go:embed ldap-phonebook.png
var iconData []byte

func (app *App) setWindowIcon() {
	// Загрузка из встроенных ресурсов
	loader, err := gtk.PixbufLoaderNew()
	if err != nil {
		log.Println("Не удалось создать загрузчик иконки:", err)
		return
	}
	defer loader.Close()

	if _, err := loader.Write(iconData); err != nil {
		log.Println("Ошибка загрузки иконки:", err)
		return
	}

	if err := loader.Close(); err != nil {
		log.Println("Ошибка завершения загрузки иконки:", err)
		return
	}

	pixbuf := loader.GetPixbuf()
	if pixbuf != nil {
		app.window.SetIcon(pixbuf)
	}
}

func isAlreadyRunning() bool {
	if _, err := os.Stat(lockFile); err == nil {
		// Читаем PID из файла
		data, err := os.ReadFile(lockFile)
		if err != nil {
			return false
		}

		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return false
		}

		// Проверяем существует ли процесс
		process, err := os.FindProcess(pid)
		if err != nil {
			return false
		}

		// Проверяем что это наш процесс
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err == nil && strings.Contains(string(cmdline), filepath.Base(os.Args[0])) {
			return true
		}

		// Посылаем сигнал 0 для проверки существования процесса
		err = process.Signal(syscall.Signal(0))
		return err == nil
	}
	return false
}

func createLockFile() error {
	file, err := os.Create(lockFile)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(fmt.Sprintf("%d", os.Getpid()))
	return err
}

func activateExistingInstance() {
	// Используем netcat для отправки команды через unix socket
	cmd := exec.Command("sh", "-c", fmt.Sprintf("echo activate | nc -U %s", socketFile))
	cmd.Run()
}

func (app *App) createMainWindow() {
	var err error

	app.window, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Не удалось создать окно:", err)
	}

	app.window.SetTitle(fmt.Sprintf("LDAP Телефонный Справочник v%s", appVersion))
	app.window.SetDefaultSize(800, 600)
	app.window.SetPosition(gtk.WIN_POS_CENTER)
	app.window.SetIconName("system-users")

	// Обработчик закрытия окна (сворачиваем в трей)
	app.window.Connect("delete-event", func() bool {
		app.minimizeToTray()
		return true
	})

	app.createUI()
	app.window.ShowAll()

	// Запускаем сервер активации
	go app.runActivationServer()
}

func (app *App) runActivationServer() {
	os.Remove(socketFile)

	listener, err := net.Listen("unix", socketFile)
	if err != nil {
		log.Println("Ошибка создания unix socket:", err)
		return
	}
	defer listener.Close()
	defer os.Remove(socketFile)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}

		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			conn.Close()
			continue
		}

		if string(buf[:n-1]) == "activate" {
			glib.IdleAdd(func() bool {
				app.restoreFromTray()
				return false
			})
		}

		conn.Close()
	}
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
	grid.SetRowSpacing(6)
	grid.SetMarginTop(6)
	grid.SetMarginBottom(6)
	grid.SetMarginStart(6)
	grid.SetMarginEnd(6)
	frame.Add(grid)

	serverEntry, _ := gtk.EntryNew()
	serverEntry.SetText("localhost:389")

	bindEntry, _ := gtk.EntryNew()
	bindEntry.SetText("cn=admin,dc=example,dc=org")

	passwordEntry, _ := gtk.EntryNew()
	passwordEntry.SetText("123456")
	passwordEntry.SetVisibility(false)

	connectBtn, _ := gtk.ButtonNewWithLabel("Подключиться")
	connectBtn.Connect("clicked", func() {
		server, _ := serverEntry.GetText()
		bindDN, _ := bindEntry.GetText()
		password, _ := passwordEntry.GetText()

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
		app.showMessage("Успешно подключено к LDAP серверу")
	})

	grid.Attach(createLabel("Сервер:"), 0, 0, 1, 1)
	grid.Attach(serverEntry, 1, 0, 1, 1)
	grid.Attach(createLabel("Учетная запись:"), 0, 1, 1, 1)
	grid.Attach(bindEntry, 1, 1, 1, 1)
	grid.Attach(createLabel("Пароль:"), 0, 2, 1, 1)
	grid.Attach(passwordEntry, 1, 2, 1, 1)
	grid.Attach(connectBtn, 0, 3, 2, 1)
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
	//	if searchTerm == "" {
	//		app.showError("Ошибка", "Введите поисковый запрос")
	//		return
	//	}

	app.listStore.Clear()

	str := fmt.Sprintf("(&(objectClass=person)(|(cn=*%s*)(sn=*%s*)))", searchTerm, searchTerm)
	//	str = "(objectClass=person)"
	searchRequest := ldap.NewSearchRequest(
		"dc=example,dc=org",
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		str,
		[]string{"cn", "title", "ou", "telephoneNumber"},
		nil,
	)
	//	searchRequest := ldap.NewSearchRequest(
	//		"dc=example,dc=com",
	//		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
	//		fmt.Sprintf("(&(objectClass=user)(|(cn=*%s*)(sn=*%s*)))", searchTerm, searchTerm),
	//		[]string{"cn", "title", "department", "telephoneNumber"},
	//		nil,
	//	)

	result, err := app.ldapConn.Search(searchRequest)
	if err != nil {
		str := err.Error()
		app.showError("Ошибка поиска", str)
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
	app.indicator = appindicator.New(appName, "system-users", appindicator.CategoryApplicationStatus)
	app.indicator.SetStatus(appindicator.StatusActive)

	menu, _ := gtk.MenuNew()

	showItem, _ := gtk.MenuItemNewWithLabel("Показать")
	showItem.Connect("activate", app.restoreFromTray)
	menu.Append(showItem)

	separator, _ := gtk.SeparatorMenuItemNew()
	menu.Append(separator)

	exitItem, _ := gtk.MenuItemNewWithLabel("Выход")
	exitItem.Connect("activate", func() {
		os.Remove(lockFile)
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

		glib.TimeoutAdd(10, func() bool {
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
		message,
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
		message,
	)
	dialog.SetTitle("Сообщение")
	dialog.Run()
	dialog.Destroy()
}
