package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

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
	SocketFile   string `json:"socket_file"`
}

var (
	config        Config
	configPath    string
	mainWindow    *gtk.Window
	treeView      *gtk.TreeView
	searchEntry   *gtk.Entry
	resultsView   *gtk.TreeView
	detailsView   *gtk.TextView
	detailsBuffer *gtk.TextBuffer
	indicator     *appindicator.Indicator
	searchResult  []LDAPEntry
)

// LDAPEntry represents a single LDAP entry from the LDIF file
type LDAPEntry struct {
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
	appName    = "ldap-phonebook"
	appVersion = "0.8"
	configFile = "ldap-phonebook.json"
)

func main() {

	// Загружаем конфигурацию
	loadConfig()

	// Проверяем, не запущен ли уже экземпляр программы
	if isAlreadyRunning() {
		fmt.Println("Программа уже запущена. Активируем существующий экземпляр...")
		activateExistingInstance()
		os.Exit(0)
	}

	// Инициализируем GTK
	gtk.Init(nil)

	// Создаем главное окно
	createMainWindow()

	// Создаем иконку в трее
	createStatusIndicator()

	// Запускаем Unix socket сервер
	go startUnixSocketServer()

	// Показываем все виджеты
	mainWindow.ShowAll()

	// Загружаем данные из LDAP
	go loadLDAPData()

	// Главный цикл GTK
	gtk.Main()
}

func loadConfig() {

	// Определяем путь к конфигурационному файлу
	configPaths := []string{
		filepath.Join(filepath.Dir(os.Args[0]), configFile),
		filepath.Join("/etc", appName, configFile),
		filepath.Join(os.Getenv("HOME"), ".config", appName, configFile),
	}

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			data, err := os.ReadFile(path)
			if err != nil {
				log.Printf("Ошибка чтения конфига %s: %v\n", path, err)
				continue
			}

			if err := json.Unmarshal(data, &config); err != nil {
				log.Printf("Ошибка разбора конфига %s: %v\n", path, err)
				continue
			}

			configPath = path
			break
		}
	}

	if configPath == "" {

		// Создаем конфиг по умолчанию, если файл не существует
		config = Config{
			LDAPServer: "abook:389",
			BindDN:     "dc=mail,dc=local",
			//			BindDN:       "cn=user-ro,dc=mail,dc=local",
			//			BindPassword: "ro_pass",
			BindPassword: "",
			BaseDN:       "dc=mail,dc=local",
			SocketFile:   "/tmp/ldap-phonebook.sock",
		}

		configPath = filepath.Join(os.Getenv("HOME"), ".config", appName, configFile)
		// Создаем директорию, если ее нет
		os.MkdirAll(filepath.Dir(configPath), 0755)

		// Сохраняем конфиг по умолчанию
		data, _ := json.MarshalIndent(config, "", "  ")
		os.WriteFile(configPath, data, 0644)
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
		onWindowDelete()
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

	parent, _ := treeStore.GetIterFirst()
	iter := treeStore.Append(parent)
	treeStore.SetValue(iter, 0, "Loading...")

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

	treeView.SetSearchColumn(0)
	treeView.SetReorderable(false) // Запрещаем перетаскивание, чтобы сохранить сортировку

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
	searchLabel, err := gtk.LabelNew(" Панель поиска")
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
	//	searchEntry.SetProperty("can-focus", true)
	searchEntry.SetProperty("focus-on-click", true)
	searchEntry.SetProperty("primary-icon-stock", "gtk-find")
	searchEntry.SetProperty("primary-icon-activatable", false)
	//	searchEntry.SetProperty("primary-icon-sensitive", false)

	searchEntry.SetProperty("secondary-icon-stock", "gtk-clear")
	searchEntry.SetProperty("secondary-icon-tooltip-text", "Очистить поиск")

	searchEntry.SetPlaceholderText("Поиск по ФИО, email, телефону...")

	searchButton, err := gtk.ButtonNewWithLabel("Поиск")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки поиска: %v\n", err)
		os.Exit(1)
	}
	searchButton.SetTooltipText("Поиск")
	searchButton.SetProperty("label", "gtk-find")
	searchButton.SetProperty("use-stock", true)

	exitButton, err := gtk.ButtonNewWithLabel("Выход")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки выхода: %v\n", err)
		os.Exit(1)
	}
	exitButton.SetTooltipText("Выход")
	exitButton.SetProperty("label", "gtk-quit")
	exitButton.SetProperty("use-stock", true)

	helpButton, err := gtk.ButtonNewWithLabel("?")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки помощи: %v\n", err)
		os.Exit(1)
	}
	helpButton.SetTooltipText("О программе")

	// Настройка порядка табуляции
	searchEntry.SetProperty("can-focus", true)
	searchButton.SetProperty("can-focus", true)
	exitButton.SetProperty("can-focus", true)
	helpButton.SetProperty("can-focus", true)

	searchBox.PackStart(searchEntry, true, true, 0)
	searchBox.PackStart(searchButton, false, false, 0)
	searchBox.PackStart(exitButton, false, false, 0)
	searchBox.PackStart(helpButton, false, false, 0)

	//	searchPanel.PackStart(searchBox, false, false, 0)
	//topCenterBox.PackStart(searchPanel, false, false, 0)

	//	searchBox.PackStart(searchEntry, true, true, 0)
	//	searchBox.PackStart(searchButton, false, false, 0)
	//	searchBox.PackStart(exitButton, false, false, 0)

	// Центральная часть - результаты поиска
	resultsView, err = gtk.TreeViewNew()
	if err != nil {
		fmt.Printf("Ошибка создания таблицы результатов: %v\n", err)
		os.Exit(1)
	}

	// Включаем возможность изменения ширины колонок
	resultsView.SetProperty("headers-clickable", true)
	resultsView.SetProperty("reorderable", true)
	resultsView.SetProperty("focus-on-click", true)
	resultsView.SetProperty("activate-on-single-click", true)

	// Настройка модели результатов
	listStore, err := gtk.ListStoreNew(
		glib.TYPE_STRING, // ФИО
		glib.TYPE_STRING, // Телефон
		glib.TYPE_STRING, // Email
		glib.TYPE_STRING, // Должность
		glib.TYPE_STRING, // Отдел
		glib.TYPE_STRING, // Организация
	)
	if err != nil {
		fmt.Printf("Ошибка создания модели результатов: %v\n", err)
		os.Exit(1)
	}

	resultsView.SetModel(listStore)

	resultsView.SetEnableSearch(false)

	// Добавляем колонки с возможностью изменения ширины
	addResizableColumn(resultsView, "ФИО", 0)
	addResizableColumn(resultsView, "Телефон", 1)
	addResizableColumn(resultsView, "Email", 2)
	addResizableColumn(resultsView, "Должность", 3)
	addResizableColumn(resultsView, "Отдел", 4)
	addResizableColumn(resultsView, "Организация", 5)

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
	detailsBox.SetSizeRequest(-1, 150)

	// Добавляем части в вертикальный разделитель
	centerPanel.Pack1(topCenterBox, true, false)
	centerPanel.Pack2(detailsBox, false, false)
	centerPanel.SetPosition(int(float64(mainWindow.GetAllocatedHeight()) * 0.8))

	// Добавляем панели в горизонтальный разделитель
	mainPaned.Pack1(leftPanel, false, false)
	mainPaned.Pack2(centerPanel, true, false)
	mainPaned.SetPosition(int(float64(mainWindow.GetAllocatedWidth()) * 0.5))

	searchEntry.GrabFocus()
	resultsScrolled.SetSizeRequest(-1, 350)
	// Добавляем главный контейнер в окно
	mainWindow.Add(mainPaned)

	// Настройка обработчиков событий
	// Обработка сигналов для корректного завершения

	// Обработчик нажатия клавиш для главного окна
	mainWindow.Connect("key-press-event", func(window *gtk.Window, event *gdk.Event) {
		keyEvent := gdk.EventKeyNewFromEvent(event)

		// Обработка Esc
		if keyEvent.KeyVal() == gdk.KEY_Escape {
			minimizeToTray()
		}
	})

	// Обработка нажатия кнопки О программе
	helpButton.Connect("clicked", showAboutDialog)

	// Обработка нажатия кнопки поиска
	searchButton.Connect("clicked", func() {
		go performSearch()
	})
	// Обработка нажатия Enter в поле поиска
	searchEntry.Connect("activate", func() {
		go performSearch()
	})
	// Обработка нажатия Enter в поле поиска
	searchEntry.Connect("icon-press", func() {
		go clearSearch()
	}) // Обработка нажатия Выход в поле поиск
	exitButton.Connect("clicked", func() {
		gtk.MainQuit()
	})
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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		glib.IdleAdd(func() {
			gtk.MainQuit()
		})
	}()

}

func showAboutDialog() {
	dialog, err := gtk.AboutDialogNew()
	if err != nil {
		fmt.Printf("Ошибка создания диалога: %v\n", err)
		return
	}

	// Декодируем иконку
	iconData, _ := base64.StdEncoding.DecodeString(iconBase64)
	loader, _ := gdk.PixbufLoaderNew()
	loader.Write(iconData)
	loader.Close()
	pixbuf, _ := loader.GetPixbuf()

	dialog.SetLogo(pixbuf)
	dialog.SetIcon(pixbuf)

	dialog.SetTitle("О программе")

	dialog.SetProgramName(appName)
	dialog.SetVersion(appVersion)
	dialog.SetCopyright("© 2025 Maxim Izvekov")
	dialog.SetComments("Программа для поиска контактных данных в LDAP")
	//	dialog.SetLicense("Лицензия: MIT")
	dialog.SetLicenseType(gtk.LICENSE_MIT_X11)
	dialog.SetWebsite("https://github.com/imax1000/ldap-phonebook")
	dialog.SetWebsiteLabel("Официальный сайт")

	var authors []string
	authors = append(authors, "Maxim Izvekov (maximizvekov@yandex.ru)")
	dialog.SetAuthors(authors)

	dialog.SetComments("конфигурация: " + configPath)

	dialog.Run()
	dialog.Destroy()
}

func onWindowDelete() bool {
	minimizeToTray()
	return true
}

func minimizeToTray() {
	mainWindow.Hide()
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
	}
}

func createStatusIndicator() {
	/*
		// Декодируем иконку
			iconData, _ := base64.StdEncoding.DecodeString(iconBase64)
			loader, _ := gdk.PixbufLoaderNew()
			loader.Write(iconData)
			loader.Close()
			pixbuf, _ := loader.GetPixbuf()
	*/

	// Определяем путь файлу иконки
	icon := appName + ".ico"
	iconPaths := []string{
		filepath.Join("/usr/share/icons", appName, icon),
		filepath.Join(filepath.Dir(os.Args[0]), icon),
		filepath.Join(os.TempDir(), icon),
	}

	for _, path := range iconPaths {
		if _, err := os.Stat(path); err == nil {
			icon = path
			break
		}
	}
	if icon[0] != '/' {
		//файл с иконкой не найден. Создадим свой

		// Декодируем иконку
		iconData, _ := base64.StdEncoding.DecodeString(iconBase64)
		//		loader, _ := gdk.PixbufLoaderNew()
		//		loader.Write(iconData)
		//		loader.Close()
		//		pixbuf, _ := loader.GetPixbuf()
		file, err := os.OpenFile(filepath.Join(os.TempDir(), icon), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return
		}

		//	pixbuf.WritePNG(file.W)
		_, err = file.Write(iconData)
		if err != nil {
			return
		}
		icon = filepath.Join(os.TempDir(), icon)
	}

	indicator = appindicator.New(appName, icon, appindicator.CategoryApplicationStatus)

	//	indicator = appindicator.New(appName, defaultIcon, appindicator.CategoryApplicationStatus)
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
			minimizeToTray()
		} else {
			restoreFromTray()
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

	//	renderer.SetProperty("ellipsize", pango.ELLIPSIZE_END)

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

func quotRemove(str string) string {
	return strings.Replace(strings.Replace(str, "&#039;", "'", -1), "&quot;", "\"", -1)
}
func quotAdd(str string) string {
	return strings.Replace(strings.Replace(str, "'", "&#039;", -1), "\"", "&quot;", -1)
}
func buildOrgTree(entries []*ldap.Entry) *OrgNode {
	root := &OrgNode{
		Name:     "Организации и отделы",
		Children: make(map[string]*OrgNode),
	}

	for _, entry := range entries {
		str := quotRemove(entry.GetAttributeValue("o"))

		orgParts := strings.SplitN(str, ",", 2)
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
			deptName = quotRemove(entry.GetAttributeValue("ou"))
			if deptName != "" {
				if _, exists := deptNode.Children[deptName]; !exists {
					deptNode.Children[deptName] = &OrgNode{
						Name:     deptName,
						Children: make(map[string]*OrgNode),
					}
				}
			}
		} else {
			deptName = quotRemove(entry.GetAttributeValue("ou"))
			// Organization has no departments, add OU directly under org
			if deptName != "" {
				if _, exists := orgNode.Children[deptName]; !exists {
					orgNode.Children[deptName] = &OrgNode{
						Name:     deptName,
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

	var s []string
	for _, child := range node.Children {
		s = append(s, child.Name)
	}
	sort.Slice(s, func(i, j int) (less bool) {
		return strings.ToLower(s[i]) < strings.ToLower(s[j])
	})

	for _, str := range s {
		child := node.Children[str]
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

		// Получаем модель
		model, err := treeView.GetModel()
		if err != nil {
			log.Println("Ошибка модели:", err)
			return
		}

		var store *gtk.TreeStore
		// Приводим к TreeStore
		store, ok := model.(*gtk.TreeStore)
		if !ok {
			log.Println("Неверный тип модели")
			return
		}

		if err != nil {
			return
		}

		// Очищаем дерево
		store.Clear()
		/////////////////////////////////////////////////////////////////////////////////////

		populateTreeStore(store, nil, buildOrgTree(sr.Entries))

		// Добавляем организации и отделы
		for _, entry := range sr.Entries {
			orgName := quotRemove(entry.GetAttributeValue("o"))
			if orgName == "" {
				continue
			}

		}
		// Раскрытие первого уровня
		iter, _ := store.GetIterFirst()
		path, _ := store.GetPath(iter)
		treeView.ExpandRow(path, false)
		/*
			// Раскрытие второго уровня
			iter, err = store.GetIterFromString("0:0")
			if err != nil {
				return
			}
			ok = true
			for index := 0; ok; index++ {
				path, _ := store.GetPath(iter)
				treeView.ExpandRow(path, false)
				ok = store.IterNext(iter)
			}
		*/
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

	itemName, err := getTextIter(model.(*gtk.TreeStore), iter)
	//	value, err := model.(*gtk.TreeStore).GetValue(iter, 0)
	if err != nil {
		return
	}
	//	itemName, _ := value.GetString()

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
		//		searchPeople(quotAdd("(&(o=" + rootName + ", " + parentName + ")(ou=" + itemName + "))"))
		searchPeople("(&(o=" + rootName + ", " + parentName + ")(ou=" + itemName + "))")
	} else if depth == 3 && !hasChildren {
		// Ищем людей в отделе
		//		searchPeople(quotAdd("(&(o=" + parentName + ")(ou=" + itemName + "))"))
		searchPeople("(&(o=" + parentName + ")(ou=" + itemName + "))")
	} else if depth == 3 && hasChildren {
		// Ищем людей в отделе
		searchPeople("(o=" + parentName + ", " + itemName + ")")
		//		searchPeople(quotAdd("(o=" + parentName + ", " + itemName + ")"))
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
	if searchPeople(filter) == 0 {
		text = ConvertString(text)
		if len(text) > 0 {
			// Формируем фильтр для поиска
			filter := fmt.Sprintf("(|(cn=*%s*)(mail=*%s*)(telephoneNumber=*%s*))", text, text, text)

			// Ищем людей
			searchPeople(filter)
		}
	}
}

func searchPeople(text string) int {

	filter := text
	// Подключаемся к LDAP серверу
	l, err := ldap.Dial("tcp", config.LDAPServer)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка подключения к LDAP серверу: " + err.Error())
		})
		return -1
	}
	defer l.Close()

	// Аутентификация
	err = l.Bind(config.BindDN, config.BindPassword)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка аутентификации в LDAP: " + err.Error())
		})
		return -1
	}

	// Поиск людей
	searchRequest := ldap.NewSearchRequest(
		config.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		quotAdd("(&(objectClass=inetOrgPerson)"+filter+")"),
		[]string{"cn", "mail", "telephoneNumber", "ou", "o", "title", "l", "postalAddress"},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		glib.IdleAdd(func() {
			showErrorDialog("Ошибка поиска людей: " + err.Error())
		})
		return -1
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

			var item LDAPEntry
			item.CN = entry.GetAttributeValue("cn")
			item.Mail = entry.GetAttributeValue("mail")
			item.OU = quotRemove(entry.GetAttributeValue("ou"))
			item.L = entry.GetAttributeValue("l")
			item.Title = entry.GetAttributeValue("title")
			item.O = quotRemove(entry.GetAttributeValue("o"))
			item.TelephoneNumber = entry.GetAttributeValue("telephoneNumber")
			item.PostalAddress = quotRemove(entry.GetAttributeValue("postalAddress"))

			searchResult = append(searchResult, item)
		}

		sort.Slice(searchResult, func(i, j int) (less bool) {
			return searchResult[i].CN < searchResult[j].CN
		})

		for _, entry := range searchResult {
			iter := listStore.(*gtk.ListStore).Append()
			listStore.(*gtk.ListStore).Set(iter,
				[]int{0, 1, 2, 3, 4, 5},
				[]any{
					entry.CN,
					entry.TelephoneNumber,
					entry.Mail,
					entry.Title,
					entry.OU,
					entry.O,
				})
		}
		resultsView.ColumnsAutosize()
	})
	// Безопасное обновление текста
	glib.IdleAdd(func() {
		// Получаем границы текста
		start, end := detailsBuffer.GetBounds()

		// Удаляем старый текст
		detailsBuffer.Delete(start, end)
	})
	return len(sr.Entries)
}
func clearSearch() {
	// Безопасное обновление текста
	glib.IdleAdd(func() {
		// Обновляем результаты в основном потоке GTK

		// Вставляем новый текст
		searchEntry.SetText("")

		listStore, err := resultsView.GetModel()
		if err != nil {
			return
		}

		// Очищаем список
		listStore.(*gtk.ListStore).Clear()

		resultsView.ColumnsAutosize()

		searchResult = nil

		// Получаем границы текста
		start, end := detailsBuffer.GetBounds()

		// Удаляем старый текст
		detailsBuffer.Delete(start, end)

		searchEntry.GrabFocusWithoutSelecting()

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
	//	title, _ := model.(*gtk.TreeModel).GetValue(iter, 3)
	department, _ := model.(*gtk.TreeModel).GetValue(iter, 4)
	organization, _ := model.(*gtk.TreeModel).GetValue(iter, 5)

	fullNameStr, _ := fullName.GetString()
	emailStr, _ := email.GetString()
	phoneStr, _ := phone.GetString()
	//	titleStr, _ := title.GetString()
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
		"%s", message,
	)
	dialog.Run()
	dialog.Destroy()
}

func isAlreadyRunning() bool {
	// Проверяем, существует ли сокет
	if _, err := os.Stat(config.SocketFile); err == nil {
		// Пробуем подключиться к сокету
		conn, err := net.Dial("unix", config.SocketFile)
		if err == nil {
			conn.Close()
			return true
		}
		// Если подключиться не удалось, удаляем старый сокет
		os.Remove(config.SocketFile)
	}
	return false
}

func activateExistingInstance() {
	conn, err := net.Dial("unix", config.SocketFile)
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
	os.Remove(config.SocketFile)

	// Создаем директорию для сокета, если ее нет
	os.MkdirAll(filepath.Dir(config.SocketFile), 0755)

	// Создаем Unix socket
	l, err := net.Listen("unix", config.SocketFile)
	if err != nil {
		fmt.Printf("Ошибка создания Unix socket: %v\n", err)
		return
	}
	//	defer l.Close()

	// Устанавливаем права на сокет
	os.Chmod(config.SocketFile, 0600)

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

func setWindowIcon() {

	// Декодируем иконку
	iconData, _ := base64.StdEncoding.DecodeString(iconBase64)
	loader, _ := gdk.PixbufLoaderNew()
	loader.Write(iconData)
	loader.Close()
	pixbuf, _ := loader.GetPixbuf()

	mainWindow.SetIcon(pixbuf)

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
	if err != nil {
		return
	}

	var store *gtk.TreeStore

	store, ok := model.(*gtk.TreeStore)
	if !ok {
		fmt.Println("модель не является TreeStore")
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

var ConvertMap = map[rune]rune{
	'q':  'й',
	'w':  'ц',
	'e':  'у',
	'r':  'к',
	't':  'е',
	'y':  'н',
	'u':  'г',
	'i':  'ш',
	'o':  'щ',
	'p':  'з',
	'[':  'х',
	']':  'ъ',
	'a':  'ф',
	's':  'ы',
	'd':  'в',
	'f':  'а',
	'g':  'п',
	'h':  'р',
	'j':  'о',
	'k':  'л',
	'l':  'д',
	';':  'ж',
	'\'': 'э',
	'z':  'я',
	'x':  'ч',
	'c':  'с',
	'v':  'м',
	'b':  'и',
	'n':  'т',
	'm':  'ь',
	',':  'б',
	'.':  'ю',
	'Q':  'Й',
	'W':  'Ц',
	'E':  'У',
	'R':  'К',
	'T':  'Е',
	'Y':  'Н',
	'U':  'Г',
	'I':  'Ш',
	'O':  'Щ',
	'P':  'З',
	'{':  'Х',
	'}':  'Ъ',
	'A':  'Ф',
	'S':  'Ы',
	'D':  'В',
	'F':  'А',
	'G':  'П',
	'H':  'Р',
	'J':  'О',
	'K':  'Л',
	'L':  'Д',
	':':  'Ж',
	'"':  'Э',
	'Z':  'Я',
	'X':  'Ч',
	'C':  'С',
	'V':  'М',
	'B':  'И',
	'N':  'Т',
	'M':  'Ь',
	'<':  'Б',
	'>':  'Ю',
}

func ConvertString(in string) string {

	var buffer bytes.Buffer
	for _, ch := range in {
		r, ok := ConvertMap[ch]
		if ok {
			buffer.WriteString(string(r))
		}
	}
	str := buffer.String()
	return strings.TrimSpace(str)
}

const (
	iconBase64 = `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JX9/f39/f39/f38lP0FBQUFBPXFhQj9BQUFRfElBQUElLy8xf39/fwAqLS8rDkFBQUFBPF94aGFicHhLO0FBQTRWVjB/f39/ACksLisPQkJCQkJCODxHSkpFNT9CQkJBNFpaVH9/f39/f380QkJCQkJCQkJCQkJCQkJCQkJCQkI0Y2NXf39/f39/fzRCQkJCQkJCQkJCQkJCQkJCQkJCQjRlaVd/f39/ACotLysPQkJCQkJCQkJCQkJCQkJCQkJCNGBgWH9/f38AKSwuKw9CQkJCQkJCQkJCQkJCQkJCQkI1VVcyf39/f39/fzVCREREREREREREREREREREREREQjV/f39/f39/f39/NEBDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NANX9/f39/f39/f38QR0hISEhISEhISEhISEhISEhISEcQf39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39/f39////////////4AAAf+AAAH/gAAAPAAAADwAAAA/gAAAP4AAADwAAAA8AAAAP4AAAf+AAAA8AAAAPAAAAD+AAAA/gAAAPAAAADwAAAA/gAAB/4AAADwAAAA8AAAAP4AAAD+AAAA8AAAAPAAAAD+AAAH/gAAB/4AAAf//////////8oAAAAMAAAAGAAAAABAAgAAAAAAAAJAAAAAAAAAAAAAAABAAAAAQAAbXR5AGdydwB3dnUAcHZ6AGZlZQA5gqkAaoCMAGmBjgBxhZAAeomRAKenpwA4gqsAfai+ADyMtwBDk70AXIukAGaLnwBkjaMAbpGjAGqSqABzk6YAdJapAHyWpAB9mqsAepioAHidsABqoL4AdqW/AEWWwgBFnMoASZ7MAEmZxABTnsUAWZ/GAE2izwBMo9EAXKHHAFujyQBTptMAV6jUAFyq1QBkpckAaKfJAGypywBooMAAYq7XAG2u0gBnsNgAbbLXAGuy2QBnsNcAdK3NAHmnwAB6r80Ae6jBAH2xzgBztdoAd7jbAHy63AB6s9MAjq7CAJWyxACHhoUAj42NAJCPjgCCmaYAg5yrAIWmuACTrbsAjbDDAKWlpACrq6oArrCxAK+wrwC2uboAu7u7AL/EyACErMMAhK7FAIKxzACMtMoAhLXRAIW+3gCJt9EAi7nUAIq+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pqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqWlpaWlpaWeCQwNDg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODQwJnqWlpaWlpaWmpqampqalpaWlpaWlCQ4PICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICARDgmlpaWlpaWlpqampqampqampqampgwRISIiIiImJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmIiIiIRENpqampqampqampqampqampqampqYOICQoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCQgEKampqampqampqampqampqampqamECMnKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgnIxBISEhIR6CmpqampqampqampqamphAjKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCMQS05OS0tHpqampqampqajCAIHQkNEREMGAxMrKysrKysrKysrKysrKysrKysrKysrKysrKysrKysrKysrKysjEHh6enhOSqampqampqamAAVCREdLeoFLRQcBKysrKysrKysrKysrKysrKysrKysrKysrKysrKysrKysrKysrJBJ8goJ8eEympqampqampgAFQkRHS3qBS0UHASwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCUSgoODgnlNpqampqampqajCAIGB0JDQ0IGAxQtLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0pEoOJiYN+X6ampqampqampqampqamEiktLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tKRKJkZGJf2ympqampqampqampqamphMqLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLSoTkZKSkX9spqampqampqampqampqYTKi0yMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMi0qE5GTk5GCbaampqampqampqampqamEyoyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyKhORk5ORgm2mpqampqampqMIAgdCQ0REQwYEFjMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMyoTipOTioJ4pqampqampqYABUJER0t6gUtFBwE1NTU1NTU1NTU1NVd0j5abm5ubmY92ZTU1NTU1NTU1NTU1NTMvFIOKioN+eKampqampqamAAVCREdLeoFLRQcBNjY2NjY2NjY2V4+bm5ubm5ubm5ubm5thNjY2NjY2NjY2NjY2LxR+goJ+e1+mpqampqampqMIAgYHQkNDQgYEHDc3Nzc3Nzc3ZZmbm5qAaVMeHlBba4mbYTc3Nzc3Nzc3Nzc3Ni8UbHh4d02gpqampqampqampqampqYUMDc3Nzc3Nzc3Nzc3YZqbm28XFDEvMDAwLy4VGDA3Nzc3Nzc3Nzc3NzcwFqampqampqampqampqampqampqamFjA+Pj4+Pj4+Pj4+PpWbm1wWND4+Pj4+Pj4+Pj0+Pj4+Pj4+Pj4+Pj4+MBampqampqampqampqampqampqamphYwPj4+Pj4+Pj4+Pmebm30xPj4+Pj4+Pj4+Pj4+Pj4+Pj4+Pj4+Pj4+PjAWSEhISEegpqampqampqampqampqYWMD4+Pj4+Pj4+Pj6Fm5tSPj4+QWVlYT4+Pj4+YWVlQT4+Pj4+Pj4+Pj45FktOTktLR6ampqampqamowgCB0JDRERDBgo4QUFBQUFBlZuTPUFBYZabm5uVZ0FhlZubm5l0QUFBQUFBQUE+PBZ4enp4TkqmpqampqampgAFQkRHS3qBS0UHAUFBQUFBQZqbhEFBQZSbm5ubm5t0j5ubm5ubm4VBQUFBQUFBQTwWfIKCfHhMpqampqampqYABUJER0t6gUtFBwFBQUFBQUGbm3VBQWWbm5EdF16Xm5ubfRcZfZubcUFBQUFBQUE9HIKDg4J5TaampqampqamowgCBgdCQ0NCBgo7QUFBQUFXm5t0QUFxm5tqQEE5UJebm11BQDiTm5ZXQUFBQUFBPxyDiYmDfl+mpqampqampqampqamph0/V1dXV1dXV1dXV5ubhVdXcpubcldXVz9am5tzV1dXapubaFdXV1dXVz8diZGRiX9spqampqampqampqampqY4P1dXV1dXV1dXV1eXm4dXV2Obm3NXV1dXVZubhVdXV1abm4VXV1dXV1c/OJGSkpF/bKampqampqampqampqamOD9XV1dXV1dXV1dXk5uPV1dhm5uFV1dXV1eRm49XV1dWk5uVV1dXV1dXPziRk5ORgm2mpqampqampqampqampjg/V1dXV1dXV1dXV4abmVdXVpeblFdXV1dXj5uWV1dXV4abmVdXV1dXV1U4kZOTkYJtpqampqampqajCAIHQkNEREMGCjthYWFhYWFum5tnYWF9m5tnYWFhYZWbm2VhYWGEm5thYWFhYVdVOIqTk4qCeKampqampqamAAVCREdLeoFLRQcBZGRkZGRkY5ubdGRkXZubj2RkZGWbm5txZGRkh5ubZGRkZGRXVTiDioqDfnimpqampqampgAFQkRHS3qBS0UHAWRkZGRkZFaYm5VkZFWAm5uPcXOWm5ubdmRkZIebm2RkZGRkYVU4foKCfntfpqampqampqajCAIGB0JDQ0IGClBkZGRkZGRka5ubcWRkUJKbm5ubm5h9m41kZGSUm5dkZGRkZGFVOGx4eHdNoKampqampqampqampqamOlVZZGRkZGRkZGRkZFKXm5ZkZFkeb5ebm4BGapiEZGRkmpuOZGRkZGRhVTqmpqampqampqampqampqampqampjpVYWRkZGRkZGRkZGRZaZubjWRkWTsZHhk4WVUbO2RkdpubbmRkZGRkYVU6pqampqampqampqampqampqampqY6VWFkZGRkZGRkZGRkZFKAm5uNZGRkZFlZZGRkZGRkcZmbl1RkZGRkZGFVOkhISEhHoKampqampqampqampqamO1ZhZGRkZGRkZGRkZGRkO4mbm5ZzZGRkZGRkZGRkdJmbm2tZZGRkZGRhVjtLTk5LS0empqampqampqMIAgdCQ0REQwYLUGRkZGRkZGRkZGE7fZubm5aFdHFxcXSFlpubm31RZGRkZGRkYVY7eHp6eE5KpqampqampqYABUJER0t6gUtFBwFkZGRlZGRlZGRkYTtbkZubm5ubm5ubm5ubll47ZGRlZWRkZGFWO36Cgnx4TKampqampqamAAVCREdLeoFLRQcBZGVlZWVlZWVkZGRlVh1TcI6Xm5ubm5N9XB1SZGVlZWVkZWVkWDuCg4OCeU2mpqampqampqMIAgYHQkNDQgYLUGVlZWVlZWVlZGRkZWVkWDseGRkZGRkdO1ZkZWVlZWVlZGVlZFg7g4mJg35fpqampqampqampqampqY7WGVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVYO4mRkYl/bKampqampqampqampqamO1hlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlWDuRkpKRf2ympqampqampqampqampjtYZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZVg7kZOTkYNtpqampqampqampqampqY7WGVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVYO5GTk5GDbaampqampqamowgCB0JDRERDBgtTZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlWFCKk5OKgnimpqampqampgAFQkRHS3qBS0UHAWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZVhQg4qKg354pqampqampqYABUJER0t6gUtFBwFlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVYUH6Cgn57X6ampqampqamowgCBgdCQ0NCBgtTZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmZlWFBseHh3TaCmpqampqampqampqamplBYZWZmZmZmZmZmZmZmZmZmZmZmZmZmZmZmaGZmZmZmZmZmZmZmZmZmZVhQpqampqampqampqampqampqampqZQWGVoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGVYUKampqampqampqampqampqampqamUFhiZ2hoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGdiWFCmpqampqampqampqampqampqampkZUXWJlZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2ViYlRQpqampqampqampqampqampqampqYZiIeMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIeEHqampqampqampqampqampqampqamnxlQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQHp+mpqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAH8AAAAAAA/gfwAAAAAAD+AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA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Yl+XnZhwYmJiYmJiSkVfj5OTkImCr7S0tLS0tLS0tLS0tLS0tBNEYWJiYmJiYmJiYmJzmp2Ra2Jikp2bc2JiYmJieJ2dfGJiYmGNnZ16YmJiYmJiYkRfk5SUk4qCr7S0tLS0tLS0tLS0tLS0tBNEYWJiYmJiYmJiYmJtmZ2Sb2Jijp2dfGJiYmJidZydfm9iYmJ3nZ1+YmJiYmJiYkRfk5SUk4qEsLS0tLS0tLSztLS0s7S0tBNEYWJiYmJiYmJiYmJtlJ2YcGJieJ2djmJiYmJibpqdkmtiYmJ1nZ2RYmJiYmJiYkRek5SUk4qEsLS0tLS0tLSztLELBgkJCQ4hIR8eJWFia2tra2tjjZ2dc2tibpmdmG9ra2trepydmG9va2tunZ2Sa2trb2tiYkRek5SUk4qEsLS0tLS0tLS0oQACTVFVV4CDdlVOAh1jb29vb29ieJ2df29vYZSdnH1vb29vfZ2dm3Jvb29znZ2Sb29vb29iYURej5OTj4mCsLS0tLS0tLS0BgECTVFVV4CLgFZRAwFsb29vb29vaJ2dkm9vY3WanZp6b2xymJ2dnHtvb29znZ2Sb29vb29jYUZeiYqKiYaCsLS0tLS0tLS0oQACTVBSVliAWFVOAh5sb29vb29vY4+dnHpvb12HnZ2akpGbnZmanX5vb296nZ2Sb29vb29sYUZghYWFhYJqp7S0tLS0tLS0tLELCAkJCQQfIB8fJWFsb29vb29vY3WdnZJvb2Nch52dnZ2dmWmTnZFvb299nZ2Ob29vb29sYUYpn6Cgn56rs7S0tLS0tLS0tLS0tLS0tBVGYWxvb29vb29vb29vbF2UnZx9b29hQ3mTmJiNZUR1jXhvb2+SnZ14b29vb29sYUYVtLS0tLS0tLS0tLS0tLS0tLS0tLS0tBVGYWxvb29vb29vb29vb2NmmZ2bc29vY0MoKSkoXW9EKENvb3qbnZlub29vb29sY0YVtLS0tLS0tLS0tLS0tLS0tLS0tLS0tBVGYWxvb29vb29vb29vb29deJ2dm3tvb29vY2Nvb29vb29vcpWdnYdjb29vb29sY11TVlZWVlUCs7S0tLS0tLS0tLS0tLS0tBVdY2xvb29vb29vb29vb29sXIidnZuMc29vb29vb29vb296lZ2dmWZsb29vb29sY11TWFhYWFdWq7S0tLS0tLS0tLELCQkJCQ5PTx8fJWNvb29vb29vb29vY1x4mp2dmY58c3JycnJzfJGbnZ2ZeWFvb29vb29sY11egIGBgFhXq7S0tLS0tLS0oQACTVFVV4CDgFVOAh5vb29vb29vb29vb2NDaZecnZ2dm5iVlZianZ2dnZhpRG9vb29vb29sY11eg4aGg4BZqrS0tLS0tLS0BgECTVFVV4CLgFZRAwFwcHBvb29vb29vb29vXSl5kJ2dnZ2dnZ2dnZ2Xd1Ndb29vb29vb29vY11lhomJhoJqqrS0tLS0tLS0oQACTVBSVliAWFVOAh5wcG9vb29wcHBwcHBwb2NcKVNpd4iIiIh4eV4pQ2Nvb3BwcHBvb3BvY11liYqKiYZ2qrS0tLS0tLS0tLELCAkJCQQgIR8fJWNwcG9vb29wcHBwcHBwcG9wbGNdQ0JCQkJDXGRjcHBvb3BwcHBwcHBwY11lj5CQj4aBrbS0tLS0tLS0tLS0tLS0tBVdY3BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwY11lj5OTk4mCC7S0tLS0tLS0tLS0tLS0tBVdY3BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwY11lk5SUk4qCr7S0tLS0tLS0tLS0tLS0tBVdY3BycHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwY11mk5SUlIqEsLS0tLS0tLS0tLS0tLS0tBVdY3BycnJwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwbF1mk5SUlI+EsLS0tLS0tLS0tLELCQkJCQ5PTyAfJWNwcHBwcHBwcnJycHBwcHJycnJwcHBwcnJycnBwcHBwcnJycHBwcnJwbF1mk5SUk4qEsLS0tLS0tLS0oQACTVFVV4CDgFVOAh5wcnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJybV9lj5OTj4mCsLS0tLS0tLS0BgECTVFVV4CLgFZRAwFycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJybV9liYqKiYaCsLS0tLS0tLS0oQACTVBSVliAWFVOAh5ycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJybV9lhYWFhYJqp7S0tLS0tLS0tLELCAkJCQQhIR8fWm1ycXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFybV9Tn6Cgn56rs7S0tLS0tLS0tLS0tLO0tExfbXJxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFybV9MtLS0tLS0tLS0tLS0tLS0tLS0tLOztExfbXJycXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFybV9MtLS0tLS0tLS0tLS0tLS0tLS0tLSztA1fZ3JycXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFxcXFyZ19MtLS0tLS0tLS0tLS0tLS0tLS0tLS0tB1fZ21ycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnBtbWQdtLS0tLS0tLS0tLS0tLS0tLS0tLS0tBNuf36MjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIx+f3Qds7S0tLS0tLS0tLS0tLS0tLS0tLS0tLNTZmhudHR0dHR0dHR0dHR0dHR0dHR0dHR0dHR0dHR0dHR0dHR0dHR0dHR0dG5oaF6ztLS0tLS0tLS0tLS0tLS0tLS0tLS0tLOhq62tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2tq6GztLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLQAAPx/AAAAAAAAAAAAAP7/AAAAAAAAAAAAAPz/AAAAAAAAAAAAAPz/AAAAAAAAAAAL+AAAAAAAH/AAAAAf4TC/AAAAB/4AAAAP8ADAAAAAB/4AAAAAEAAAAAAAC7QAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAARAAAAAAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAgAAAAAAAAAAAAAAAwAAAAAAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAAAAAAEAAAAAAACAAAAAAAEAAAAAAACAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAoAAAAYAAAAMAAAAABAAgAAAAAAAAkAAAAAAAAAAAAAAABAAAAAQAAZmlqAGhnZgBtbW0AZmZlAHRzcwB7e3oAY3V/AGZmZgBnZ2YAZGRkADV9owB8o7gApaWlADiDqgBreYEAcXyDAH6luwA6ha4AOIOrADqHsAA6ibMAPo66AD6QvABeg5kAQ420AEGPuABEkr0ATJK3AEyXvwBTlbkAXJm6AFiUtQBcj6sAdI6dAHmRnwBpiZoAY5y8AGqatQBpnbsAc521AHegtwBulKkAbaK/AHSivQB8pr4AfKK3AEKXxQBGncwASprGAEqeywBFmcYAVJrBAFmewwBHoM8AS6HPAEyk0gBbo8sAVqHJAFOn1ABWqNQAW6rVAGSixABkpsoAa6XGAGurzgBmqMwAYa3XAGWv2ABsrdEAZrDYAGyx1gBsstkAZ7DXAHWlwQB1q8oAeafBAHypwwB8rsoAcqnGAHKv0AB9sM0AdLHTAHO22gB9tNMAfLrcAH232ACBf34AgYB/AIOCggCIh4YAjYuLAJCOjgCWlZQAmJeWAJybmwCDqb8AgqW5AKGfnwClpaQAqKenAKysqwCusLEAsK+vALGwrwC0tLQAtLe4ALq7vAC2urwAhK3EAIyvwwCEsswAibPMAI2xxgCDttMAhbnWAIS+3gCLutYAir7cAIy30ACUs8UAmrrMAJW91QCjvcwAvMDDAKC+0QCKwN8AlMLcAJrC2ACMwuEAk8biAJfI5ACZx+IAmcjkAKfBzgClw9QAqsTTAKnH2QCqydsAvsPHAL7EyQCyx9MAtsvXALzR2wCjzeQAqM/kAKzR5QCz0+UAvNjnAMPExADBxsoAxsrNAMvLywDHzdEAyc7RAM3S1QDN1dsAw9PcANLS0wDR1toA1dreAMPc6QDM2eEAyt/qAMXa5gDW3eIA2N/jAM/i7gDP4u0AzuDrANTj7ADa4eYA3OTqAN7o7gDX4OUA5OruAOrt7gDi5+oA7O/wAOfs8ADv8PEA8vLyALq/wwBkZGQAmaSoAChigQC1u7sALS0tAAAAAAAAAAgAd561AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7sKDRERExQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBMTERIKu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uwoSExQUFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFBQRCru7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uxITFBYaLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uGhQUEru7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uxEUGjIyLy8vLy8vNTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NTU1NS8vLy8yMhoUE7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uxMZGjIvMTY2Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3NzYxLzIZE7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uxMaMi82Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc2MTIaGLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uxgaMDY2Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3NjAaGWRkZGRkZGIEuLu7u7u7u7u7u7u7u7u7u7u7u7u7uxgaMDY3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3NjAaGWhoaWloaGRkBLu7u7u7u7u7u7u7u7u7u7u7u7u7uxgaMDY3Nzc3Nzc3Nzc3Ojc3Nzc3Nzc3Nzc3Ojc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzo3Nzc3NzAaGGprampramllYru7u7u7u7u7u7u7u7u2CAACBAQFBQVYBQUEAgYgOTo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6OjAcGJSWl5eWlGppZbu7u7u7u7u7u7u7uwkDAgVZXGJkaZSXl2lhWgQCFzo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6OjEcG5eXmpqXl5RqaLu7u7u7u7u7u7u7uwMBBFdaXWJnapedl2pjXFYCADo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6OjEcG5manp6amZV7abu7u7u7u7u7u7u7uwMBBFdaXWJmapedl2pjXFYCADs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs6OjEcG56en5+enpaKa7u7u7u7u7u7u7u7uwkDAgVYW15iZGqUamReWgQCFzw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDs8PDw8PDw8Ozw8PDw8PDw7PDw8PDw8OzkzG56fn5+fn5mVa7u7u7u7u7u7u7u7u7u2BwACAgQEBAUFBQQEAg4gODw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDkzG5+kpKSkn5qVe7u7u7u7u7u7u7u7u7u7u7u7u7u7uxszOTw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDkzG6SlqqqlpJqWiru7u7u7u7u7u7u7u7u7u7u7u7u7uxszODw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDgzHaWqqqqqqpuYiru7u7u7u7u7u7u7u7u7u7u7u7u7uxszODw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDw8PDgzHaqqqqqqqpuYiru7u7u7u7u7u7u7u7u7u7u7u7u7uxszODw8QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI8PDgzHaqqq6uqqp6Yi7u7u7u7u7u7u7u7u7u7u7u7u7u7ux0zOEJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQjg0Haqqq6uqqp6Yi7u7u7u7u7u7u7u7u7u7u7u7u7u7ux00OEJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQjg0Haqrq6urqp6Yi7u7u7u7u7u7u7u7u7u2CAACBAQFBQVYBQUEAg4pREJDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NCQjg0HaWqq6uqpZ6Zlbu7u7u7u7u7u7u7uwkDAgVZXGJkaZSXl2lhWgQCI0JFRUVFRUVFRUVFRUVFRUVFRUdVfYSQkZGRkZGRhIFzR0VFRUVFRUVFRUVFRUVFRUVFRUVCQj40HqSlqqqlpJ6Zlbu7u7u7u7u7u7u7uwMBBFdaXWJnapedl2pjXFYCAEhFRUVFRUVFRUVFRUVFRUVShKKrrrK0tLS0tLS0sq+sqZJURUVFRUVFRUVFRUVFRUVFRUVIQj40Hp+kpaWkn5qYlbu7u7u7u7u7u7u7uwMBBFdaXWJmapedl2pjXFYCAEdHR0dHR0dHR0dHR0dHVJKutLS0tLS0tLS0tLS0tLS0tLSoVEdHR0dHR0dHR0dHR0dHR0dHRj44Hpqanp6ampiVe7u7u7u7u7u7u7u7uwkDAgVYW15iZGqUamReWgQCI0dHR0dHR0dHR0dHR0d+qLS0tLS0tLSuqqGhoaGqrrS0tLSpVEdHR0dHR0dHR0dHR0dHR0dHRkE9HpaWmZmYlpWKtbu7u7u7u7u7u7u7u7u2BwACAgQEBAQFBQQEAg4pREdHR0dHR0dHR0dHR4GstLS0tLOqh2wrKyUlJSUnK2yHnLSoVEdHR0dHR0dHR0dHR0dHR0dHRkE9HoqKiouKimu1ubu7u7u7u7u7u7u7u7u7u7u7u7u7ux49QUZHR0dHR0dHR0dHR0dHR0dHfbG0tLSvoWwfHx4kPT4+Pj49JB4fHx9JRkdHR0dHR0dHR0dHR0dHR0dHR0A9Hru7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uyQ9QEZHUlJSUlJSUlJSUlJSUlJUqLS0tK6FJSQ9REdSUlJSUlJSUlJEQT09RlJSUlJSUlJSUlJSUlJSUlJSR0A9JLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uyQ9QFJSUlJSUlJSUlJSUlJSUlKQsrS0tHglQUZSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUkA9JLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uyQ/QFJSUlJSUlJSUlJSUlJSUnOptLS0jiRRUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUkA/JGRkZGRkZGIEuLu7u7u7u7u7u7u7u7u7u7u7u7u7uyQ/QFJSUlJSUlJSUlJSUlJSUoGxtLSwbFFSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUlJSUkA/JmhoaWloaGRkBLu7u7u7u7u7u7u7u7u7u7u7u7u7uyQ/QFJSUlJSUlJSUlJSUlJSUpK0tLSOSlJSUlJSVISPj4RzUlJSUlJSUoGPj499VFJSUlJSUlJSUlJSUlJSUko/JmprampramllYru7u7u7u7u7u7u7u7u2BwACBAQFBQVYBQUEAg4nUVRUVFRUVFRUVKi0tK+GUVRUVFSPr7S0tLSxqH1UVFSPrrS0tLS0ro9zVFRUVFRUVFRUVFRSUk8/JpSWl5eWlGppZbu7u7u7u7u7u7u7uwkDAgVZXGJkaZSXl2lhWgQCI1RUVFRUVFRUVK60tLB+VFRUVI+vtLS0tLS0tKyQVISutLS0tLS0tLKgfVRUVFRUVFRUVFRUVE8/JpeXmpqXl5RqaLu7u7u7u7u7u7u7uwMBBFdaXWJnapedl2pjXVYCAFRUVFRUVFRUVLS0tKt0VFRUVKu0tLS0tLS0tLSykqK0tLS0tLS0tLSzoFRUVFRUVFRUVFRUVE8/Jpmanp6amZV7abu7u7u7u7u7u7u7uwMBBFdaXWJmapedl2pjXVYCAFRUVFRUVFRUVLS0tKtyVFRUgbS0tK+MKyVwm7G0tLS0tKt3JSeHsLS0tJFUVFRUVFRUVFRUVVFOKp6en5+enpaKa7u7u7u7u7u7u7u7uwkDAgVYW15iZGqUamReWgQCIVRUVFRUVFRUc7S0tKlVVFRUkLS0tKQrP04qJ42vtLS0tIwqTk4md7G0tKyBVFRUVFRUVFRUVVFOKp6fn5+fn5mVa7u7u7u7u7u7u7u7u7u2BwACAgQEBAUFBQQEAg4nU1RUVFRUVFRUc7S0tKlVVFRUkrS0tI5QVFRVSieMr7S0tIZTVFRTK5u0tLGSc1RUVFRUVFRUVVNOKp+kpKSkn5qVe7u7u7u7u7u7u7u7u7u7u7u7u7u7uypOU3Jzc3Nzc3Nzc3Nzc3Nzc7S0tKlzc3Nzk7S0tJNzc3Nzc1Mrh7S0tKBzc3NzVXCvtLSpgHNzc3Nzc3Nzc1NKKqSlqqqlpJqWiru7u7u7u7u7u7u7u7u7u7u7u7u7uytKU3Nzc3Nzc3Nzc3Nzc3Nzc7S0tKyBc3NzkrS0tKBzc3Nzc3NTbLS0tKhzc3Nzc3GhtLSyhHNzc3Nzc3Nzc1NKK6WqqqqqqpuYiru7u7u7u7u7u7u7u7u7u7u7u7u7uytKU3Nzc3Nzc3Nzc3Nzc3Nzc6+0tKyBc3NzibS0tKBzc3Nzc3NzU7S0tKuAc3Nzc3KOtLS0knNzc3Nzc3Nzc1NNSaqqqqqqqpuYiru7u7u7u7u7u7u7u7u7u7u7u7u7uytKU3Nzc3Nzc3Nzc3Nzc3Nzc6W0tK6Ec3NzfrS0tKt9c3Nzc3Nzc6S0tK6Dc3Nzc3OJr7S0qHNzc3Nzc3Nzc1NNS6qqq6uqqp6Yi7u7u7u7u7u7u7u7u7u7u7u7u7u7uytKU3Nzc3Nzc3Nzc3Nzc3Nzc6G0tLKQc3NzcrS0tKyBc3Nzc3Nzc5y0tLKPc3Nzc3N/sLS0q3Nzc3Nzc3Nzc1NNS6qqq6uqqp6Yi7u7u7u7u7u7u7u7u7u7u7u7u7u7uytNU3Nzc3NzdXNzdXVzc3Nzc420tLOSc3Nzcqu0tLKQc3Nzc3Nzc6O0tLGSc3Nzc3N+q7S0sXNzc3Nzc3Nzc3FNS6qqq6urqp6Yi7u7u7u7u7u7u7u7u7u2CAACBAQFBQVYVgUEAg8ocnN9fX19fX19fX+xtLSigH19fYy0tLSigH19fX19fai0tLSgfX19fX1+rLS0tH19fX19fX1zc3FNS6Wqq6uqpZ6Zlbu7u7u7u7u7u7u7uwkDAgVZXGJkaZSXl2lhWgQCIXOAgICAgICAgH6rtLSrgYCAgHmvtLSuj4CAgICAgK60tLSogYCAgICBrLS0tICAgICAgIBzc3FNS6SlqqqlpJuZlbu7u7u7u7u7u7u7uwMBBFdaXWJnapedl2pjXFYCAHWAgICAgICAgHWhtLSzj4CAgHSbtLS0qYCAgICAj7S0tLSrgYCAgICBrLS0tICAgICAgIBzc3FNS5+kpaWkn5qYlbu7u7u7u7u7u7u7uwMBBFdaXWJmapedl2pjXFYCAH2AgICAgICAgHOIr7S0ooCAgHJ4sLS0tKmEgYGRr7S0tLSyhICAgICBrLS0tICAgICAgIB9fXFNS5qanp6ampiVe7u7u7u7u7u7u7u7uwkDAgVYW15iZGqUamReWgQCIX2AgICAgICAgHN5q7S0sYGAgHVujLS0tLSuqauytLSur7S0kYCAgICErrS0tICAgICAgIB9fXFNS5aWmZmYlpWKtbu7u7u7u7u7u7u7u7u2BwACAgQEBAUFBQQEAg4ocn2AgICAgICAgIBxnLS0tJOAgIByLJy0tLS0tLS0tK56rbS0k4CAgICQr7S0sICAgICAgIB9fXFNS4qKiouKimu1ubu7u7u7u7u7u7u7u7u7u7u7u7u7uytNcXV9gICAgICAgICAgICAgIB1cK+0tK+QgICAcit6qq+0tLSvqndLjq+voICAgICTs7S0oX2AgICAgIB9fXFNS7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uytNcXV9gICAgICAgICAgICAgICAbo20tLSphICAgHErX3qNnI16X0xyb3p6doCAgIGptLS0jYCAgICAgIB9fXFNS7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u0tNcXV9gICAgICAgICAgICAgICAfXCqtLSzooCAgIByTScnLScrTXWAcSsrUICAgJGxtLSveYCAgICAgICAfXFNS7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u0tNcXV9gICAgICAgICAgICAgICAgHF4sLS0tKKEgICAgIB1cnSAgICAgICAgICAj6+0tLScdICAgICAgICAfXFNS2RkZGRkZGIEuLu7u7u7u7u7u7u7u7u7u7u7u7u7u0tNcX19gICAgICAgICAgICAgICAgH1NhbG0tLOpkYCAgICAgICAgICAgICAgICPrLS0tLB4dICAgICAgICAfXFNS2hoaWloaGRkBLu7u7u7u7u7u7u7u7u7u7u7u7u7u0tucX19gICAgICAgICAgICAgICAgIB0LIyxtLS0sqCEgICAgICAgICAgICAgZOutLS0tI1ufYCAgICAgICAfXJuTGprampramllYru7u7u7u7u7u7u7u7u2CAACBAQFBQVYBQUEAg8tdICAgICAgICAgICAgICAdCx6q7S0tLSzq5OPj4SEhISEj5Gir7S0tLSvmyx0gICAgICAgICAfXJuTJSWl5eWlGppZbu7u7u7u7u7u7u7uwkDAgVZXGJkaZSXl2lhWgQCIoCAgICAgICAgICAgICAgHQsd6GvtLS0tLSxr6urq6usr7S0tLS0tK+NLHKAgICAgICAgICAfXJuTJeXmpqXl5RqaLu7u7u7u7u7u7u7uwMBBFdaXWJnapedl2pjXFYCAICAgIGAgICAgYGAgICAgIB0TF+FqrS0tLS0tLS0tLS0tLS0tLS0pXcscYCAgICAgIGBgICAfXJuTJmanp6amZV7abu7u7u7u7u7u7u7uwMBBFdaXWJmapedl2pjXFYCAICAgYGBgYGAgYGBgYCAgYGBgXEsJ3eOpbCus7S0tLS0tK+wqo53K0x0gYGBgYGBgYCAgYGAgHRuTJ6en5+enpaKa7u7u7u7u7u7u7u7uwkDAgVYW15iZGqUamReWgQCIoCAgYGBgYGBgYGBgYCAgYGBgYB1cUwrLHd4h4yMjIyMjHp3XytMcX2BgYGBgYGBgYCAgIGAgHRuTJ6fn5+fn5mVa7u7u7u7u7u7u7u7u7u2BwACAgQEBAUFBQQEAg9gdYCAgICBgYGBgYGBgYCAgICBgYGBgH10cUwsJycnJycnJyxMbnR9gICAgYGBgICAgICAgYGBgHRuTJ+kpKSkn5qVe7u7u7u7u7u7u7u7u7u7u7u7u7u7u0xudH2BgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGAgHRuTKSlqqqlpJqWiru7u7u7u7u7u7u7u7u7u7u7u7u7u0xudH6BgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRuTKWqqqqqqpuYiru7u7u7u7u7u7u7u7u7u7u7u7u7u0xudH6BgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRuTKqqqqqqqpuYiru7u7u7u7u7u7u7u7u7u7u7u7u7u0xudIGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRuTKqqq6uqqp6Yi7u7u7u7u7u7u7u7u7u7u7u7u7u7u0xudIGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRuTKqqq6uqqp6Yi7u7u7u7u7u7u7u7u7u7u7u7u7u7u0xudIGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRuTKqrq6urqp6Yi7u7u7u7u7u7u7u7u7u2CAACBAQFBQVYBQUEAg9geYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRubKWqq6uqpZ6Zlbu7u7u7u7u7u7u7uwkDAgVZXGJkaZSXl2lhWgQCIoGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRubKSlqqqlpJuZlbu7u7u7u7u7u7u7uwMBBFdaXWJnapedl2pjXFYCAIGBgYGBgYGCgYGCgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYKCgoKBgYGBgYGBgYKCgoKCgYGBgXRvbJ+kpaWkn5qYlbu7u7u7u7u7u7u7uwMBBFdaXWJmapedl2pjXVYCAIGBgYGBgYKCgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGCgoGBgYGBgYGBgYGBgoGBgYGBgXRvbJqanp6ampiVe7u7u7u7u7u7u7u7uwkDAgVYW15iZGqUamReWgQCIoGBgYGBgYKBgYGBgoGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgYGBgXRvbJaWmZmYlpWKtbu7u7u7u7u7u7u7u7u2BwACAgQEBAUFBQQEAg9gfoGCgoKEhISEgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKBgXVvbIqKi4uKimu1ubu7u7u7u7u7u7u7u7u7u7u7u7u7u2xvdIGBgoKCgoKCgoKEgoSEgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKBgXRvbLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u2xvdIGBhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISDgXRvbLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u2xvdH6Bg4OEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhIODgXlvbLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u19udH6Bg4SEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhISEhIOBfnRvbLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u19udn5+gYODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODgYF+fnRvbLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u19sb3l+foOBg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4OBfn5+fnRvbLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uy14o6KipqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqampqaooqN8LLu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7uwtteoaGiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIhoZwDru7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7gLLV9fbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxsbGxfXy0Lu7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u///////////////////////////////////////////////////////////////////////////////////////////////////AAAAAAAAAAD////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP/4AAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/4AAAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP/4AAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/4AAAAAAAAAAAAAP//+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP/4AAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/4AAAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP/4AAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/4AAAAAAAAAAAAAP//+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP/4AAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/4AAAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP//+AAAAAAAAAAAAP/4AAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/wAAAAAAAAAAAAAP/4AAAAAAAAAAAAAP//+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAB////+AAAAAAAAAAD//////////////////////////////////////////////////////////////////////////////////////////////////ygAAACAAAAAAAEAAAEACAAAAAAAAEAAAAAAAAAAAAAAAAEAAAABAABmZmYAaGZmAGlpaABkcnoAa3Z8AG94fQBwb28AdXRzAHh2dQB8e3oAZWVlAGZmZgA3gagANn6kAGl5ggBxfYUAOoWtADqFrQA6h7EAO4qzAD+RvQBDjbQAQZK+AEuStwBPk7gAV4+uAFmOqgBZkK4AVJW5AFyauwBbk7EAZJaxAGOdvQBum7UAap68AGeYtABynbUAdp+2AGuWrgBtor8AdaK9AHuhtgB+prwAfKW8AIGnvQB+pLsAQpfFAEOYxgBHns0ASpvGAEufywBRn8gAR6DPAEihzwBNpNIAVaHKAFykywBSptMAVqjUAFur1QBkosQAYqfMAGapzQBrq84Aa6TDAGKu1wBlr9gAbK7RAGaw2ABssdcAa7LZAGew1gB0pcAAdKzMAHmnwgB8qsMAfq7LAHGu0AB/sM0AdrHSAHO22wB3uNwAfLTSAHu63ACAfn0AhIKCAIiHhgCNjIwAlJKSAJuamgCBp7wAg6m/AIanugCjoqIAqKinAKysqwCusLEAsK+vALS1tQC0t7kAtru9ALy9vQCoqKcAvcLGAIWtxACHrcMAgrLNAI2zyQCDttQAhrjVAIS+3gCMu9YAi73bAJe2yACSvtkAkr3UAKG8ywC5vsIAh8DfAIrA3wCVw90AnMPZAIzC4QCTxuIAl8jkAJnH4gCZyOQAqcHOAKTB0QCqxtYAvsLFAL7EyQC6ztkAtcvYAL/Q2gCkzeQAqc/kAK3R5QC01ecAu9XkAL3Z6AC51+gAxcXGAMHGyQDGycsAysvLAMfN0ADKztEAwtLbAM3S1QDO1doA0M/PANTV1QDQ1doA0dbaANTa3QDD2+kAzN/qAMvb4wDV3OIA2d/kANjf5ADP4u4Az+LtAM3g6wDT4+wA2uHmANrh5gDe5eoA3eTqAN7p7gDX4OYA4efqAOPr7wDp7O4A6u7wAPLy8gAwb5EAZmZmAHqhtAC0u74AoaGhABkZGQCIiIgAAAAAAAQECwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLEMEBAQEhITExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMSEhAQEAyxuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLixDRAQEBATExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTEBAQEA2xuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uAwQExMUFC4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4WFhMTEAy4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4EBATExYWLi4uLi8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLy8vLi4uLhYWExMQELi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLgRERYWLy8wMDAwMDAwMDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0MDAwMDAwLy8WFhISuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uBISFhYvLzAwMDA0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0MDAvLxYWExO4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4ExMuLjIyNjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjAwLi8VFbi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLgVFS4uMjI2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2MDAxMRUVuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uBUVMTE1NTY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY1NTExFRVeXl9fX19eXl5msri4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4FRUxMTU1NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjU1MTEVFV5eX19fX19fXl5dsri4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLgVFTExNjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjYxMRUVZWVlZWVlZWViYl5muLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uBUVMTE2Njk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk2OTExFRVlZWVlZWVlZWJiXl64uLi4uLi4uLi4uLi4uLi4uLi4CgABAgICAgICAgICAgICAAADGjk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5MTEVFY6OkZGRkY6OZWVgYLi4uLi4uLi4uLi4uLi4uLi4CgACBwlXWVldX2JljpGOYl1XVQYAAzc5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTk5OTkyMhcXkJCRkZGRkJCCgmJiuLi4uLi4uLi4uLi4uLi4uAsAAgdUVlhZXV9iZY6XmJFlYV1YVQcADjo6Ojo6Ojo6OTk6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo5OTo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo5OTIyFxeTk5iYmJiTk4+PYmK4uLi4uLi4uLi4uLi4uLi4AAACB1RWWFldX2JljpeYkWVhXVhVCAAAOjo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6MjIXF5WVmpqampWVj49jY7i4uLi4uLi4uLi4uLi4uLgAAAIHVFZYWV1fYmWOl5iRZWFdWFUIAQA6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6Ojo6OjozMxcXmpqbm5ubmpqQkGRkuLi4uLi4uLi4uLi4uLi4uAsAAgdUVlhZXV9iZY6XmJFlYV1YVQcADjs7Ozs7Ojo6Ojo7Ozs7Ozs6Ojo6Ojs7Ozs7Ozs7Ojo6Ozs7Ozs6Ojo6Ojs7Ozs7Ozo6Ojo6Ozs7Ozs7Ozs6Ojo7Ozs7Ozo6Ojc3GBiampubm5uampOTZGS4uLi4uLi4uLi4uLi4uLi4uAoAAgcJVVZXWFldXl9iX11ZV1UGAAM4Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozo6NzcYGJubn5+fn5ubk5N1dbi4uLi4uLi4uLi4uLi4uLi4uLIKAAACAgICAgICAgICAgIAAAMbOzs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs3NxgYn5+hoaGhn5+VlXV1uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uBgYNzc7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozc3GBifn6enp6efn5aWgoK4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4GBg3Nzs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7NzgcGKGhp6enp6GhlpaCgri4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLgcHDg4Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs4OBwYp6empqamp6eWloODuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uBwcODg7Ozs7Ozs7Ozs7O0E7Ozs7OztBQUE7Ozs7Ozs7OztBQUE7Ozs7Ozs7Ozs7Ozs7Ozs7O0FBQTs7Ozs7Ozs7O0FBQTs7Ozs7Ozs7OztBOzs7Ozg4HBynp6ioqKinp5qag4O4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4HBw4ODs7QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQTtBODgcHKenqKioqKammpqDg7i4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLgcHDg4QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE4OBwcp6eoqKiopqaamoODuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uBwcODhBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQTg4HBynp6ioqKinp5qag4O4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4HBw4OEFBQkJCQkJCQkJBQUFBQkJCQkJCQkJBQUFBQkJCQkJCQkJCQkJCQkJBQUJCQkJCQkJCQUFBQUJCQkJCQkJCQUFBQUJCQUFCQkJCQkJCQkFBODgcHKenqKioqKenmpqDg7i4uLi4uLi4uLi4uLi4uLi4uLIKAAACAgICAgICAgICAgIAAAMfQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQUE4OBwcoaGoqKiooaGamo+PuLi4uLi4uLi4uLi4uLi4uLgKAAIHCVdYWV1fYmWOkY5iXVdVBgADQ0RERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERERCQj09HR2hoaioqKinp5qaj4+4uLi4uLi4uLi4uLi4uLi4CwACB1RWWFldX2JljpeYkWVhXVhVBwAORERERERERERERERERERERERERERERERERG6HjKSvsLCwsLCwsLCwpJyJe1BEREREREREREREREREREREREREREREREREREJCPT0dHZuboaGhoZublZWPj7i4uLi4uLi4uLi4uLi4uLgAAAIHVFZYWV1fYmWOl5iRZWFdWFUIAABGRkZGRkZGREZGRkRGRkZGRkZGRkZGdoyvsLCwsLCwsLCwsLCwsLCwsLCwsKmHRkZGRkZERkZGRkZGRkZGRkZGRkZGRkZGR0c9PR0dm5uhoaGhn5+Wlo+PuLi4uLi4uLi4uLi4uLi4uAAAAgdUVlhZXV9iZY6XmJFlYV1YVQgBAEZGRkZGRkZGRkZGRkZGRkZGRkZGd6WwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCHRkZGRkZGRkZGRkZGRkZGRkZGRkZGRkZHRz09HR2VlZqampqVlZKSg4O4uLi4uLi4uLi4uLi4uLi4CgACB1RWWFldX2JljpeYkWVhXVhVBwAORkZGRkZGRkZGRkZGRkZGRkZGUJywsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsIlGRkZGRkZGRkZGRkZGRkZGRkZGRUVGRkdHPj4dHZWVmpqampWVk5ODZ7i4uLi4uLi4uLi4uLi4uLi4CgACBwlVVldYWV1eX2JfXVlXVQYAA0NFRUVFRUVFRUVFRUVFRUVFRW6tsLCwsLCwsLCwsK6elICAgICAhZ6osLCwsLCwiUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFR0c+Ph0dgoKPj4+Pj4+CgmS1uLi4uLi4uLi4uLi4uLi4uLi4sgoAAAICAgICAgICAgICAgAAAyZFRUVFRUVFRUVFRUVFRUVFRUVur7CwsLCwsLCuhGgfGRkZGRkZGRkZGRkZKoCUsLCJRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRT4+HR2Dg4+Pj4+Pj4Jntbi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4HR0+PkVFRkZGRkZGRkZGRkZGRkZGRkZGRkZGU6+wsLCwsLCugB8bGxseHR08PDw8PCAdHhsbGxsbKkBGRkZGRkZGRkZGRkZGRkZGRkZGRkZGRkVFPj4dHbi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLggID4+RUVQUFBQUFBQUFBQUEZQUFBQUFBQUFCpsLCwsLCwhB8bHiA+RVBQUEZQRkZQUFBQUD48IBsbPFBQRkZQUFBQUFBGUFBQUFBQUFBQUFBQRUU+PiAguLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uCAgPj5QUFBQUFBQUFBQUFBQUFBQUFBQUFBQiLCwsLCwsHEbHjxFUFBQUFBQUFBQUFBQUFBQUFBQUD9FUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUD4+ICC4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4ICA/P1BQUFBQUFBQUFBQUFBQUFBQUFBQUFGvsLCwsLBxGyBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQPz8gILi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLggID8/UFBQUFBQUFBQUFBQUFBQUFBQUFBQh7CwsLCwlBs/UFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBRUFBQUFBQUFA/PyAgXl5fX19fXl5dZrK4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uCAgPz9QUFFRUVFRUVFRUFFRUFFRUVFRUVGpsLCwsK4kQFFRUVFRUVFRUVFRUVFRUVBQUVFRUVFRUVFRUVFRUVFRUVBRUVBRUVFRUVFRUVFRUVFQUT8/ICBeXl9fX19eXl5eXbK4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4ICA/P1FRUVFRUVFRUVFRUVFRUVFRUVFRbrCwsLCwhCBRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRSUkgIGVlZWVlZWVlYmJeZri4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLggIE1NUVFRUVFRUVFRUVFRUVFRUVFRUVGHsLCwsLAqT1FRUVFRUVFTh42NjY17UVFRUVFRUVFRUX6NjY2Nh25RUVFRUVFRUVFRUVFRUVFRUVFRUVFNTSAgZWVlZWVlZWViYl5euLi4uLi4uLi4uLi4uLi4uLi4sgoAAAICAgICAgICAgICAgAABCFRUVFRUVFRUVFRUVFRUYywsLCwqEBRUVFRUVFRjbCwsLCwsLCtjVFRUVFRUYmvsLCwsLCwsKV7UVFRUVFRUVFRUVFRUVFRUVFRUU1NICCOjpGRkZGOjnV1YGC4uLi4uLi4uLi4uLi4uLi4uAoAAgcJV1hZXV9iZY6RjmJdV1UGAARPUVFRUVFRUVFRUVFRpbCwsLCET1FRUVFRUZywsLCwsLCwsLCwrXtRUVGMsLCwsLCwsLCwsLCcblFRUVFRUVFRUVFRUVFRUVFRTU0iIpCQkZGRkZCQgoJiYri4uLi4uLi4uLi4uLi4uLgLAAIHVFZYWV1fYmWOl5iRZWFdWFUHAA5RUVFRUVFRUVFRUVGvsLCwsIFRUVFRUVGIsLCwsLCwsLCwsLCwsIdRfbCwsLCwsLCwsLCwsLCpblFRUVFRUVFRUVFRUVFRUVFNTSIik5OYmJiYk5OPj2JiuLi4uLi4uLi4uLi4uLi4uAAAAgdUVlhZXV9iZY6XmJFlYV1YVQgAAFNTU1NTU1NTU1NTU7CwsLCwc1NTU1NTU62wsLCwsLCwsLCwsLCwsI2ksLCwsLCwsLCwsLCwsLCkU1NTU1NTU1NTU1NTU1NTU09PJyeVlZqampqVlZCQY2O4uLi4uLi4uLi4uLi4uLi4AAACB1RWWFldX2JljpeYkWVhXVhVBwAAU1NTU1NTU1NTU1NTsLCwsLByU1NTU1N7sLCwsLCudCEfKoSwsLCwsLCwsLCwlCofIXGusLCwsLCKU1NTU1NTU1NTU1NTU1NTT08nJ5qam5ubm5qakJBkZLi4uLi4uLi4uLi4uLi4uLgLAAIHVFZYWV1fYmWOl5iRZWFdWFUHAA5TU1NTU1NTU1NTU1OwsLCwsGxTU1NT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sKp6enp6enp2jbCwsLCwsJ16enp2dnZ6h7CwsLCwenp6enp6enp6enBwbGxISJuboaGhoZ+flpaPj7i4uLi4uLi4uLi4uLi4uLgAAAIHVFZYWV1fYmWOl5iRZWFdWFUIAQB6enp6enp6enp6enp6S66wsLCwinp6enp6KJ+wsLCwsKp6enp6eomwsLCwsLCwqnp6enp6enqHsLCwsLB6enp6enp6enp6dnZsbEhIlZWampqalZWSkoODuLi4uLi4uLi4uLi4uLi4uAsAAgdUVlhZXV9iZY6XmJFlYV1YVQcAD3p6enp6enp6enp6enpslLCwsLCtenp6enpsW66wsLCwsK+cioypsLCwsLCwsLCwenp6enp6eoewsLCwsHp6enp6enp6enp2dmxsSEiVlZqampqWlpOTg2e4uLi4uLi4uLi4uLi4uLi4uAoAAgcJVVZXWFldXl9iX11ZV1UGAARuenp6enp6enp6enp6enppsLCwsLCHenp6enoocbCwsLCwsLCwsLCwsLCwlLCwsLB+enp6enp6jbCwsLCwenp6enp6enp6enZ2bGxISIKCj4+Pj4+PgoJktbi4uLi4uLi4uLi4uLi4uLi4uLIKAAACAgICAgICAgICAgIAAAQlenp6enp6enp6enp6enp6ekufsLCwsKp6enp6em0kdLCwsLCwsLCwsLCwsIUlsLCwsIp6enp6enqdsLCwsKd6enp6enp6enp6dnZsbEhIg4OPj4+Pj4+CZ7W4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uEpKbGxwcHp6enp6enp6enp6enp6enp6enp6bWuwsLCwsI16enp6emoha6ywsLCwsLCwsLB/IUussLCwjHp6enp6eq2wsLCwlnp6enp6enp6enp2dmxsSkq4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4SkpsbHZ2enp6enp6enp6enp6enp6enp6enp6KJ+wsLCwr3x6enp6emohKX+nsLCwsLCUWyFLcHSfn595enp6enp+sLCwsLCBenp6enp6enp6enZ2bGxKSri4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhKSmxsdnZ6enp6enp6enp6enp6enp6enp6enptWrCwsLCwqnt6enp6em0kJCQka2tbJCQkTnp6JCQkJEx6enp6eqSwsLCwsGt6enp6enp6enp6dnZsbEpKuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uEpKbGx2dnp6enp6enp6enp6enp6enp6enp6enoohbCwsLCwpHp6enp6enBOKCQkJCQkS256enpsKCgobXp6enp+sLCwsLCnTnp6enp6enp6enp2dmxsSkq4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4SGpwcHd3enp6enp6enp6enp6enp6enp6enp6enAkp7CwsLCwpHt6enp6enp6em1tb3p6enp6enp6enp6enp6e6qwsLCwsHRvenp6enp6enp6end3bGxKSl5eX19fX15eXWayuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhqam9wd3d6enp6enp6enp6enp6enp6enp6enp6emoprrCwsLCwqn56enp6enp6enp6enp6enp6enp6enp6enuqsLCwsLCsKHp6enp6enp6enp6d3dsbEpKXl5fX19fX19eXl2yuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uEtLb293d3p6enp6enp6enp6enp6enp6enp6enp6eihbrrCwsLCwr416enp6enp6enp6enp6enp6enp6enp+qrCwsLCwsHFsenp6enp6enp6enp3d21tS0tlZWVlZWVlZWJiXma4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4S0tvb3d3enp6enp6enp6enp6enp6enp6enp6enp6dyVbrrCwsLCwsK2Kenp6enp6enp6enp6enp6enp7nLCwsLCwsLCUKHp6enp6enp6enp6end3bW1LS2VlZWWCgmVlYmJeXri4uLi4uLi4uLi4uLi4uLi4uLIKAAACAgICAgICAgICAgIBAQQpenp6enp6enp6enp6enp6enp6enp6byRbrLCwsLCwsLCvnIl6enp6enp6enp6enqJpK+wsLCwsLCwpyRtenp6enp6enp6enp6d3dtbUtLjo6RkZGRjo5lZWBguLi4uLi4uLi4uLi4uLi4uLgKAAIHCVdYWV1fYriOkY5iXVdVBgAEcHp6enp6enp6enp6enp6enp6enp6byQplrCwsLCwsLCwsLCtqZycnJycnKmqsLCwsLCwsLCwsJQpS3p6enp6enp6enp6enp3d21tS0uQkJGRkZGQkIKCYmK4uLi4uLi4uLi4uLi4uLi4CwACB1RWWFldX2JljpeYkWVhXVhVBwAPenp6ent7e3t7enp6e3t7e3t6enp6bygkcaywsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCFJCh6enp6e3t7e3p6enp7e3d3bW1LS5OTmJiYmJOTj49iYri4uLi4uLi4uLi4uLi4uLgAAAIHVFZYWV1fYmWOl5iRZWFdWFUIAAB6enp6e3t7e3t6enp7e3t7enp6ent6d0skKX+ssLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCUWiRLd3p6enp7e3t7enp6e3t7enptbUtLlZWampqalZWPj2NjuLi4uLi4uLi4uLi4uLi4uAAAAgdUVlhZXV9iZY6XmJFlYV1YVQgBAHp6e3t7e3p6e3t7enp6e3t6enp6e3t7e20oJCRxlK6wsLCwsLCwsLCwsLCwsLCwsKyEWiQkanp6e3t7enp6e3t7enp6e3t6em1tS0uampubm5uampCQZGS4uLi4uLi4uLi4uLi4uLi4CgACB1RWWFldX2JljpeYkWVhXVhVBwAPenp7e3t7enp7e3t6enp7e3p6enp7e3t6e3dqKCQkJGuFlp+wsLCwsLCwsLCflH9pJCQkS3B7enp7e3t6enp7e3t7enp7e3p6bW1LS5qam5ubm5qak5NkZLi4uLi4uLi4uLi4uLi4uLi4CgACBwlVVldYWV1eX2JfXVlXVQYABHB6e3t7enp6ent7e3t6enp7enp7e3t7enp7e3t7bUslJCQkJCQkJCQkJCQkJCQkJCQoanB7enp6ent7e3t6enp7enp6ent7enpvb0tLm5ufn5+fm5uTk3V1uLi4uLi4uLi4uLi4uLi4uLi4sgoAAAICAgICAgICAgICAgAABFp7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3tvaktKJSUlJSUkJCUlSktqcHt7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e29vS0ufn6GhoaGfn5WVdXW4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4S0tvb3p6e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7b29LS5+fp6enp5+flpaCgri4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhLS29venp7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3pvb0tLoaGnp6enoaGWloKCuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uEtLb297e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e29vS0unp6ampqanp5aWg4O4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4S0tvb3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7b29LS6enqKioqKenmpqDg7i4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhLS29ve3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3tvb0tLp6eoqKiop6eamoODuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uEtLb297e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e29vS0unp6ioqKinp5qag4O4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4S0tvb3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7b29LS6enqKioqKenmpqDg7i4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhLS29ve3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3tvb2hop6eoqKiop6eamoODuLi4uLi4uLi4uLi4uLi4uLi4sgoAAAICAgICAgICAgICAgAABVx7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e29vaGihoaioqKihoZqaj4+4uLi4uLi4uLi4uLi4uLi4uAoAAgcJV1hZXV9iZY6RjmJdV1UGAARye3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7b29oaKGhqKioqKenmpqPj7i4uLi4uLi4uLi4uLi4uLgLAAIHVFZYWV1fYmWOl5iRZWFdWFUHAA97e3t7e3t7e3t7e3t7e3t8e3t7e3t7e3t7e3t8e3t7e3t7e3t7e3t8e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3tvb2hom5uhoaGhm5uVlY+PuLi4uLi4uLi4uLi4uLi4uAAAAgdUVlhZXV9iZY6XmJFlYV1YVQgAAHt7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e29vaGibm6GhoaGfn5aWj4+4uLi4uLi4uLi4uLi4uLi4AAACB1RWWFldX2JljpeYkWVhXVhVCAEAe3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7b29oaJWVmpqampWVkpKDg7i4uLi4uLi4uLi4uLi4uLgLAAIHVFZYWV1fYmWOl5iRZWFdWFUHAA97e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3t7e3tvb2holZWampqalZaTk4NnuLi4uLi4uLi4uLi4uLi4uLgKAAIHCVVWV1hZXV5fYl9dWVdVBgAEeHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx7e3BwaGiCgo+Pj4+Pj4KCZLW4uLi4uLi4uLi4uLi4uLi4uLiyCgAAAgICAgICAgICAgICAAAFXHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHt7cHBoaIODj4+Pj4+Pgme1uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhoaG9ve3t8fH5+fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8e3tvb2houLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uGhob297e3x8fn5+fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx+fHx8fHx7e3JyaGi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4aGhvb3t7fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fH5+fHx8fHt7b29oaLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhoaG9ve3t8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8e31ycmhouLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uGhob294eH19fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fX14eG9vaGi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4aGhvb3h4fX18fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx8fHx+fnh4b29oaLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLhbW2trcnJ4eH17fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX17e3h4eHhvb2houLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uFtba2tycnh4fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fX19fXt7eHh4eG9vaGi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4KiqLnp2do6Ojo6Wlo6Ojo6WlpaOjo6OjpaWlo6Ojo6Ojo6Wlo6Ojo6Ojo6OjpaWlo6Ojo6Ojo6Ojo6Wlo6Ojo6WlpaWjo6OjpaWlo6Ojo6OkpJ2dnYtoaLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLgmKouenZ2jo6OjpaOjo6OjpaOlpaOjo6Olo6Wlo6Ojo6OjpaWjo6Ojo6Ojo6OjpaWjo6Ojo6Ojo6OjpaWjo6OjpaOlpaOjo6Olo6Ojo6Ojo6SknZ2di2gtuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLIpKipoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaFpaKbK4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLItWmhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoaGhoWi2yuLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uLi4uP/////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////4AAAAAAAAAAAAAB//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAAAH////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD//+AAAAAAAAAAAAAAAAAA//+AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//4AAAAAAAAAAAAAAAAAA///AAAAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA///AAAAAAAAAAAAAAAAAAP//gAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//+AAAAAAAAAAAAAAAAAAP//wAAAAAAAAAAAAAAAAAH////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAAAf////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP//wAAAAAAAAAAAAAAAAAD//4AAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//gAAAAAAAAAAAAAAAAAD//8AAAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD//+AAAAAAAAAAAAAAAAAA//+AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//4AAAAAAAAAAAAAAAAAA///AAAAAAAAAAAAAAAAAAf////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAAAB////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA///AAAAAAAAAAAAAAAAAAP//gAgAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//+AAAAAAAAAAAAAAAAAAP//wAAAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP////AAAAAAAAAAAAAAAAD////wAAAAAAAAAAAAAAAA////8AAAAAAAAAAAAAAAAP//wAAAAAAAAAAAAAAAAAD//4AAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//AAAAAAAAAAAAAAAAAAD//wAAAAAAAAAAAAAAAAAA//8AAAAAAAAAAAAAAAAAAP//gAAAAAAAAAAAAAAAAAD//8AAAAAAAAAAAAAAAAAB////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////wAAAAAAAAAAAAAA//////8AAAAAAAAAAAAAAP//////AAAAAAAAAAAAAAD//////4AAAAAAAAAAAAAB//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////` // иконка в base64
)
