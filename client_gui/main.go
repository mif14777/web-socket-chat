package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/gorilla/websocket"
)

// Message mirrors the server/client protocol from client/client.go
// Keep field names and JSON tags in sync with the server.
type Message struct {
	Type      string            `json:"type"`
	Content   string            `json:"content,omitempty"`
	From      string            `json:"from,omitempty"`
	To        string            `json:"to,omitempty"`
	Timestamp string            `json:"timestamp,omitempty"`
	Users     []string          `json:"users,omitempty"`
	Flags     map[string]bool   `json:"flags,omitempty"`
	Error     string            `json:"error,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
}

type GUIClient struct {
	conn    *websocket.Conn
	nick    string
	server  string
	port    int
	running bool

	chatArea     *widget.RichText
	messageEntry *widget.Entry
	sendBtn      *widget.Button
	connectBtn   *widget.Button

	serverEntry *widget.Entry
	portEntry   *widget.Entry
	nickEntry   *widget.Entry

	win fyne.Window

	chatScroll *container.Scroll

	// User list
	users     []string
	usersList *widget.List
}

func NewGUIClient(win fyne.Window) *GUIClient {
	chat := widget.NewRichText()
	msgEntry := widget.NewEntry()
	msgEntry.SetPlaceHolder("Введите сообщение… (#команды и @приват тоже работают)")

	gc := &GUIClient{
		chatArea:     chat,
		messageEntry: msgEntry,
		win:          win,
		server:       "localhost",
		port:         12345,
		running:      false,
	}

	gc.serverEntry = widget.NewEntry()
	gc.serverEntry.SetText(gc.server)
	gc.serverEntry.SetPlaceHolder("Сервер (напр. localhost)")
	gc.portEntry = widget.NewEntry()
	gc.portEntry.SetText(strconv.Itoa(gc.port))
	gc.portEntry.SetPlaceHolder("Порт (напр. 12345)")
	gc.nickEntry = widget.NewEntry()
	gc.nickEntry.SetPlaceHolder("Никнейм")

	gc.connectBtn = widget.NewButton("Подключиться", func() {
		gc.onConnect()
	})

	gc.sendBtn = widget.NewButton("Отправить", func() { gc.onSend() })
	gc.sendBtn.Disable()

	gc.messageEntry.OnSubmitted = func(string) { gc.onSend() }

	// User list widget
	gc.usersList = widget.NewList(
		func() int { return len(gc.users) },
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			user := gc.users[id]
			if user == gc.nick {
				label.SetText("● " + user + " (вы)")
			} else {
				label.SetText("○ " + user)
			}
		},
	)

	return gc
}

func (g *GUIClient) layout() fyne.CanvasObject {
	// Make inputs wide by using a 3-column grid filling horizontal space,
	// with the Connect button anchored on the right.
	fields := container.NewGridWithColumns(3, g.serverEntry, g.portEntry, g.nickEntry)
	top := container.NewBorder(nil, nil, nil, g.connectBtn, fields)

	g.chatScroll = container.NewVScroll(g.chatArea)
	g.chatScroll.SetMinSize(fyne.NewSize(600, 400))

	bottom := container.NewBorder(nil, nil, nil, g.sendBtn, g.messageEntry)

	// User list on the right side with fixed width
	userTitle := widget.NewLabel("👥 Пользователи онлайн")
	userTitle.TextStyle = fyne.TextStyle{Bold: true}
	userListContainer := container.NewBorder(
		userTitle,
		nil, nil, nil,
		g.usersList,
	)

	userScroll := container.NewVScroll(userListContainer)
	userScroll.SetMinSize(fyne.NewSize(200, 400))

	// Chat in center, users on right
	content := container.NewBorder(nil, nil, nil, userScroll, g.chatScroll)

	return container.NewBorder(top, bottom, nil, nil, content)
}

func (g *GUIClient) onConnect() {
	if g.conn != nil {
		return
	}

	server := strings.TrimSpace(g.serverEntry.Text)
	if server == "" {
		server = "localhost"
	}
	portStr := strings.TrimSpace(g.portEntry.Text)
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p >= 65536 {
		dialog.ShowError(fmt.Errorf("некорректный порт: %s", portStr), g.win)
		return
	}
	nick := strings.TrimSpace(g.nickEntry.Text)
	if nick == "" {
		dialog.ShowError(fmt.Errorf("введите никнейм"), g.win)
		return
	}

	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", server, p), Path: "/ws"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		dialog.ShowError(fmt.Errorf("не удалось подключиться: %w", err), g.win)
		return
	}

	g.conn = c
	g.server, g.port, g.nick = server, p, nick
	g.running = true
	g.appendLine(fmt.Sprintf("✅ Подключено к %s", u.String()))

	// Login handshake (nick)
	if err := g.login(); err != nil {
		g.appendLine("❌ Ошибка входа: " + err.Error())
		_ = g.conn.Close()
		g.conn = nil
		g.running = false
		return
	}

	g.connectBtn.Disable()
	g.serverEntry.Disable()
	g.portEntry.Disable()
	g.nickEntry.Disable()
	g.sendBtn.Enable()
	g.messageEntry.Enable()
	g.win.Canvas().Focus(g.messageEntry)

	// Server will send user list automatically, no need to request
	// usersCmd := Message{Type: "command", Data: map[string]string{"command": "users"}}
	// _ = g.writeJSON(usersCmd)

	go g.readLoop()
}

func (g *GUIClient) login() error {
	// read initial message
	_, raw, err := g.conn.ReadMessage()
	if err != nil {
		return err
	}
	var initial Message
	if err := json.Unmarshal(raw, &initial); err != nil {
		return err
	}

	sendNick := g.nick
	if initial.Type == "nick_prompt" {
		// server suggests previous nick via Content; prefer user-entered one if provided
		if sendNick == "" {
			sendNick = initial.Content
		}
	} else if initial.Type != "nick_request" {
		return fmt.Errorf("неожиданный ответ от сервера: %s", initial.Type)
	}

	nickMsg := Message{Type: "nick", Content: sendNick}
	if err := g.writeJSON(nickMsg); err != nil {
		return err
	}
	// wait for confirmation
	_, raw2, err := g.conn.ReadMessage()
	if err != nil {
		return err
	}
	var resp Message
	if err := json.Unmarshal(raw2, &resp); err != nil {
		return err
	}
	if resp.Type == "error" {
		return fmt.Errorf("%s", resp.Error)
	}
	if resp.Type != "nick_ok" {
		return fmt.Errorf("ожидался nick_ok, получено: %s", resp.Type)
	}
	g.appendLine("✅ Никнейм принят сервером")
	return nil
}

func (g *GUIClient) readLoop() {
	for g.running {
		_, raw, err := g.conn.ReadMessage()
		if err != nil {
			if g.running {
				g.appendLine("❌ Ошибка чтения: " + err.Error())
			}
			g.running = false
			break
		}
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			g.appendLine("❌ Ошибка JSON: " + err.Error())
			continue
		}
		g.handleIncoming(msg)
	}
}

func (g *GUIClient) handleIncoming(msg Message) {
	switch msg.Type {
	case "chat":
		g.appendLine(fmt.Sprintf("%s: %s", msg.From, msg.Content))
	case "private":
		// Highlight private messages with cyan color
		g.appendPrivateLine(fmt.Sprintf("💬 [ЛС] %s: %s", msg.From, msg.Content))
	case "private_sent":
		// Confirmation that private message was sent - silent (already echoed locally)
	case "mass_private":
		g.appendPrivateLine(fmt.Sprintf("📢 [ALL-LS] %s: %s", msg.From, msg.Content))
	case "mass_private_sent":
		// Confirmation that mass private was sent - silent (already echoed locally)
	case "system":
		// Replace emoji with simple symbols for macOS compatibility
		content := msg.Content
		content = strings.ReplaceAll(content, "🟢", "+")
		content = strings.ReplaceAll(content, "🔴", "-")
		content = strings.ReplaceAll(content, "✅", "[OK]")
		content = strings.ReplaceAll(content, "❌", "[X]")
		g.appendSystemLine(content)
		// Auto-refresh user list when someone joins or leaves
		if strings.Contains(msg.Content, "присоединился") ||
			strings.Contains(msg.Content, "покинул") ||
			strings.Contains(msg.Content, "вернулся") {
			usersCmd := Message{Type: "command", Data: map[string]string{"command": "users"}}
			_ = g.writeJSON(usersCmd)
		}
	case "users":
		// Update user list and refresh widget
		g.users = msg.Users
		g.usersList.Refresh()
		// Don't show in chat to avoid clutter - list is visible in sidebar
		// g.appendLine(fmt.Sprintf("👥 Онлайн (%d): %s", len(msg.Users), strings.Join(msg.Users, ", ")))
	case "help":
		g.appendLine("📖 Справка по командам:")
		for k, v := range msg.Data {
			g.appendLine(fmt.Sprintf("  %-16s %s", k, v))
		}
	case "mailbox_status", "offline_message", "offline_delivered", "offline_saved", "fav_list", "fav_added", "fav_removed", "fav_cleared", "blocked", "unblocked", "wordlengths_toggle", "last_result":
		// Show the content in a simple way
		if msg.Content != "" {
			g.appendLine(msg.Content)
		} else if msg.Error != "" {
			g.appendLine("❌ " + msg.Error)
		}
	case "error":
		g.appendLine("❌ " + msg.Error)
	default:
		g.appendLine(fmt.Sprintf("[?] %s", msg.Type))
	}
}

func (g *GUIClient) writeJSON(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return g.conn.WriteMessage(websocket.TextMessage, b)
}

func (g *GUIClient) onSend() {
	if g.conn == nil {
		return
	}
	text := strings.TrimSpace(g.messageEntry.Text)
	if text == "" {
		return
	}

	if strings.HasPrefix(text, "#") {
		// command
		m := Message{Type: "command", Data: map[string]string{"command": ""}}
		parts := strings.SplitN(text, " ", 2)
		cmd := strings.ToLower(strings.TrimPrefix(parts[0], "#"))
		m.Data["command"] = cmd
		switch cmd {
		case "help", "users", "mailbox", "wordlengths":
			// no extra fields
		case "last":
			if len(parts) < 2 {
				g.appendLine("❌ Использование: #last <ник>")
				return
			}
			m.Data["target"] = parts[1]
		case "all":
			if len(parts) < 2 {
				g.appendLine("❌ Использование: #all сообщение")
				return
			}
			m.Data["content"] = parts[1]
		case "block", "unblock":
			if len(parts) < 2 {
				g.appendLine(fmt.Sprintf("❌ Использование: #%s ник", cmd))
				return
			}
			m.Data["target"] = parts[1]
		case "fav":
			if len(parts) < 2 {
				m.Data["action"] = "list"
			} else {
				sub := strings.SplitN(parts[1], " ", 2)
				if len(sub) == 1 {
					low := strings.ToLower(sub[0])
					if low == "list" {
						m.Data["action"] = "list"
					} else if low == "clear" {
						m.Data["action"] = "clear"
					} else {
						m.Data["action"] = "add"
						m.Data["target"] = sub[0]
					}
				} else {
					m.Data["action"] = sub[0]
					m.Data["target"] = sub[1]
				}
			}
		default:
			g.appendLine("❌ Неизвестная команда: " + cmd)
			return
		}
		if err := g.writeJSON(m); err != nil {
			g.appendLine("❌ Ошибка отправки: " + err.Error())
		}
	} else if strings.HasPrefix(text, "@") {
		// private
		parts := strings.SplitN(text, " ", 2)
		if len(parts) < 2 {
			g.appendLine("❌ Использование: @никнейм сообщение")
			return
		}
		target := strings.TrimPrefix(parts[0], "@")
		m := Message{Type: "private", Content: parts[1], To: target}
		if err := g.writeJSON(m); err != nil {
			g.appendLine("❌ Ошибка отправки: " + err.Error())
		} else {
			g.appendPrivateLine(fmt.Sprintf("💬 [ЛС] Вы → %s: %s", target, parts[1]))
		}
	} else {
		// normal chat message
		m := Message{Type: "message", Content: text}
		if err := g.writeJSON(m); err != nil {
			g.appendLine("❌ Ошибка отправки: " + err.Error())
		} else {
			// Echo own message locally
			g.appendLine(fmt.Sprintf("Вы: %s", text))
		}
	}

	g.messageEntry.SetText("")
}

func (g *GUIClient) appendLine(line string) {
	g.appendLineWithStyle(line, widget.RichTextStyle{})
}

func (g *GUIClient) appendLineWithStyle(line string, style widget.RichTextStyle) {
	// Add new text segment to RichText
	seg := &widget.TextSegment{
		Text:  line,
		Style: style,
	}
	
	// Append to existing segments
	if len(g.chatArea.Segments) > 0 {
		// Add newline before new segment
		g.chatArea.Segments = append(g.chatArea.Segments, &widget.TextSegment{Text: "\n"})
	}
	g.chatArea.Segments = append(g.chatArea.Segments, seg)
	g.chatArea.Refresh()
	
	if g.chatScroll != nil {
		g.chatScroll.ScrollToBottom()
	}
}

func (g *GUIClient) appendPrivateLine(line string) {
	// Cyan/blue style for private messages
	style := widget.RichTextStyle{
		ColorName: "primary", // Use theme primary color (usually blue/cyan)
		TextStyle: fyne.TextStyle{},
	}
	g.appendLineWithStyle(line, style)
}

func (g *GUIClient) appendSystemLine(line string) {
	// Gray/muted style for system messages
	style := widget.RichTextStyle{
		ColorName: "foreground",
		TextStyle: fyne.TextStyle{Italic: true},
	}
	g.appendLineWithStyle(line, style)
}

func (g *GUIClient) cleanup() {
	g.running = false
	if g.conn != nil {
		_ = g.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		time.Sleep(300 * time.Millisecond)
		_ = g.conn.Close()
		g.conn = nil
	}
}

func main() {
	// Set dark theme via env to avoid deprecated API warnings
	_ = os.Setenv("FYNE_THEME", "dark")
	application := app.New()
	w := application.NewWindow("WebSocket Chat (GUI)")
	w.Resize(fyne.NewSize(800, 500))

	gc := NewGUIClient(w)
	w.SetContent(gc.layout())

	w.SetCloseIntercept(func() {
		gc.cleanup()
		w.Close()
	})

	w.ShowAndRun()
}
