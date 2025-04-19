package main

import (
	"fmt"
	"log"
	"os"

	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/glib"
	"gopkg.in/ldap.v2"
)

// App структура для хранения состояния приложения
type App struct {
	window      *gtk.Window
	statusIcon  *gtk.StatusIcon
	trayMenu    *gtk.Menu
	isInTray    bool
	ldapConn    *ldap.Conn
	searchEntry *gtk.Entry
	listStore   *gtk.ListStore
}

func main() {
	// Инициализация GTK
	gtk.Init(&os.Args)

	// Создание экземпляра приложения
	app := &App{}

	// Создание главного окна
	app.createMainWindow()

	// Создание иконки в трее
	app.createStatusIcon()

	// Запуск главного цикла GTK
	gtk.Main()
}

func (app *App) createMainWindow() {
	var err error

	// Создание главного окна
	app.window, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Не удалось создать окно:", err)
	}

	app.window.SetTitle("LDAP Телефонный Справочник (Go)")
	app.window.SetDefaultSize(800, 600)
	app.window.SetPosition(gtk.WIN_POS_CENTER)

	// Обработчик закрытия окна (будем сворачивать в трей)
	app.window.Connect("delete-event", func() bool {
		app.minimizeToTray()
		return true // Предотвращаем закрытие окна
	})

	// Основной интерфейс
	app.createUI()

	// Показываем все виджеты
	app.window.ShowAll()
}

func (app *App) createUI() {
	// Вертикальный контейнер
	box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	if err != nil {
		log.Fatal("Не удалось создать контейнер:", err)
	}
	app.window.Add(box)

	// Панель подключения
	app.createConnectionPanel(box)

	// Панель поиска
	app.createSearchPanel(box)

	// Отображение результатов
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

	// Элементы для подключения
	serverEntry, _ := gtk.EntryNew()
	serverEntry.SetText("ldap://localhost")

	bindEntry, _ := gtk.EntryNew()
	bindEntry.SetText("cn=admin,dc=example,dc=com")

	passwordEntry, _ := gtk.EntryNew()
	passwordEntry.SetVisibility(false)

	connectBtn, _ := gtk.ButtonNewWithLabel("Подключиться")
	connectBtn.Connect("clicked", func() {
		server := serverEntry.GetText()
		bindDN := bindEntry.GetText()
		password := passwordEntry.GetText()

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

	// Размещение элементов
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

	// Создаем модель данных
	app.listStore, _ = gtk.ListStoreNew(
		glib.TYPE_STRING, // ФИО
		glib.TYPE_STRING, // Должность
		glib.TYPE_STRING, // Отдел
		glib.TYPE_STRING, // Телефон
	)

	// Создаем TreeView
	treeView, _ := gtk.TreeViewNewWithModel(app.listStore)
	treeView.SetHeadersVisible(true)
	scrolled.Add(treeView)

	// Добавляем колонки
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

	searchTerm := app.searchEntry.GetText()
	if searchTerm == "" {
		app.showError("Ошибка", "Введите поисковый запрос")
		return
	}

	// Очищаем предыдущие результаты
	app.listStore.Clear()

	// Выполняем поиск
	searchRequest := ldap.NewSearchRequest(
		"dc=example,dc=com",
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(|(cn=*%s*)(telephoneNumber=*%s*))", searchTerm, searchTerm),
		[]string{"cn", "title", "department", "telephoneNumber"},
		nil,
	)

	result, err := app.ldapConn.Search(searchRequest)
	if err != nil {
		app.showError("Ошибка поиска", err.Error())
		return
	}

	// Заполняем результаты
	for _, entry := range result.Entries {
		name := entry.GetAttributeValue("cn")
		title := entry.GetAttributeValue("title")
		dept := entry.GetAttributeValue("department")
		phone := entry.GetAttributeValue("telephoneNumber")

		iter := app.listStore.Append()
		app.listStore.Set(iter,
			[]int{0, 1, 2, 3},
			[]interface{}{name, title, dept, phone},
		)
	}

	app.showMessage(fmt.Sprintf("Найдено %d записей", len(result.Entries)))
}

// Создание иконки в трее
func (app *App) createStatusIcon() {
	statusIcon, err := gtk.StatusIconNew()
	if err != nil {
		log.Fatal("Не удалось создать иконку в трее:", err)
	}
	app.statusIcon = statusIcon

	// Устанавливаем иконку (должна быть в путях или указать полный путь)
	app.statusIcon.SetFromIconName("system-users")
	app.statusIcon.SetTooltipText("LDAP Телефонный Справочник")

	// Обработчик клика по иконке
	app.statusIcon.Connect("activate", func() {
		if app.isInTray {
			app.restoreFromTray()
		} else {
			app.minimizeToTray()
		}
	})

	// Создаем контекстное меню для иконки в трее
	app.createTrayMenu()
}

// Создание меню для иконки в трее
func (app *App) createTrayMenu() {
	menu, err := gtk.MenuNew()
	if err != nil {
		log.Fatal("Не удалось создать меню:", err)
	}
	app.trayMenu = menu

	// Пункт "Показать"
	showItem, _ := gtk.MenuItemNewWithLabel("Показать")
	showItem.Connect("activate", app.restoreFromTray)
	menu.Append(showItem)

	// Разделитель
	separator, _ := gtk.SeparatorMenuItemNew()
	menu.Append(separator)

	// Пункт "Выход"
	exitItem, _ := gtk.MenuItemNewWithLabel("Выход")
	exitItem.Connect("activate", func() {
		gtk.MainQuit()
	})
	menu.Append(exitItem)

	menu.ShowAll()
}

// Сворачивание в трей
func (app *App) minimizeToTray() {
	app.window.Hide()
	app.isInTray = true
	app.statusIcon.SetVisible(true)
}

// Восстановление из трея
func (app *App) restoreFromTray() {
	app.window.Present()
	app.window.Show()
	app.isInTray = false
	app.statusIcon.SetVisible(false)
}

// Вспомогательные функции
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
	column.SetSortColumnId(id)
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
	// Можно реализовать через статусбар или уведомление
	log.Println(message)
}
