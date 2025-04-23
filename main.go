package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net"
    "os"
    "path/filepath"

    "github.com/go-ldap/ldap/v3"
    "github.com/gotk3/gotk3/glib"
    "github.com/gotk3/gotk3/gtk"
    "github.com/linuxdeepin/go-xapp"
)

type Config struct {
    LDAPServer   string `json:"ldap_server"`
    BindDN       string `json:"bind_dn"`
    BindPassword string `json:"bind_password"`
    BaseDN       string `json:"base_dn"`
    WindowWidth  int    `json:"window_width"`
    WindowHeight int    `json:"window_height"`
    IconName     string `json:"icon_name"`
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
    statusIcon  *xapp.StatusIcon
    ldapConn    *ldap.Conn
    searchEntry *gtk.Entry
    listStore   *gtk.ListStore
    config      Config
    listener    net.Listener
}

func main() {
    gtk.Init(&os.Args)

    app := &App{}
    app.loadConfig()
    app.createMainWindow()
    app.createStatusIcon()
    app.startSocketServer()

    gtk.Main()

    if app.listener != nil {
	app.listener.Close()
	os.Remove(socketFile)
    }
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
		log.Printf("Error reading config %s: %v\n", path, err)
		continue
	    }

	    if err := json.Unmarshal(data, &app.config); err != nil {
		log.Printf("Error parsing config %s: %v\n", path, err)
		continue
	    }

	    log.Printf("Config loaded from %s\n", path)
	    return
	}
    }

    // Default configuration
    app.config = Config{
	LDAPServer:   "ldap://localhost:389",
	BindDN:       "cn=admin,dc=example,dc=com",
	BindPassword: "",
	BaseDN:       "dc=example,dc=com",
	WindowWidth:  800,
	WindowHeight: 600,
	IconName:     defaultIcon,
    }
}

func (app *App) createMainWindow() {
    var err error

    app.window, err = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
    if err != nil {
	log.Fatal("Failed to create window:", err)
    }

    app.window.SetTitle(fmt.Sprintf("LDAP Phonebook v%s", appVersion))
    app.window.SetDefaultSize(app.config.WindowWidth, app.config.WindowHeight)
    app.window.SetPosition(gtk.WIN_POS_CENTER)
    app.window.SetIconName(app.config.IconName)

    // Setup UI
    box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
    if err != nil {
	log.Fatal("Failed to create box:", err)
    }
    app.window.Add(box)

    // Connection panel
    app.createConnectionPanel(box)
    // Search panel
    app.createSearchPanel(box)
    // Results view
    app.createResultsView(box)

    app.window.ShowAll()
}

func (app *App) createConnectionPanel(box *gtk.Box) {
    frame, err := gtk.FrameNew("LDAP Connection")
    if err != nil {
	log.Fatal("Failed to create frame:", err)
    }
    box.PackStart(frame, false, false, 0)

    grid, err := gtk.GridNew()
    if err != nil {
	log.Fatal("Failed to create grid:", err)
    }
    grid.SetColumnSpacing(6)
    grid.SetRowSpacing(6)
    grid.SetMarginTop(6)
    grid.SetMarginBottom(6)
    grid.SetMarginStart(6)
    grid.SetMarginEnd(6)
    frame.Add(grid)

    // Server entry
    serverEntry, _ := gtk.EntryNew()
    serverEntry.SetText(app.config.LDAPServer)
    grid.Attach(createLabel("Server:"), 0, 0, 1, 1)
    grid.Attach(serverEntry, 1, 0, 1, 1)

    // Bind DN entry
    bindEntry, _ := gtk.EntryNew()
    bindEntry.SetText(app.config.BindDN)
    grid.Attach(createLabel("Bind DN:"), 0, 1, 1, 1)
    grid.Attach(bindEntry, 1, 1, 1, 1)

    // Password entry
    passwordEntry, _ := gtk.EntryNew()
    passwordEntry.SetVisibility(false)
    grid.Attach(createLabel("Password:"), 0, 2, 1, 1)
    grid.Attach(passwordEntry, 1, 2, 1, 1)

    // Connect button
    connectBtn, _ := gtk.ButtonNewWithLabel("Connect")
    connectBtn.Connect("clicked", func() {
	server, _ := serverEntry.GetText()
	bindDN, _ := bindEntry.GetText()
	password, _ := passwordEntry.GetText()

	conn, err := ldap.DialURL(server)
	if err != nil {
	    app.showError("Connection Error", err.Error())
	    return
	}

	err = conn.Bind(bindDN, password)
	if err != nil {
	    app.showError("Bind Error", err.Error())
	    return
	}

	app.ldapConn = conn
	app.showMessage("Successfully connected to LDAP server")
    })
    grid.Attach(connectBtn, 0, 3, 2, 1)
}

func (app *App) createSearchPanel(box *gtk.Box) {
    frame, _ := gtk.FrameNew("Search")
    box.PackStart(frame, false, false, 0)

    hbox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
    hbox.SetMarginTop(6)
    hbox.SetMarginBottom(6)
    hbox.SetMarginStart(6)
    hbox.SetMarginEnd(6)
    frame.Add(hbox)

    app.searchEntry, _ = gtk.EntryNew()
    app.searchEntry.SetPlaceholderText("Enter name or phone number")
    hbox.PackStart(app.searchEntry, true, true, 0)

    searchBtn, _ := gtk.ButtonNewWithLabel("Search")
    searchBtn.Connect("clicked", app.onSearchClicked)
    hbox.PackStart(searchBtn, false, false, 0)
}

func (app *App) createResultsView(box *gtk.Box) {
    scrolled, _ := gtk.ScrolledWindowNew(nil, nil)
    box.PackStart(scrolled, true, true, 0)

    app.listStore, _ = gtk.ListStoreNew(
	glib.TYPE_STRING, // Name
	glib.TYPE_STRING, // Title
	glib.TYPE_STRING, // Department
	glib.TYPE_STRING, // Phone
    )

    treeView, _ := gtk.TreeViewNewWithModel(app.listStore)
    treeView.SetHeadersVisible(true)
    scrolled.Add(treeView)

    // Add columns
    addColumn(treeView, "Name", 0)
    addColumn(treeView, "Title", 1)
    addColumn(treeView, "Department", 2)
    addColumn(treeView, "Phone", 3)
}

func (app *App) onSearchClicked() {
    if app.ldapConn == nil {
	app.showError("Error", "Not connected to LDAP server")
	return
    }

    query, _ := app.searchEntry.GetText()
    if query == "" {
	app.showError("Error", "Please enter search query")
	return
    }

    searchRequest := ldap.NewSearchRequest(
	app.config.BaseDN,
	ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
	fmt.Sprintf("(|(cn=*%s*)(telephoneNumber=*%s*))", query, query),
	[]string{"cn", "title", "department", "telephoneNumber"},
	nil,
    )

    result, err := app.ldapConn.Search(searchRequest)
    if err != nil {
	app.showError("Search Error", err.Error())
	return
    }

    // Clear previous results
    app.listStore.Clear()

    // Add new results
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

    app.showMessage(fmt.Sprintf("Found %d entries", len(result.Entries)))
}

func (app *App) createStatusIcon() {
    app.statusIcon = xapp.NewStatusIcon()
    app.statusIcon.SetName(appName)
    app.statusIcon.SetIconName(app.config.IconName)
    app.statusIcon.SetTooltipText("LDAP Phonebook")

    // Handle click on icon
    app.statusIcon.Connect("activate", func() {
	if app.window.GetVisible() {
	    app.window.Hide()
	} else {
	    app.window.Present()
	}
    })

    // Create context menu
    menu, _ := gtk.MenuNew()

    showItem, _ := gtk.MenuItemNewWithLabel("Show")
    showItem.Connect("activate", func() {
	app.window.Present()
    })
    menu.Append(showItem)

    separator, _ := gtk.SeparatorMenuItemNew()
    menu.Append(separator)

    exitItem, _ := gtk.MenuItemNewWithLabel("Exit")
    exitItem.Connect("activate", gtk.MainQuit)
    menu.Append(exitItem)

    menu.ShowAll()
    app.statusIcon.SetMenu(menu)
}

func (app *App) startSocketServer() {
    os.Remove(socketFile)

    listener, err := net.Listen("unix", socketFile)
    if err != nil {
	log.Println("Failed to create socket:", err)
	return
    }
    app.listener = listener

    go func() {
	for {
	    conn, err := listener.Accept()
	    if err != nil {
		if !strings.Contains(err.Error(), "use of closed network connection") {
		    log.Println("Accept error:", err)
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
		    app.window.Present()
		    return false
		})
	    }

	    conn.Close()
	}
    }()
}

func createLabel(text string) *gtk.Label {
    label, err := gtk.LabelNew(text)
    if err != nil {
	log.Fatal("Failed to create label:", err)
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
    dialog.SetTitle("Message")
    dialog.Run()
    dialog.Destroy()
}