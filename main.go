package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/getlantern/systray"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed assets/icons/icon.png
var appIcon []byte

type ClipboardItem struct {
	Content   string  `json:"content"`
	Timestamp float64 `json:"timestamp"`
	ID        string  `json:"id"`
}

type ClipboardManager struct {
	historyFile    string
	menuItems      []*systray.MenuItem
	clipboardItems []ClipboardItem
	menuMutex      sync.Mutex
	maxMenuItems   int
	handlersSetup  bool
	clearItem      *systray.MenuItem
	quitItem       *systray.MenuItem
	headerItem     *systray.MenuItem
}

func NewClipboardManager(historyFile string) *ClipboardManager {
	return &ClipboardManager{
		historyFile:    historyFile,
		menuItems:      make([]*systray.MenuItem, 0),
		clipboardItems: make([]ClipboardItem, 0),
		maxMenuItems:   25,
		handlersSetup:  false,
	}
}

func (m *ClipboardManager) startClipboardMonitor() {
	var lastContent string
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		content := m.readClipboard()

		if content != lastContent && content != "" {
			lastContent = content
			m.saveClipboardItem(content)
		}
	}
}

func (m *ClipboardManager) readClipboard() string {
	cmd := exec.Command("pbpaste")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (m *ClipboardManager) saveClipboardItem(content string) {
	if content == "" {
		return
	}

	items := m.loadHistory()

	newItem := ClipboardItem{
		Content:   content,
		Timestamp: float64(time.Now().Unix()),
		ID:        fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(items)),
	}

	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Content == content {
			items = append(items[:i], items[i+1:]...)
		}
	}

	items = append([]ClipboardItem{newItem}, items...)

	if len(items) > 100 {
		items = items[:100]
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err == nil {
		_ = os.WriteFile(m.historyFile, data, 0644)
	}
}

func (m *ClipboardManager) loadHistory() []ClipboardItem {
	data, err := os.ReadFile(m.historyFile)
	if err != nil {
		return []ClipboardItem{}
	}

	var items []ClipboardItem
	_ = json.Unmarshal(data, &items)
	return items
}

func (m *ClipboardManager) clearHistory() {
	m.menuMutex.Lock()
	defer m.menuMutex.Unlock()

	items := []ClipboardItem{}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}

	err = os.WriteFile(m.historyFile, data, 0644)
	if err != nil {
		return
	}

	m.clipboardItems = items

	m.updateMenuDisplay(items)
}

func (m *ClipboardManager) setupHandlers() {
	if m.handlersSetup {
		return
	}
	m.handlersSetup = true

	for i := range m.menuItems {
		idx := i
		go func() {
			for {
				<-m.menuItems[idx].ClickedCh
				m.menuMutex.Lock()
				if idx < len(m.clipboardItems) {
					m.copyToClipboard(m.clipboardItems[idx].Content)
				}
				m.menuMutex.Unlock()
			}
		}()
	}
}

func (m *ClipboardManager) refreshMenu() {
	m.menuMutex.Lock()
	defer m.menuMutex.Unlock()

	items := m.loadHistory()

	// REFRESH IF HISTORY CHANGED
	if len(items) == len(m.clipboardItems) {
		same := true
		for i := range items {
			if i >= len(m.clipboardItems) || items[i].ID != m.clipboardItems[i].ID {
				same = false
				break
			}
		}
		if same {
			return
		}
	}

	m.clipboardItems = items
	m.updateMenuDisplay(items)
}

func (m *ClipboardManager) updateMenuDisplay(items []ClipboardItem) {
	for _, item := range m.menuItems {
		item.Hide()
	}

	displayItems := items
	if len(displayItems) > m.maxMenuItems {
		displayItems = displayItems[:m.maxMenuItems]
	}

	if len(displayItems) == 0 {
		if len(m.menuItems) > 0 {
			m.menuItems[0].SetTitle("No clipboard history")
			m.menuItems[0].Disable()
			m.menuItems[0].Show()
		}
	} else {
		for i, item := range displayItems {
			if i >= len(m.menuItems) {
				break
			}

			content := item.Content
			if len(content) > 50 {
				content = content[:47] + "..."
			}
			content = strings.ReplaceAll(content, "\n", " ")
			content = strings.ReplaceAll(content, "\t", " ")

			m.menuItems[i].SetTitle(fmt.Sprintf("%d. %s", i+1, content))
			m.menuItems[i].Enable()
			m.menuItems[i].Show()
		}
	}
}

func (m *ClipboardManager) copyToClipboard(text string) {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

func getIcon() []byte {
	return appIcon
}

func onReady(c *ClipboardManager) {
	systray.SetIcon(getIcon())
	systray.SetTitle("")
	systray.SetTooltip("ClipHis - Clipboard History Manager")

	c.headerItem = systray.AddMenuItem("Recent History", "")
	c.headerItem.Disable()

	for i := 0; i < c.maxMenuItems; i++ {
		item := systray.AddMenuItem("", "")
		item.Hide()
		c.menuItems = append(c.menuItems, item)
	}

	systray.AddSeparator()
	c.clearItem = systray.AddMenuItem("Clear History", "")
	c.quitItem = systray.AddMenuItem("Quit", "")

	c.setupHandlers()

	c.refreshMenu()

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			c.refreshMenu()
		}
	}()

	go func() {
		<-c.clearItem.ClickedCh
		c.clearHistory()
		c.refreshMenu()
	}()

	go func() {
		<-c.quitItem.ClickedCh
		systray.Quit()
	}()
}

func getHistoryFilePath() string {
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	historyFile := filepath.Join(usr.HomeDir, ".clipboard_history", "history.json")

	dataDir := filepath.Join(usr.HomeDir, ".clipboard_history")
	_ = os.MkdirAll(dataDir, 0755)
	return historyFile
}

func main() {

	// INIT
	h := getHistoryFilePath()
	c := NewClipboardManager(h)

	// START CLIPBOARD MONITOR
	go c.startClipboardMonitor()

	// RUN SYSTEM TRAY
	systray.Run(func() { onReady(c) }, nil)
}
