package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"path/filepath"
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
	SocketPath   string `json:"socket_path"`
}

var (
	config        Config
	mainWindow    *gtk.Window
	treeView      *gtk.TreeView
	searchEntry   *gtk.Entry
	resultsView   *gtk.TreeView
	detailsView   *gtk.TextView
	detailsBuffer *gtk.TextBuffer
	socketPath    string
	indicator     *appindicator.Indicator
)

func main() {
	// Проверяем, не запущен ли уже экземпляр программы
	if isAlreadyRunning() {
		fmt.Println("Программа уже запущена. Активируем существующий экземпляр...")
		activateExistingInstance()
		os.Exit(0)
	}

	// Загружаем конфигурацию
	loadConfig()

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
		config = Config{
			LDAPServer:   "ldap://localhost:389",
			BindDN:       "cn=admin,dc=example,dc=com",
			BindPassword: "password",
			BaseDN:       "dc=example,dc=com",
			SocketPath:   filepath.Join(os.TempDir(), "ldap-phonebook.sock"),
		}

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

	socketPath = config.SocketPath
}

func createMainWindow() {
	var err error

	// Создаем главное окно
	mainWindow, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		fmt.Printf("Ошибка создания окна: %v\n", err)
		os.Exit(1)
	}

	mainWindow.SetTitle("LDAP Телефонный справочник")
	mainWindow.SetDefaultSize(1000, 600)
	mainWindow.Connect("destroy", func() {
		gtk.MainQuit()
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

	// Добавляем дерево в прокручиваемую область
	scrolledWindow, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		fmt.Printf("Ошибка создания прокручиваемой области: %v\n", err)
		os.Exit(1)
	}

	scrolledWindow.Add(treeView)
	leftPanel.PackStart(scrolledWindow, true, true, 0)

	// Центральная панель - вертикальный контейнер
	centerPanel, err := gtk.PanedNew(gtk.ORIENTATION_VERTICAL)
	if err != nil {
		fmt.Printf("Ошибка создания центральной панели: %v\n", err)
		os.Exit(1)
	}

	// Верхняя часть центральной панели - поиск
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

	searchEntry.SetPlaceholderText("Поиск по ФИО, email, телефону...")

	searchButton, err := gtk.ButtonNewWithLabel("Поиск")
	if err != nil {
		fmt.Printf("Ошибка создания кнопки поиска: %v\n", err)
		os.Exit(1)
	}

	searchButton.Connect("clicked", func() {
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

	searchBox.PackStart(searchEntry, true, true, 0)
	searchBox.PackStart(searchButton, false, false, 0)
	searchBox.PackStart(exitButton, false, false, 0)

	// Центральная часть - результаты поиска
	resultsView, err = gtk.TreeViewNew()
	if err != nil {
		fmt.Printf("Ошибка создания таблицы результатов: %v\n", err)
		os.Exit(1)
	}

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

	// Добавляем колонки
	addColumn(resultsView, "ФИО", 0)
	addColumn(resultsView, "Email", 1)
	addColumn(resultsView, "Телефон", 2)
	addColumn(resultsView, "Отдел", 3)
	addColumn(resultsView, "Организация", 4)

	// Прокручиваемая область для результатов
	resultsScrolled, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		fmt.Printf("Ошибка создания прокручиваемой области результатов: %v\n", err)
		os.Exit(1)
	}

	resultsScrolled.Add(resultsView)

	// Контейнер для верхней и центральной части
	topCenterBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	if err != nil {
		fmt.Printf("Ошибка создания контейнера: %v\n", err)
		os.Exit(1)
	}

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

	// Контекстное меню для детальной информации
	detailsView.Connect("button-press-event", func(v *gtk.TextView, ev *gdk.Event) {
		event := gdk.EventButtonNewFromEvent(ev)
		if event.Button() == 3 { // Правая кнопка мыши
			menu, err := gtk.MenuNew()
			if err != nil {
				return
			}

			copyItem, err := gtk.MenuItemNewWithLabel("Копировать")
			if err != nil {
				return
			}

			copyItem.Connect("activate", func() {
				start, end := detailsBuffer.GetBounds()
				text, err := detailsBuffer.GetText(start, end, false)
				if err == nil {
					clipboard, err := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD)
					if err == nil {
						clipboard.SetText(text)
					}
				}
			})

			menu.Append(copyItem)
			menu.ShowAll()
			menu.PopupAtPointer(ev)
		}
	})

	// Прокручиваемая область для детальной информации
	detailsScrolled, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		fmt.Printf("Ошибка создания прокручиваемой области деталей: %v\n", err)
		os.Exit(1)
	}

	detailsScrolled.Add(detailsView)
	detailsBox.PackStart(detailsScrolled, true, true, 0)

	// Устанавливаем минимальный размер для нижней панели
	detailsBox.SetSizeRequest(-1, 100)

	// Добавляем части в вертикальный разделитель
	centerPanel.Pack1(topCenterBox, true, false)
	centerPanel.Pack2(detailsBox, false, false)
	centerPanel.SetPosition(int(float64(mainWindow.GetAllocatedHeight()) * 0.8))

	// Добавляем панели в горизонтальный разделитель
	mainPaned.Pack1(leftPanel, false, false)
	mainPaned.Pack2(centerPanel, true, false)
	mainPaned.SetPosition(int(float64(mainWindow.GetAllocatedWidth()) * 0.25))

	// Добавляем главный контейнер в окно
	mainWindow.Add(mainPaned)

	// Настройка обработчиков событий
	setupEventHandlers()
}

func createStatusIndicator() {
	indicator = appindicator.New("ldap-phonebook", "system-users", appindicator.CategoryApplicationStatus)
	indicator.SetStatus(appindicator.StatusActive)

	// Создаем меню
	menu, err := gtk.MenuNew()
	if err != nil {
		fmt.Printf("Ошибка создания меню: %v\n", err)
		return
	}

	// Пункт "Показать"
	showItem, err := gtk.MenuItemNewWithLabel("Показать")
	if err != nil {
		fmt.Printf("Ошибка создания пункта меню: %v\n", err)
		return
	}

	showItem.Connect("activate", func() {
		if mainWindow.GetVisible() {
			mainWindow.Hide()
		} else {
			mainWindow.Present()
		}
	})

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
		"(objectClass=organization)",
		[]string{"o"},
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

		// Добавляем организации и отделы
		for _, entry := range sr.Entries {
			orgName := entry.GetAttributeValue("o")
			if orgName == "" {
				continue
			}

			// Добавляем организацию
			orgIter := treeStore.(*gtk.TreeStore).Append(nil)
			treeStore.(*gtk.TreeStore).SetValue(orgIter, 0, orgName)

			// Ищем отделы в организации
			deptSearchRequest := ldap.NewSearchRequest(
				entry.DN,
				ldap.ScopeSingleLevel, ldap.NeverDerefAliases, 0, 0, false,
				"(objectClass=organizationalUnit)",
				[]string{"ou"},
				nil,
			)

			deptSr, err := l.Search(deptSearchRequest)
			if err != nil {
				continue
			}

			// Добавляем отделы
			for _, deptEntry := range deptSr.Entries {
				deptName := deptEntry.GetAttributeValue("ou")
				if deptName == "" {
					continue
				}

				deptIter := treeStore.(*gtk.TreeStore).Append(orgIter)
				treeStore.(*gtk.TreeStore).SetValue(deptIter, 0, deptName)
			}
		}
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
	if depth != 2 {
		return // Выбрана не организация и не отдел
	}

	// Получаем название отдела
	value, err := model.(*gtk.TreeModel).GetValue(iter, 0)
	if err != nil {
		return
	}

	deptName, _ := value.GetString()

	// Ищем людей в отделе
	searchPeople("(ou=" + deptName + ")")
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
		"(&(objectClass=person)"+filter+")",
		[]string{"cn", "mail", "telephoneNumber", "ou", "o", "title"},
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

		// Добавляем результаты
		for _, entry := range sr.Entries {
			iter := listStore.(*gtk.ListStore).Append()
			listStore.(*gtk.ListStore).Set(iter,
				[]int{0, 1, 2, 3, 4},
				[]interface{}{
					entry.GetAttributeValue("cn"),
					entry.GetAttributeValue("mail"),
					entry.GetAttributeValue("telephoneNumber"),
					entry.GetAttributeValue("ou"),
					entry.GetAttributeValue("o"),
				})
		}
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

	// Формируем детальную информацию
	details := fmt.Sprintf("ФИО: %s\nEmail: %s\nТелефон: %s\nОтдел: %s\nОрганизация: %s",
		fullNameStr, emailStr, phoneStr, deptStr, orgStr)

	// Обновляем детальную информацию
	detailsBuffer.SetText(details)

	// Выделяем соответствующий отдел в дереве
	selectDepartmentInTree(deptStr)
}

func selectDepartmentInTree(department string) {
	treeStore, err := treeView.GetModel()
	if err != nil {
		return
	}

	// Ищем отдел в дереве
	iter, ok := findDepartmentIter(treeStore.(*gtk.TreeModel), department)
	if !ok {
		return
	}

	// Выделяем отдел
	selection, err := treeView.GetSelection()
	if err != nil {
		return
	}

	selection.SelectIter(iter)
}

func findDepartmentIter(model *gtk.TreeModel, department string) (*gtk.TreeIter, bool) {
	var iter gtk.TreeIter
	var ok bool

	// Ищем в корневых элементах (организациях)
	for _, ok = model.GetIterFirst(); ok; ok = model.IterNext(&iter) {
		// Проверяем дочерние элементы (отделы)
		var childIter gtk.TreeIter
		if model.IterChildren(&childIter, &iter) {
			for {
				value, err := model.GetValue(&childIter, 0)
				if err != nil {
					break
				}

				deptName, _ := value.GetString()

				if deptName == department {
					return &childIter, true
				}

				if !model.IterNext(&childIter) {
					break
				}
			}
		}
	}

	return nil, false
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
	if _, err := os.Stat(socketPath); err == nil {
		// Пробуем подключиться к сокету
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		// Если подключиться не удалось, удаляем старый сокет
		os.Remove(socketPath)
	}
	return false
}

func activateExistingInstance() {
	conn, err := net.Dial("unix", socketPath)
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
	os.Remove(socketPath)

	// Создаем директорию для сокета, если ее нет
	os.MkdirAll(filepath.Dir(socketPath), 0755)

	// Создаем Unix socket
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Printf("Ошибка создания Unix socket: %v\n", err)
		return
	}
	defer l.Close()

	// Устанавливаем права на сокет
	os.Chmod(socketPath, 0600)

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
		glib.IdleAdd(func() {
			mainWindow.Present()
		})
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
