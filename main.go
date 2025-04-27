package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/dawidd6/go-appindicator"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"gopkg.in/ldap.v2"
)

// Config структура для хранения конфигурации
type Config struct {
	LDAPServer   string `json:"ldap_server"`
	BindDN       string `json:"bind_dn"`
	BindPassword string `json:"bind_password"`
	BaseDN       string `json:"base_dn"`
}

var (
	config        Config
	mainWindow    *gtk.Window
	treeView      *gtk.TreeView
	searchEntry   *gtk.Entry
	resultsView   *gtk.TreeView
	detailsView   *gtk.TextView
	detailsBuffer *gtk.TextBuffer
	indicator     *appindicator.Indicator
	searchResult  []LDIFEntry

	isInTray bool
)

// LDIFEntry represents a single LDAP entry from the LDIF file
type LDIFEntry struct {
	DN              string
	ObjectClass     string
	SN              string
	CN              string
	OU              string
	Title           string
	Mail            string
	GivenName       string
	Initials        string
	TelephoneNumber string
	L               string
	PostalAddress   string
	O               string
}

// OrgNode represents a node in the organizational tree
type OrgNode struct {
	Name     string
	Children map[string]*OrgNode
}

const (
	appName     = "ldap-phonebook"
	appVersion  = "0.2"
	defaultIcon = "ldap-phonebook.ico"
	configFile  = "ldap-phonebook.json"
	socketFile  = "/tmp/ldap-phonebook.sock"
)

func main() {

	// Проверяем, не запущен ли уже экземпляр программы
	if isAlreadyRunning() {
		fmt.Println("Программа уже запущена. Активируем существующий экземпляр...")
		activateExistingInstance()
		os.Exit(0)
	}

	// Создаем конфиг по умолчанию, если файл не существует
	config = Config{
		LDAPServer:   "localhost:389",
		BindDN:       "dc=mail,dc=local",
		BindPassword: "",
		BaseDN:       "dc=mail,dc=local",
	}

	// Загружаем конфигурацию
	loadConfig()

	isInTray = false

	// Инициализируем GTK
	gtk.Init(nil)

	// Создаем главное окно
	createMainWindow()

	// Создаем иконку в трее
	createStatusIndicator()

	// Запускаем Unix socket сервер
	go startUnixSocketServer()

	// Обработка сигналов для корректного завершения
	setupSignalHandler()

	// Показываем все виджеты
	mainWindow.ShowAll()

	// Загружаем данные из LDAP
	go loadLDAPData()

	// Главный цикл GTK
	gtk.Main()
}

func loadConfig() {
	// Определяем путь к конфигурационному файлу
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	configPath := filepath.Join(configDir, "ldap-phonebook", "config.json")

	// Читаем конфигурационный файл
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		// Создаем конфиг по умолчанию, если файл не существует
		//		config = Config{
		//			LDAPServer:   "localhost:389",
		//			BindDN:       "dc=mail,dc=local",
		//			BindPassword: "123456",
		//			BaseDN:       "dc=mail,dc=local",
		//		}

		// Создаем директорию, если ее нет
		os.MkdirAll(filepath.Dir(configPath), 0755)

		// Сохраняем конфиг по умолчанию
		data, _ := json.MarshalIndent(config, "", "  ")
		ioutil.WriteFile(configPath, data, 0644)
		return
	}

	// Парсим конфигурацию
	err = json.Unmarshal(data, &config)
	if err != nil {
		fmt.Printf("Ошибка чтения конфигурации: %v\n", err)
		os.Exit(1)
	}

}

func createMainWindow() {
	var err error

	// Создаем главное окно
	mainWindow, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		fmt.Printf("Ошибка создания окна: %v\n", err)
		os.Exit(1)
	}

	mainWindow.SetTitle("LDAP Телефонный справочник" + " v." + appVersion)
	mainWindow.SetDefaultSize(1200, 600)
	mainWindow.Connect("destroy", func() {
		gtk.MainQuit()
	})
	setWindowIcon()

	mainWindow.Connect("delete-event", func() bool {
		minimizeToTray()
		return true
	})

	// Создаем основной контейнер с разделителем
	mainPaned, err := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	if err != nil {
		fmt.Printf("Ошибка создания контейнера: %v\n", err)
		os.Exit(1)
	}

	// Левая панель - дерево организаций и отделов (25% ширины)
	leftPanel, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	if err != nil {
		fmt.Printf("Ошибка создания левой панели: %v\n", err)
		os.Exit(1)
	}

	// Создаем дерево
	treeView, err = gtk.TreeViewNew()
	if err != nil {
		fmt.Printf("Ошибка создания дерева: %v\n", err)
		os.Exit(1)
	}

	// Настройка модели дерева
	treeStore, err := gtk.TreeStoreNew(glib.TYPE_STRING)
	if err != nil {
		fmt.Printf("Ошибка создания модели дерева: %v\n", err)
		os.Exit(1)
	}

	treeView.SetModel(treeStore)

	// Добавляем колонку
	renderer, err := gtk.CellRendererTextNew()
	if err != nil {
		fmt.Printf("Ошибка создания рендерера: %v\n", err)
		os.Exit(1)
	}

	column, err := gtk.TreeViewColumnNewWithAttribute("Организации и отделы", renderer, "text", 0)
	if err != nil {
		fmt.Printf("Ошибка создания колонки: %v\n", err)
		os.Exit(1)
	}

	treeView.AppendColumn(column)
	treeView.SetEnableSearch(false)

	// Контекстное меню для дерева
	treeView.Connect("button-press-event", func(v *gtk.TreeView, ev *gdk.Event) {
		event := gdk.EventButtonNewFromEvent(ev)
		if event.Button() == 3 { // Правая кнопка мыши
			menu, err := gtk.MenuNew()
			if err != nil {
				return
			}

			expandItem, err := gtk.MenuItemNewWithLabel("Развернуть все")
			if err != nil {
				return
			}

			collapseItem, err := gtk.MenuItemNewWithLabel("Свернуть все")
			if err != nil {
				return
			}

			expandItem.Connect("activate", func() {
				treeView.ExpandAll()
			})

			collapseItem.Connect("activate", func() {
				treeView.CollapseAll()
			})

			menu.Append(expandItem)
			menu.Append(collapseItem)
			menu.ShowAll()
			menu.PopupAtPointer(ev)
		}
	})

	// Добавляем дерево в прокручиваемую область
	scrolledWindow, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		fmt.Printf("Ошибка создания прокручиваемой области: %v\n", err)
		os.Exit(1)
	}
	scrolledWindow.SetSizeRequest(330, -1)

	scrolledWindow.Add(treeView)
	leftPanel.PackStart(scrolledWindow, true, true, 0)

	// Центральная панель - вертикальный контейнер
	centerPanel, err := gtk.PanedNew(gtk.ORIENTATION_VERTICAL)
	if err != nil {
		fmt.Printf("Ошибка создания центральной панели: %v\n", err)
		os.Exit(1)
	}

	// Панель поиска
	searchPanel, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	if err != nil {
		fmt.Printf("Ошибка создания панели поиска: %v\n", err)
		os.Exit(1)
	}

	// Статический текст над строкой поиска
	searchLabel, err := gtk.LabelNew("Панель поиска")
	if err != nil {
		fmt.Printf("Ошибка создания метки: %v\n", err)
		os.Exit(1)
	}
	searchLabel.SetHAlign(gtk.ALIGN_START)
	searchPanel.PackStart(searchLabel, false, false, 0)

	// Горизонтальная панель с элементами поиска
	searchBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		fmt.Printf("Ошибка создания панели поиска: %v\n", err)
		os.Exit(1)
	}

	searchEntry, err = gtk.EntryNew()
	if err != nil {
		fmt.Printf("Ошибка создания поля поиска: %v\n", err)
		os.Exit(1)
	}
	searchEntry.SetProperty("can-focus", true)
	searchEntry.SetProperty("focus-on-click", true)

	searchEntry.SetPlaceholderText("Поиск по ФИО, email, телефону...")

	searchButton, err := gtk.ButtonNewWithLabel("Поиск")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки поиска: %v\n", err)
		os.Exit(1)
	}

	searchButton.Connect("clicked", func() {
		go performSearch()
	})
	// Обработка нажатия Enter в поле поиска
	searchEntry.Connect("activate", func() {
		go performSearch()
	})

	exitButton, err := gtk.ButtonNewWithLabel("Выход")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки выхода: %v\n", err)
		os.Exit(1)
	}

	exitButton.Connect("clicked", func() {
		gtk.MainQuit()
	})

	helpButton, err := gtk.ButtonNewWithLabel("?")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки помощи: %v\n", err)
		os.Exit(1)
	}

	// Настройка порядка табуляции
	searchEntry.SetProperty("can-focus", true)
	searchButton.SetProperty("can-focus", true)
	exitButton.SetProperty("can-focus", true)
	helpButton.SetProperty("can-focus", true)

	searchBox.PackStart(searchEntry, true, true, 0)
	searchBox.PackStart(searchButton, false, false, 0)
	searchBox.PackStart(exitButton, false, false, 0)
	searchBox.PackStart(helpButton, false, false, 0)

	searchPanel.PackStart(searchBox, false, false, 0)
	//topCenterBox.PackStart(searchPanel, false, false, 0)

	searchBox.PackStart(searchEntry, true, true, 0)
	searchBox.PackStart(searchButton, false, false, 0)
	searchBox.PackStart(exitButton, false, false, 0)

	// Центральная часть - результаты поиска
	resultsView, err = gtk.TreeViewNew()
	if err != nil {
		fmt.Printf("Ошибка создания таблицы результатов: %v\n", err)
		os.Exit(1)
	}

	// Включаем возможность изменения ширины колонок
	resultsView.SetProperty("headers-clickable", true)
	resultsView.SetProperty("reorderable", true)

	// Настройка модели результатов
	listStore, err := gtk.ListStoreNew(
		glib.TYPE_STRING, // ФИО
		glib.TYPE_STRING, // Email
		glib.TYPE_STRING, // Телефон
		glib.TYPE_STRING, // Отдел
		glib.TYPE_STRING, // Организация
	)
	if err != nil {
		fmt.Printf("Ошибка создания модели результатов: %v\n", err)
		os.Exit(1)
	}

	resultsView.SetModel(listStore)

	resultsView.SetEnableSearch(false)
	// Добавляем колонки
	//	addColumn(resultsView, "ФИО", 0)
	//	addColumn(resultsView, "Email", 1)
	//	addColumn(resultsView, "Телефон", 2)
	//	addColumn(resultsView, "Отдел", 3)
	// addColumn(resultsView, "Организация", 4)

	// Добавляем колонки с возможностью изменения ширины
	addResizableColumn(resultsView, "ФИО", 0)
	addResizableColumn(resultsView, "Email", 1)
	addResizableColumn(resultsView, "Телефон", 2)
	addResizableColumn(resultsView, "Отдел", 3)
	addResizableColumn(resultsView, "Организация", 4)

	// Прокручиваемая область для результатов
	resultsScrolled, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		fmt.Printf("Ошибка создания прокручиваемой области результатов: %v\n", err)
		os.Exit(1)
	}
	//	resultsScrolled.SetSizeRequest(-1, 500)
	resultsScrolled.Add(resultsView)
	resultsScrolled.SetSizeRequest(-1, 370)

	// Контейнер для верхней и центральной части
	topCenterBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	if err != nil {
		fmt.Printf("Ошибка создания контейнера: %v\n", err)
		os.Exit(1)
	}

	topCenterBox.PackStart(searchPanel, false, false, 0)
	topCenterBox.PackStart(searchBox, false, false, 0)
	topCenterBox.PackStart(resultsScrolled, true, true, 0)

	// Нижняя часть - детальная информация (20% высоты или минимум 100px)
	detailsBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	if err != nil {
		fmt.Printf("Ошибка создания контейнера деталей: %v\n", err)
		os.Exit(1)
	}

	detailsLabel, err := gtk.LabelNew("Детальная информация:")
	if err != nil {
		fmt.Printf("Ошибка создания метки: %v\n", err)
		os.Exit(1)
	}

	detailsBox.PackStart(detailsLabel, false, false, 0)

	// Текстовый виджет для детальной информации
	detailsView, err = gtk.TextViewNew()
	if err != nil {
		fmt.Printf("Ошибка создания текстового виджета: %v\n", err)
		os.Exit(1)
	}

	detailsView.SetEditable(false)
	detailsView.SetWrapMode(gtk.WRAP_WORD)

	detailsBuffer, err = detailsView.GetBuffer()
	if err != nil {
		fmt.Printf("Ошибка получения буфера: %v\n", err)
		os.Exit(1)
	}

	// Прокручиваемая область для детальной информации
	detailsScrolled, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		fmt.Printf("Ошибка создания прокручиваемой области деталей: %v\n", err)
		os.Exit(1)
	}

	detailsScrolled.Add(detailsView)
	detailsBox.PackStart(detailsScrolled, true, true, 0)

	// Устанавливаем минимальный размер для нижней панели
	detailsBox.SetSizeRequest(-1, 120)

	// Добавляем части в вертикальный разделитель
	centerPanel.Pack1(topCenterBox, true, false)
	centerPanel.Pack2(detailsBox, false, false)
	centerPanel.SetPosition(int(float64(mainWindow.GetAllocatedHeight()) * 0.8))

	// Добавляем панели в горизонтальный разделитель
	mainPaned.Pack1(leftPanel, false, false)
	mainPaned.Pack2(centerPanel, true, false)
	mainPaned.SetPosition(int(float64(mainWindow.GetAllocatedWidth()) * 0.5))

	// Настройка обработчиков событий
	setupEventHandlers()
	helpButton.Connect("clicked", showAboutDialog)

	searchEntry.GrabFocus()

	// Добавляем главный контейнер в окно
	mainWindow.Add(mainPaned)

}

func showAboutDialog() {
	dialog, err := gtk.AboutDialogNew()
	if err != nil {
		fmt.Printf("Ошибка создания диалога: %v\n", err)
		return
	}
	dialog.SetTitle("О программе")

	dialog.SetProgramName(appName)
	dialog.SetVersion(appVersion)
	dialog.SetCopyright("© 2025 Maxim Izvekov")
	dialog.SetComments("Программа для поиска контактных данных в LDAP")
	//	dialog.SetLicense("Лицензия: MIT")
	dialog.SetLicenseType(gtk.LICENSE_MIT_X11)
	dialog.SetWebsite("https://github.com/imax1000/ldap-phonebook")
	dialog.SetWebsiteLabel("Официальный сайт")

	// Загрузка иконки
	pixbuf, err := gdk.PixbufNewFromFile("ldap-phonebook.ico")
	if err == nil {
		dialog.SetLogo(pixbuf)
	}

	dialog.Run()
	dialog.Destroy()
}

func onWindowDelete() bool {
	minimizeToTray()
	return true
}

func minimizeToTray() {
	mainWindow.Hide()
	isInTray = true
}

func restoreFromTray() {
	if mainWindow != nil {
		mainWindow.Present()
		mainWindow.Show()
		mainWindow.Deiconify()
		mainWindow.SetKeepAbove(true)

		glib.TimeoutAdd(100, func() bool {
			mainWindow.SetKeepAbove(false)
			return false
		})

		isInTray = false
	}
}

func createStatusIndicator() {
	indicator = appindicator.New(appName, defaultIcon, appindicator.CategoryApplicationStatus)
	indicator.SetStatus(appindicator.StatusActive)
	indicator.SetTitle(appName)

	// Создаем меню
	menu, err := gtk.MenuNew()
	if err != nil {
		fmt.Printf("Ошибка создания меню: %v\n", err)
		return
	}

	// Пункт "заголовок"
	appItem, err := gtk.MenuItemNewWithLabel(appName + " v." + appVersion)
	if err != nil {
		fmt.Printf("Ошибка создания пункта меню: %v\n", err)
		return
	}
	appItem.SetSensitive(false)
	menu.Append(appItem)

	// Пункт "Показать"
	showItem, err := gtk.MenuItemNewWithLabel("Показать")
	if err != nil {
		fmt.Printf("Ошибка создания пункта меню: %v\n", err)
		return
	}

	showItem.Connect("activate", restoreFromTray)

	menu.Append(showItem)

	// Пункт "Выход"
	exitItem, err := gtk.MenuItemNewWithLabel("Выход")
	if err != nil {
		fmt.Printf("Ошибка создания пункта меню: %v\n", err)
		return
	}

	exitItem.Connect("activate", func() {
		gtk.MainQuit()
	})

	menu.Append(exitItem)

	menu.ShowAll()
	indicator.SetMenu(menu)

	// Обработка двойного клика
	exitItem.Connect("activate", func() {
		if mainWindow.GetVisible() {
			mainWindow.Hide()
		} else {
			mainWindow.Present()
		}
	})
}

// Добавляем колонку с возможностью изменения ширины
func addResizableColumn(treeView *gtk.TreeView, title string, id int) {
	renderer, err := gtk.CellRendererTextNew()
	if err != nil {
		fmt.Printf("Ошибка создания рендерера для колонки: %v\n", err)
		return
	}

	column, err := gtk.TreeViewColumnNewWithAttribute(title, renderer, "text", id)
	if err != nil {
		fmt.Printf("Ошибка создания колонки %s: %v\n", title, err)
		return
	}

	// Включаем возможность изменения размера колонки
	column.SetResizable(true)
	column.SetReorderable(true)
	column.SetClickable(true)

	treeView.AppendColumn(column)
}

/*
	func addColumn(treeView *gtk.TreeView, title string, id int) {
		renderer, err := gtk.CellRendererTextNew()
		if err != nil {
			fmt.Printf("Ошибка создания рендерера для колонки: %v\n", err)
			return
		}

		column, err := gtk.TreeViewColumnNewWithAttribute(title, renderer, "text", id)
		if err != nil {
			fmt.Printf("Ошибка создания колонки %s: %v\n", title, err)
			return
		}

		treeView.AppendColumn(column)
	}
*/
func buildOrgTree(entries []*ldap.Entry) *OrgNode {
	root := &OrgNode{
		Name:     "Организации и отделы",
		Children: make(map[string]*OrgNode),
	}

	for _, entry := range entries {
		str := entry.GetAttributeValue("o")
		if str == "filial" || len(str) == 0 {
			continue
		}

		orgParts := strings.SplitN(entry.GetAttributeValue("o"), ",", 2)
		orgName := strings.TrimSpace(orgParts[0])
		var deptName string
		if len(orgParts) > 1 {
			deptName = strings.TrimSpace(orgParts[1])
		}

		// Find or create organization node
		orgNode, exists := root.Children[orgName]
		if !exists {
			orgNode = &OrgNode{
				Name:     orgName,
				Children: make(map[string]*OrgNode),
			}
			root.Children[orgName] = orgNode
		}

		// Handle department and OU
		if deptName != "" {
			// Organization has departments
			deptNode, exists := orgNode.Children[deptName]
			if !exists {
				deptNode = &OrgNode{
					Name:     deptName,
					Children: make(map[string]*OrgNode),
				}
				orgNode.Children[deptName] = deptNode
			}

			// Add OU under department
			if entry.GetAttributeValue("ou") != "" {
				if _, exists := deptNode.Children[entry.GetAttributeValue("ou")]; !exists {
					deptNode.Children[entry.GetAttributeValue("ou")] = &OrgNode{
						Name:     entry.GetAttributeValue("ou"),
						Children: make(map[string]*OrgNode),
					}
				}
			}
		} else {
			// Organization has no departments, add OU directly under org
			if entry.GetAttributeValue("ou") != "" {
				if _, exists := orgNode.Children[entry.GetAttributeValue("ou")]; !exists {
					orgNode.Children[entry.GetAttributeValue("ou")] = &OrgNode{
						Name:     entry.GetAttributeValue("ou"),
						Children: make(map[string]*OrgNode),
					}
				}
			}
		}
	}

	return root
}

// Helper function to populate tree store
func populateTreeStore(store *gtk.TreeStore, parent *gtk.TreeIter, node *OrgNode) {
	iter := store.Append(parent)
	store.SetValue(iter, 0, node.Name)

	for _, child := range node.Children {
		populateTreeStore(store, iter, child)
	}
}

func loadLDAPData() {
	// Подключаемся к LDAP серверу
	l, err := ldap.Dial("tcp", config.LDAPServer)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка подключения к LDAP серверу: " + err.Error())
		})
		return
	}
	defer l.Close()

	// Аутентификация
	err = l.Bind(config.BindDN, config.BindPassword)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка аутентификации в LDAP: " + err.Error())
		})
		return
	}

	// Поиск организаций
	searchRequest := ldap.NewSearchRequest(
		config.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=inetOrgPerson)",
		[]string{"o", "ou"},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка поиска организаций: " + err.Error())
		})
		return
	}

	// Обновляем дерево в основном потоке GTK
	glib.IdleAdd(func() {
		treeStore, err := treeView.GetModel()
		if err != nil {
			return
		}

		// Очищаем дерево
		treeStore.(*gtk.TreeStore).Clear()
		/////////////////////////////////////////////////////////////////////////////////////

		//	var root *OrgNode
		//	root = buildOrgTree(sr.Entries)
		// Populate tree store
		sort.Slice(sr.Entries, func(i, j int) (less bool) {
			return sr.Entries[i].DN < sr.Entries[j].DN
		})
		populateTreeStore(treeStore.(*gtk.TreeStore), nil, buildOrgTree(sr.Entries))

		// Добавляем организации и отделы
		for _, entry := range sr.Entries {
			orgName := entry.GetAttributeValue("o")
			if orgName == "" {
				continue
			}

		}

		// Раскрытие первого уровня
		iter, _ := treeStore.(*gtk.TreeStore).GetIterFirst()
		path, _ := treeStore.(*gtk.TreeStore).GetPath(iter)
		treeView.ExpandRow(path, false)

	})
}

func setupEventHandlers() {
	// Обработка выбора в дереве
	treeView.Connect("row-activated", func() {
		go onDepartmentSelected()
	})

	// Обработка выбора в результатах поиска
	resultsView.Connect("row-activated", func() {
		go onPersonSelected()
	})

	// Обработка выбора в результатах поиска
	resultsView.Connect("cursor-changed", func() {
		if resultsView.IsFocus() {
			go onPersonSelected()
		}
	})

	// Обработка нажатия Enter в поле поиска
	searchEntry.Connect("activate", func() {
		go performSearch()
	})
}

func onDepartmentSelected() {

	selection, err := treeView.GetSelection()
	if err != nil {
		return
	}

	model, iter, ok := selection.GetSelected()
	if !ok {
		return
	}

	// Получаем путь к выбранному элементу
	path, err := model.(*gtk.TreeModel).GetPath(iter)
	if err != nil {
		return
	}

	// Определяем уровень вложенности
	depth := path.GetDepth()

	if depth <= 2 {
		return
	}

	// Получаем модель
	model, err = treeView.GetModel()
	if err != nil {
		log.Println("Ошибка модели:", err)
		return
	}

	// Приводим к TreeStore
	treeStore, ok := model.(*gtk.TreeStore)
	if !ok {
		log.Println("Неверный тип модели")
		return
	}

	// Получаем итератор текущего элемента
	//iter, err = treeStore.GetIter(path)
	//if err != nil {
	//log.Println("Ошибка итератора:", err)
	//return
	//}

	value, err := model.(*gtk.TreeStore).GetValue(iter, 0)
	if err != nil {
		return
	}
	itemName, _ := value.GetString()

	var rootName, parentName string
	// Проверяем родителя
	var parentIter gtk.TreeIter
	if treeStore.IterParent(&parentIter, iter) {
		// Получаем значение из колонки 0 (текст)
		value, err := treeStore.GetValue(&parentIter, 0)
		if err != nil {
			log.Println("Ошибка значения:", err)
			return
		}

		// Извлекаем строку
		parentName, err = value.GetString()
		if err != nil {
			log.Println("Ошибка преобразования:", err)
			return
		}
		//		fmt.Printf("Текст родителя: %s\n", parentName)
	}
	// Проверяем корень
	var rootIter gtk.TreeIter
	if treeStore.IterParent(&rootIter, &parentIter) {
		// Получаем значение из колонки 0 (текст)
		value, err := treeStore.GetValue(&rootIter, 0)
		if err != nil {
			log.Println("Ошибка значения:", err)
			return
		}

		// Извлекаем строку
		rootName, err = value.GetString()
		if err != nil {
			log.Println("Ошибка преобразования:", err)
			return
		}
		//		fmt.Printf("Текст родителя: %s\n", parentName)
	}
	//	log.Printf("Путь элемента: %s->%s->%s\n", rootName, parentName, itemName)

	hasChildren := treeStore.IterHasChild(iter)
	//	log.Printf("Элемент имеет дочерние элементы: %v\n", hasChildren)

	//treeStore.IterParent(&parentIter, iter)
	//	log.Printf("Элемент является дочерним: %v\n", hasParent)

	if depth == 4 {
		// Ищем людей в отделе
		searchPeople("(&(o=" + rootName + ", " + parentName + ")(ou=" + itemName + "))")
	} else if depth == 3 && !hasChildren {
		// Ищем людей в отделе
		searchPeople("(&(o=" + parentName + ")(ou=" + itemName + "))")
	} else if depth == 3 && hasChildren {
		// Ищем людей в отделе
		searchPeople("(o=" + parentName + ", " + itemName + ")")
	}

}

func performSearch() {
	text, err := searchEntry.GetText()
	if err != nil {
		return
	}

	if text == "" {
		return
	}

	// Формируем фильтр для поиска
	filter := fmt.Sprintf("(|(cn=*%s*)(mail=*%s*)(telephoneNumber=*%s*))", text, text, text)

	// Ищем людей
	searchPeople(filter)
}

func searchPeople(filter string) {
	// Подключаемся к LDAP серверу
	l, err := ldap.Dial("tcp", config.LDAPServer)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка подключения к LDAP серверу: " + err.Error())
		})
		return
	}
	defer l.Close()

	// Аутентификация
	err = l.Bind(config.BindDN, config.BindPassword)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка аутентификации в LDAP: " + err.Error())
		})
		return
	}

	// Поиск людей
	searchRequest := ldap.NewSearchRequest(
		config.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(&(objectClass=inetOrgPerson)"+filter+")",
		[]string{"cn", "mail", "telephoneNumber", "ou", "o", "title", "l", "postalAddress"},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка поиска людей: " + err.Error())
		})
		return
	}

	// Обновляем результаты в основном потоке GTK
	glib.IdleAdd(func() {
		listStore, err := resultsView.GetModel()
		if err != nil {
			return
		}

		// Очищаем список
		listStore.(*gtk.ListStore).Clear()

		searchResult = nil

		// Добавляем результаты
		for _, entry := range sr.Entries {

			/*		iter := listStore.(*gtk.ListStore).Append()
					listStore.(*gtk.ListStore).Set(iter,
						[]int{0, 1, 2, 3, 4},
						[]interface{}{
							entry.GetAttributeValue("cn"),
							entry.GetAttributeValue("mail"),
							entry.GetAttributeValue("telephoneNumber"),
							entry.GetAttributeValue("ou"),
							strings.Replace(strings.Replace(entry.GetAttributeValue("o"), "&#039;", "'", -1), "&quot;", "\"", -1),
						})
			*/
			var item LDIFEntry
			item.CN = entry.GetAttributeValue("cn")
			item.Mail = entry.GetAttributeValue("mail")
			item.OU = entry.GetAttributeValue("ou")
			item.L = entry.GetAttributeValue("l")
			item.Title = entry.GetAttributeValue("title")
			item.O = entry.GetAttributeValue("o")
			item.TelephoneNumber = entry.GetAttributeValue("telephoneNumber")
			item.PostalAddress = entry.GetAttributeValue("postalAddress")

			searchResult = append(searchResult, item)
		}

		sort.Slice(searchResult, func(i, j int) (less bool) {
			return searchResult[i].CN < searchResult[j].CN
		})

		for _, entry := range searchResult {
			iter := listStore.(*gtk.ListStore).Append()
			listStore.(*gtk.ListStore).Set(iter,
				[]int{0, 1, 2, 3, 4},
				[]interface{}{
					entry.CN,
					entry.Mail,
					entry.TelephoneNumber,
					entry.OU,
					strings.Replace(strings.Replace(entry.O, "&#039;", "'", -1), "&quot;", "\"", -1),
				})
		}
	})
	// Безопасное обновление текста
	glib.IdleAdd(func() {
		// Получаем границы текста
		start, end := detailsBuffer.GetBounds()

		// Удаляем старый текст
		detailsBuffer.Delete(start, end)
	})

}

func onPersonSelected() {

	selection, err := resultsView.GetSelection()
	if err != nil {
		return
	}

	model, iter, ok := selection.GetSelected()
	if !ok {
		return
	}

	// Получаем путь к выбранному элементу
	path, err := model.(*gtk.TreeModel).GetPath(iter)
	if err != nil || path == nil {
		return
	}

	index := path.GetIndices()[0]
	//	fmt.Printf("Выделенная строка имеет индекс: %d\n", rowIndex)

	// Получаем данные о человеке
	fullName, _ := model.(*gtk.TreeModel).GetValue(iter, 0)
	email, _ := model.(*gtk.TreeModel).GetValue(iter, 1)
	phone, _ := model.(*gtk.TreeModel).GetValue(iter, 2)
	department, _ := model.(*gtk.TreeModel).GetValue(iter, 3)
	organization, _ := model.(*gtk.TreeModel).GetValue(iter, 4)

	fullNameStr, _ := fullName.GetString()
	emailStr, _ := email.GetString()
	phoneStr, _ := phone.GetString()
	deptStr, _ := department.GetString()
	orgStr, _ := organization.GetString()

	if fullNameStr != searchResult[index].CN {
		fmt.Printf("Несоответсвие строки и индекса элемента : %d\n", index)
		return
	}

	// Формируем детальную информацию
	//details := fmt.Sprintf("ФИО: %s\nEmail: %s\nТелефон: %s\nОтдел: %s\nОрганизация: %s",
	//	fullNameStr, emailStr, phoneStr, deptStr, orgStr)
	details := fmt.Sprintf("ФИО: %s\nEmail: %s\nТелефон: %s\nДолжность: %s\nОтдел: %s\nОрганизация: %s\nГород: %s\nАдрес: %s",
		fullNameStr, emailStr, phoneStr, searchResult[index].Title, deptStr, orgStr, searchResult[index].L, searchResult[index].PostalAddress)

	// Безопасное обновление текста
	glib.IdleAdd(func() {
		// Получаем границы текста
		start, end := detailsBuffer.GetBounds()

		// Удаляем старый текст
		detailsBuffer.Delete(start, end)

		// Вставляем новый текст
		detailsBuffer.Insert(start, details)
	})

	pathTree := ""
	strs := strings.Split(searchResult[index].O, ",")
	pathTree = strs[0]
	if len(strs) == 2 {
		pathTree = pathTree + ":" + strs[1]
	}

	pathTree = pathTree + ":" + deptStr

	// Выделяем соответствующий отдел в дереве
	selectByPath(pathTree)

}

func showErrorDialog(message string) {
	dialog := gtk.MessageDialogNew(
		mainWindow,
		gtk.DIALOG_MODAL,
		gtk.MESSAGE_ERROR,
		gtk.BUTTONS_OK,
		message,
	)
	dialog.Run()
	dialog.Destroy()
}

func isAlreadyRunning() bool {
	// Проверяем, существует ли сокет
	if _, err := os.Stat(socketFile); err == nil {
		// Пробуем подключиться к сокету
		conn, err := net.Dial("unix", socketFile)
		if err == nil {
			conn.Close()
			return true
		}
		// Если подключиться не удалось, удаляем старый сокет
		os.Remove(socketFile)
	}
	return false
}

func activateExistingInstance() {
	conn, err := net.Dial("unix", socketFile)
	if err != nil {
		return
	}
	defer conn.Close()

	_, err = conn.Write([]byte("activate\n"))
	if err != nil {
		return
	}
}

func startUnixSocketServer() {
	// Удаляем старый сокет, если он существует
	os.Remove(socketFile)

	// Создаем директорию для сокета, если ее нет
	os.MkdirAll(filepath.Dir(socketFile), 0755)

	// Создаем Unix socket
	l, err := net.Listen("unix", socketFile)
	if err != nil {
		fmt.Printf("Ошибка создания Unix socket: %v\n", err)
		return
	}
	//	defer l.Close()

	// Устанавливаем права на сокет
	os.Chmod(socketFile, 0600)

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	cmd := string(buf[:n])
	if cmd == "activate\n" {
		restoreFromTray()
	}
}

func setupSignalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		glib.IdleAdd(func() {
			gtk.MainQuit()
		})
	}()
}
func setWindowIcon() {
	err := mainWindow.SetIconFromFile(defaultIcon)
	if err != nil {
		log.Printf("Ошибка загрузки данных иконки: %v\n", err)
	} else {
		mainWindow.SetIconName("system-users")
	}
}

func selectByPath(pathStr string) {
	parts := strings.Split(pathStr, ":")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			log.Println("Некорректный путь")
			return
		}
	}
	model, err := treeView.GetModel()
	var store *gtk.TreeStore

	store, ok := model.(*gtk.TreeStore)
	if !ok {
		fmt.Errorf("модель не является TreeStore")
		return
	}

	currentPath := "0"
	s := "0"
	// Перебираем каждую часть пути
	for _, part := range parts {
		// Формируем путь для текущего уровня
		currentPath = s + ":0"

		n, ok := findStrOnLevel(store, part, currentPath)
		if ok {
			s += ":" + fmt.Sprintf("%d", n)

		}

	}
	//	fmt.Println(s)
	//строим путь
	path, err := gtk.TreePathNewFromString(s)
	if err != nil {
		return
	}
	//разворачиваем до элемент
	treeView.ExpandToPath(path)
	//выделяем элемент
	selected, _ := treeView.GetSelection()
	selected.SelectPath(path)

	//прокручиваем до элемента
	treeView.ScrollToCell(path, nil, true, 0.5, 0.5)

	return
}
func getTextIter(store *gtk.TreeStore, iter *gtk.TreeIter) (string, error) {
	val, err := store.GetValue(iter, 0)
	if err != nil {
		return "", err
	}
	str, err := val.GetString()
	return str, err
}

func findStrOnLevel(store *gtk.TreeStore, str string, path string) (int, bool) {
	iter, err := store.GetIterFromString(path)
	if err != nil {
		return -1, false
	}
	ok := true
	for index := 0; ok; index++ {
		s, _ := getTextIter(store, iter)
		//		fmt.Printf(s)
		if s == str {
			return index, true
		}
		ok = store.IterNext(iter)
	}
	return -1, false
}
