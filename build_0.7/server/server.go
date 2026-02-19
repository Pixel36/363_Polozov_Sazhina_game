package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	mapW        = 100
	mapH        = 100
	tileSize    = 32
	maxSpeed    = 8.0
	maxPlayers  = 10
	turnTimeout = 20 * time.Second
)

type Color struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
	A uint8 `json:"a"`
}

type Player struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Race      string    `json:"race"`
	Weapon    string    `json:"weapon"`
	X         float64   `json:"x"`
	Y         float64   `json:"y"`
	TargetX   float64   `json:"tx"`
	TargetY   float64   `json:"ty"`
	HP        int       `json:"hp"`
	Color     Color     `json:"color"`
	LastMove  int64     `json:"-"`
	LastInput string    `json:"-"`
	Dead      bool      `json:"-"`
	DeathTime time.Time `json:"-"`
}

type ChatMessage struct {
	From  string `json:"from"`
	Text  string `json:"text"`
	Time  int64  `json:"time"`
	Color Color  `json:"color"`
}

type Connection struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	closed bool
}

type ServerStats struct {
	Players      int
	Connections  int
	LastUpdate   time.Time
	MessagesSent int64
	StartTime    time.Time
	ChatMessages int64
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	players     = make(map[string]*Player)
	playerNames = make(map[string]string)
	conns       = make(map[string]*Connection)
	gameMap     [][]int
	mu          sync.RWMutex
	stats       ServerStats
	playerCount int
	lastState   []byte
	lastStateMu sync.RWMutex

	usedColors  = make(map[uint32]bool)
	chatHistory []ChatMessage
	chatMu      sync.RWMutex

	playersOrder  []string
	currentTurn   int
	turnStartTime time.Time
	turnMu        sync.RWMutex
)

func main() {
	rand.Seed(time.Now().UnixNano())
	stats.StartTime = time.Now()

	fmt.Println("=== Сервер ===")
	fmt.Println("Генерация карты...")
	genMap()

	http.HandleFunc("/ws", wsHandler)
	http.HandleFunc("/stats", statsHandler)
	http.HandleFunc("/colors", colorsHandler)

	go broadcastLoop()
	go statsLoop()
	go cleanupLoop()
	go turnTimeoutLoop()

	fmt.Println("Сервер запущен на :8080")
	fmt.Println("WebSocket: ws://localhost:8080/ws")
	fmt.Println("Статистика: http://localhost:8080/stats")
	fmt.Println("Занятые цвета: http://localhost:8080/colors")

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func turnTimeoutLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		turnMu.Lock()
		if len(playersOrder) > 0 {
			currentPlayerID := playersOrder[currentTurn]
			mu.RLock()
			currentPlayer := players[currentPlayerID]
			mu.RUnlock()
			if currentPlayer != nil && currentPlayer.Dead {
				nextTurn()
				continue
			}
			elapsed := time.Since(turnStartTime)
			if elapsed > turnTimeout {
				log.Printf("⏰ Таймаут хода игрока %s", currentPlayerID)
				nextTurn()
			}
		}
		turnMu.Unlock()
	}
}

func nextTurn() {
	if len(playersOrder) == 0 {
		return
	}
	currentTurn = (currentTurn + 1) % len(playersOrder)
	turnStartTime = time.Now()
	log.Printf("➡️ Ход перешел к игроку %s", playersOrder[currentTurn])
}

func generateRock(gameMap [][]int, cx, cy, targetSize int) {
	dirs := [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	cells := [][2]int{{cx, cy}}
	gameMap[cy][cx] = 2

	maxAttempts := targetSize * 10
	attempts := 0

	for len(cells) < targetSize && attempts < maxAttempts {
		parent := cells[rand.Intn(len(cells))]
		var neighbors [][2]int
		for _, d := range dirs {
			nx, ny := parent[0]+d[0], parent[1]+d[1]
			if nx >= 0 && nx < mapW && ny >= 0 && ny < mapH && gameMap[ny][nx] == 0 {
				neighbors = append(neighbors, [2]int{nx, ny})
			}
		}
		if len(neighbors) > 0 {
			newCell := neighbors[rand.Intn(len(neighbors))]
			cells = append(cells, newCell)
			gameMap[newCell[1]][newCell[0]] = 2
			attempts = 0
		} else {
			attempts++
		}
	}
}

func colorsHandler(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	colors := make([]Color, 0, len(usedColors))
	for colKey := range usedColors {
		c := Color{
			R: uint8(colKey >> 24),
			G: uint8(colKey >> 16),
			B: uint8(colKey >> 8),
			A: uint8(colKey),
		}
		colors = append(colors, c)
	}
	mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(colors)
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Ошибка обновления до WebSocket:", err)
		return
	}

	var hello map[string]interface{}
	if err := c.ReadJSON(&hello); err != nil {
		log.Println("Ошибка чтения приветствия:", err)
		c.Close()
		return
	}

	nameRaw, ok := hello["name"]
	if !ok {
		c.WriteJSON(map[string]string{"error": "Требуется имя"})
		c.Close()
		return
	}
	name, ok := nameRaw.(string)
	if !ok {
		c.WriteJSON(map[string]string{"error": "Имя должно быть строкой"})
		c.Close()
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		c.WriteJSON(map[string]string{"error": "Имя не может быть пустым"})
		c.Close()
		return
	}
	if len(name) > 20 {
		name = name[:20]
	}

	race := "human"
	if raceRaw, ok := hello["race"]; ok {
		if r, ok := raceRaw.(string); ok && (r == "human" || r == "cat") {
			race = r
		}
	}

	weapon := "sword"
	if weaponRaw, ok := hello["weapon"]; ok {
		if w, ok := weaponRaw.(string); ok && (w == "sword" || w == "spear") {
			weapon = w
		}
	}

	var selectedColor *Color
	if colorRaw, ok := hello["color"]; ok {
		if colorMap, ok := colorRaw.(map[string]interface{}); ok {
			var c Color
			if r, ok := colorMap["r"].(float64); ok {
				c.R = uint8(r)
			}
			if g, ok := colorMap["g"].(float64); ok {
				c.G = uint8(g)
			}
			if b, ok := colorMap["b"].(float64); ok {
				c.B = uint8(b)
			}
			if a, ok := colorMap["a"].(float64); ok {
				c.A = uint8(a)
			}
			selectedColor = &c
		}
	}

	mu.Lock()
	if existingID, exists := playerNames[name]; exists {
		if p, ok := players[existingID]; ok && p.Dead {
			delete(players, existingID)
			delete(playerNames, name)
			if conn, ok := conns[existingID]; ok {
				conn.conn.Close()
				delete(conns, existingID)
			}
		} else {
			mu.Unlock()
			c.WriteJSON(map[string]string{
				"error": fmt.Sprintf("Имя '%s' уже занято", name),
			})
			c.Close()
			return
		}
	}
	if len(players) >= maxPlayers {
		mu.Unlock()
		c.WriteJSON(map[string]string{
			"error": fmt.Sprintf("Сервер переполнен (максимум %d игроков)", maxPlayers),
		})
		c.Close()
		return
	}

	var finalColor Color
	if selectedColor != nil {
		colorKey := uint32(selectedColor.R)<<24 | uint32(selectedColor.G)<<16 | uint32(selectedColor.B)<<8 | uint32(selectedColor.A)
		if usedColors[colorKey] {
			mu.Unlock()
			c.WriteJSON(map[string]string{
				"error": "Выбранный цвет уже занят",
			})
			c.Close()
			return
		}
		finalColor = *selectedColor
		usedColors[colorKey] = true
	} else {
		finalColor = generateUniqueColor()
	}
	mu.Unlock()

	x, y := findSafeSpawn()
	id := randID()
	now := time.Now().UnixMilli()

	p := &Player{
		ID:       id,
		Name:     name,
		Race:     race,
		Weapon:   weapon,
		X:        x,
		Y:        y,
		TargetX:  x,
		TargetY:  y,
		HP:       10,
		LastMove: now,
		Color:    finalColor,
		Dead:     false,
	}

	mu.Lock()
	players[id] = p
	playerNames[name] = id
	conns[id] = &Connection{
		conn:   c,
		mu:     sync.Mutex{},
		closed: false,
	}
	playerCount++
	stats.Connections++
	mu.Unlock()

	turnMu.Lock()
	playersOrder = append(playersOrder, id)
	if len(playersOrder) == 1 {
		currentTurn = 0
		turnStartTime = time.Now()
	}
	turnMu.Unlock()

	log.Printf("📥 Игрок подключился: %s (%s) оружие: %s ID: %s на позиции %.0f,%.0f", name, race, weapon, id, x, y)

	chatMu.RLock()
	if len(chatHistory) > 0 {
		lastMessages := chatHistory
		if len(chatHistory) > 50 {
			lastMessages = chatHistory[len(chatHistory)-50:]
		}
		for _, msg := range lastMessages {
			sendToClient(id, map[string]any{
				"type":  "chat",
				"from":  msg.From,
				"text":  msg.Text,
				"time":  msg.Time,
				"color": msg.Color,
			})
		}
	}
	chatMu.RUnlock()

	sendToClient(id, map[string]any{
		"type":  "init",
		"id":    id,
		"x":     float64(x),
		"y":     float64(y),
		"color": finalColor,
		"race":  race,
	})

	sendToClient(id, map[string]any{
		"type": "map",
		"data": gameMap,
	})

	chatMsg := ChatMessage{
		From:  "Система",
		Text:  fmt.Sprintf("%s присоединился к игре", name),
		Time:  now,
		Color: Color{R: 173, G: 216, B: 230, A: 255},
	}
	broadcastChat(chatMsg)

	broadcastToAll()

	for {
		var msg map[string]any
		if err := c.ReadJSON(&msg); err != nil {
			log.Printf("📤 Игрок отключился: %s (ID: %s) - %v", name, id, err)
			break
		}

		if action, ok := msg["action"].(string); ok {
			switch action {
			case "move":
				handleMove(id, msg)
			case "position":
				handlePosition(id, msg)
			case "chat":
				handleChat(id, msg)
			case "turn_action":
				handleTurnAction(id, msg)
			}
		}
	}

	mu.Lock()
	delete(players, id)
	delete(playerNames, name)
	playerCount--

	colorKey := uint32(p.Color.R)<<24 | uint32(p.Color.G)<<16 | uint32(p.Color.B)<<8 | uint32(p.Color.A)
	delete(usedColors, colorKey)

	if conn, ok := conns[id]; ok {
		conn.mu.Lock()
		conn.closed = true
		conn.conn.Close()
		conn.mu.Unlock()
		delete(conns, id)
	}

	stats.Connections--
	mu.Unlock()

	turnMu.Lock()
	for i, pid := range playersOrder {
		if pid == id {
			playersOrder = append(playersOrder[:i], playersOrder[i+1:]...)
			if len(playersOrder) == 0 {
			} else {
				if i < currentTurn {
					currentTurn--
				} else if i == currentTurn {
					if currentTurn >= len(playersOrder) {
						currentTurn = 0
					}
					turnStartTime = time.Now()
				}
			}
			break
		}
	}
	turnMu.Unlock()

	chatMsg = ChatMessage{
		From:  "Система",
		Text:  fmt.Sprintf("%s покинул игру", name),
		Time:  time.Now().UnixMilli(),
		Color: Color{R: 173, G: 216, B: 230, A: 255},
	}
	broadcastChat(chatMsg)

	broadcastToAll()

	log.Printf("❌ Игрок отключился: %s (ID: %s)", name, id)
}

func generateUniqueColor() Color {
	for attempt := 0; attempt < 100; attempt++ {
		r := uint8(rand.Intn(200) + 30)
		g := uint8(rand.Intn(200) + 30)
		b := uint8(rand.Intn(200) + 30)
		a := uint8(255)

		colorKey := uint32(r)<<24 | uint32(g)<<16 | uint32(b)<<8 | uint32(a)

		mu.Lock()
		if !usedColors[colorKey] {
			usedColors[colorKey] = true
			mu.Unlock()
			return Color{R: r, G: g, B: b, A: a}
		}
		mu.Unlock()
	}
	return Color{
		R: uint8(rand.Intn(200) + 30),
		G: uint8(rand.Intn(200) + 30),
		B: uint8(rand.Intn(200) + 30),
		A: 255,
	}
}

func handlePosition(id string, msg map[string]any) {
	x, ok1 := msg["x"].(float64)
	y, ok2 := msg["y"].(float64)

	if !ok1 || !ok2 {
		return
	}

	mu.Lock()
	p, exists := players[id]
	if !exists {
		mu.Unlock()
		return
	}

	if isPositionValid(x, y) {
		p.X = x
		p.Y = y
		p.TargetX = x
		p.TargetY = y
	}

	mu.Unlock()
}

func sendToClient(playerID string, msg map[string]any) {
	mu.RLock()
	conn, ok := conns[playerID]
	mu.RUnlock()

	if !ok {
		return
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if conn.closed {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Ошибка маршалинга для игрока %s: %v", playerID, err)
		return
	}

	conn.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("Ошибка отправки игроку %s: %v", playerID, err)
		conn.closed = true
		conn.conn.Close()
	}
}

func handleMove(id string, msg map[string]any) {
	dx, ok1 := msg["dx"].(float64)
	dy, ok2 := msg["dy"].(float64)
	clientX, ok3 := msg["x"].(float64)
	clientY, ok4 := msg["y"].(float64)

	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}

	if math.IsNaN(dx) || math.IsInf(dx, 0) || math.IsNaN(dy) || math.IsInf(dy, 0) {
		return
	}

	dx = math.Max(-maxSpeed, math.Min(maxSpeed, dx))
	dy = math.Max(-maxSpeed, math.Min(maxSpeed, dy))

	mu.Lock()
	p, exists := players[id]
	if !exists {
		mu.Unlock()
		return
	}

	now := time.Now().UnixMilli()

	nx := clientX
	ny := clientY

	dxDiff := math.Abs(nx - p.TargetX)
	dyDiff := math.Abs(ny - p.TargetY)

	if dxDiff > 50 || dyDiff > 50 {
		nx = p.X
		ny = p.Y
	} else if isPositionValid(nx, ny) {
		playerSize := float64(tileSize) * 0.7
		canMove := true

		for _, other := range players {
			if other.ID != id {
				distX := nx - other.X
				distY := ny - other.Y
				dist := math.Sqrt(distX*distX + distY*distY)

				if dist < playerSize {
					canMove = false
					break
				}
			}
		}

		if canMove {
			p.X = nx
			p.Y = ny
			p.TargetX = nx
			p.TargetY = ny
			p.LastMove = now
		}
	}

	mu.Unlock()

	sendToClient(id, map[string]any{
		"type": "move_ack",
		"x":    p.X,
		"y":    p.Y,
		"ts":   now,
	})

	stats.MessagesSent++
}

func handleTurnAction(playerID string, msg map[string]any) {
	turnMu.RLock()
	if len(playersOrder) == 0 || playersOrder[currentTurn] != playerID {
		turnMu.RUnlock()
		return
	}
	turnMu.RUnlock()

	actionType, ok := msg["type"].(string)
	if !ok {
		return
	}

	mu.RLock()
	p, exists := players[playerID]
	mu.RUnlock()
	if !exists || p.Dead {
		return
	}

	turnMu.Lock()
	if time.Since(turnStartTime) > turnTimeout {
		nextTurn()
		turnMu.Unlock()
		return
	}
	turnMu.Unlock()

	switch actionType {
	case "move":
		handleTurnMove(p, msg)
	case "attack":
		handleTurnAttack(p, msg)
	case "skip":
		handleTurnSkip(p)
	default:
		return
	}

	turnMu.Lock()
	nextTurn()
	turnMu.Unlock()

	broadcastToAll()
}

func handleTurnMove(p *Player, msg map[string]any) {
	targetX, ok1 := msg["targetX"].(float64)
	targetY, ok2 := msg["targetY"].(float64)
	if !ok1 || !ok2 {
		return
	}

	currentTileX := int(p.X / tileSize)
	currentTileY := int(p.Y / tileSize)
	targetTileX := int(targetX / tileSize)
	targetTileY := int(targetY / tileSize)

	dx := targetTileX - currentTileX
	dy := targetTileY - currentTileY

	if math.Abs(float64(dx)) > 1 || math.Abs(float64(dy)) > 1 || (dx == 0 && dy == 0) {
		return
	}

	if !isPositionValid(targetX, targetY) {
		return
	}

	mu.RLock()
	for _, other := range players {
		if other.ID != p.ID && !other.Dead {
			otherTileX := int(other.X / tileSize)
			otherTileY := int(other.Y / tileSize)
			if otherTileX == targetTileX && otherTileY == targetTileY {
				mu.RUnlock()
				return
			}
		}
	}
	mu.RUnlock()

	mu.Lock()
	p.X = targetX
	p.Y = targetY
	p.TargetX = targetX
	p.TargetY = targetY
	mu.Unlock()
}

func handleTurnAttack(p *Player, msg map[string]any) {
	targetID, ok := msg["targetID"].(string)
	if !ok {
		return
	}

	mu.RLock()
	target, exists := players[targetID]
	mu.RUnlock()
	if !exists || target.ID == p.ID || target.Dead {
		return
	}

	currentTileX := int(p.X / tileSize)
	currentTileY := int(p.Y / tileSize)
	targetTileX := int(target.X / tileSize)
	targetTileY := int(target.Y / tileSize)

	dx := math.Abs(float64(targetTileX - currentTileX))
	dy := math.Abs(float64(targetTileY - currentTileY))

	damage := 3
	maxRange := 1
	switch p.Weapon {
	case "spear":
		damage = 2
		maxRange = 2
	case "sword":
		damage = 4
	}

	if dx+dy > float64(maxRange) || (dx == 0 && dy == 0) {
		return
	}

	mu.Lock()
	target.HP -= damage
	if target.HP <= 0 && !target.Dead {
		target.Dead = true
		target.DeathTime = time.Now()

		turnMu.Lock()
		for i, pid := range playersOrder {
			if pid == target.ID {
				playersOrder = append(playersOrder[:i], playersOrder[i+1:]...)
				if i < currentTurn {
					currentTurn--
				} else if i == currentTurn {
					if currentTurn >= len(playersOrder) {
						currentTurn = 0
					}
					turnStartTime = time.Now()
				}
				break
			}
		}
		turnMu.Unlock()

		delete(playerNames, target.Name)

		chatMsg := ChatMessage{
			From:  "Система",
			Text:  fmt.Sprintf("%s был убит", target.Name),
			Time:  time.Now().UnixMilli(),
			Color: Color{R: 255, G: 100, B: 100, A: 255},
		}
		mu.Unlock()
		broadcastChat(chatMsg)
		return
	}
	mu.Unlock()
}

func handleTurnSkip(p *Player) {
}

func handleChat(id string, msg map[string]any) {
	mu.RLock()
	p, exists := players[id]
	mu.RUnlock()

	if !exists {
		return
	}

	text, ok := msg["text"].(string)
	if !ok || text == "" {
		return
	}

	if len(text) > 200 {
		text = text[:200]
	}

	chatMsg := ChatMessage{
		From:  p.Name,
		Text:  text,
		Time:  time.Now().UnixMilli(),
		Color: p.Color,
	}

	broadcastChat(chatMsg)
	stats.ChatMessages++
}

func broadcastChat(msg ChatMessage) {
	chatMu.Lock()
	chatHistory = append(chatHistory, msg)
	if len(chatHistory) > 1000 {
		chatHistory = chatHistory[len(chatHistory)-1000:]
	}
	chatMu.Unlock()

	mu.RLock()
	defer mu.RUnlock()

	for playerID := range conns {
		sendToClient(playerID, map[string]any{
			"type":  "chat",
			"from":  msg.From,
			"text":  msg.Text,
			"time":  msg.Time,
			"color": msg.Color,
		})
	}
}

func isPositionValid(x, y float64) bool {
	points := []struct{ dx, dy float64 }{
		{0, 0},
		{-tileSize / 3, -tileSize / 3},
		{tileSize / 3, -tileSize / 3},
		{-tileSize / 3, tileSize / 3},
		{tileSize / 3, tileSize / 3},
		{-tileSize / 3, 0},
		{tileSize / 3, 0},
		{0, -tileSize / 3},
		{0, tileSize / 3},
	}

	for _, point := range points {
		tx := int((x + point.dx) / float64(tileSize))
		ty := int((y + point.dy) / float64(tileSize))

		if tx < 0 || ty < 0 || tx >= mapW || ty >= mapH {
			return false
		}

		if gameMap[ty][tx] != 0 {
			return false
		}
	}

	return true
}

func broadcastLoop() {
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		broadcastToAll()
	}
}

func broadcastToAll() {
	mu.RLock()

	if len(players) == 0 {
		mu.RUnlock()
		return
	}

	playerList := make([]map[string]any, 0, len(players))
	for _, p := range players {
		if p.Dead {
			continue
		}
		playerList = append(playerList, map[string]any{
			"id":    p.ID,
			"name":  p.Name,
			"race":  p.Race,
			"x":     p.X,
			"y":     p.Y,
			"tx":    p.TargetX,
			"ty":    p.TargetY,
			"hp":    p.HP,
			"color": p.Color,
		})
	}

	msg := map[string]any{
		"type": "state",
		"ts":   time.Now().UnixMilli(),
		"data": playerList,
	}

	turnMu.RLock()
	if len(playersOrder) > 0 {
		msg["current_turn"] = playersOrder[currentTurn]
		timeLeft := (turnTimeout - time.Since(turnStartTime)).Seconds()
		if timeLeft < 0 {
			timeLeft = 0
		}
		msg["turn_time_left"] = timeLeft
	}
	turnMu.RUnlock()

	data, err := json.Marshal(msg)
	if err != nil {
		mu.RUnlock()
		log.Println("Ошибка маршалинга:", err)
		return
	}

	lastStateMu.Lock()
	lastState = data
	lastStateMu.Unlock()

	for id, conn := range conns {
		go func(playerID string, conn *Connection) {
			conn.mu.Lock()
			defer conn.mu.Unlock()

			if conn.closed {
				return
			}

			conn.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err := conn.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("Ошибка отправки игроку %s: %v", playerID, err)
				conn.closed = true
				conn.conn.Close()
			}
		}(id, conn)
	}

	stats.MessagesSent++
	stats.LastUpdate = time.Now()
	mu.RUnlock()
}

func sendRawToClient(playerID string, data []byte) {
	mu.RLock()
	conn, ok := conns[playerID]
	mu.RUnlock()

	if !ok {
		return
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if conn.closed {
		return
	}

	conn.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("Ошибка отправки игроку %s: %v", playerID, err)
		conn.closed = true
		conn.conn.Close()
	}
}

func genMap() {
	gameMap = make([][]int, mapH)
	for y := 0; y < mapH; y++ {
		gameMap[y] = make([]int, mapW)
		for x := 0; x < mapW; x++ {
			gameMap[y][x] = 0
		}
	}

	centerMin := mapW/2 - 2
	centerMax := mapW/2 + 2
	for y := centerMin; y <= centerMax; y++ {
		for x := centerMin; x <= centerMax; x++ {
			gameMap[y][x] = 0
		}
	}

	isInCenter := func(x, y int) bool {
		return x >= centerMin && x <= centerMax && y >= centerMin && y <= centerMax
	}

	numLakes := rand.Intn(5) + 5
	for i := 0; i < numLakes; i++ {
		attempts := 0
		for {
			attempts++
			if attempts > 100 {
				break
			}
			cx := rand.Intn(mapW-20) + 10
			cy := rand.Intn(mapH-20) + 10
			if isInCenter(cx, cy) {
				continue
			}
			rx := rand.Intn(6) + 4
			ry := rand.Intn(6) + 4

			for dy := -ry; dy <= ry; dy++ {
				for dx := -rx; dx <= rx; dx++ {
					if dx*dx*ry*ry+dy*dy*rx*rx <= rx*rx*ry*ry {
						x := cx + dx
						y := cy + dy
						if x >= 0 && x < mapW && y >= 0 && y < mapH && !isInCenter(x, y) {
							gameMap[y][x] = 1
						}
					}
				}
			}
			break
		}
	}

	numRocks := rand.Intn(10) + 10
	for i := 0; i < numRocks; i++ {
		attempts := 0
		for {
			attempts++
			if attempts > 100 {
				break
			}
			cx := rand.Intn(mapW-12) + 6
			cy := rand.Intn(mapH-12) + 6
			if isInCenter(cx, cy) {
				continue
			}
			size := rand.Intn(8) + 5
			generateRock(gameMap, cx, cy, size)
			break
		}
	}

	log.Printf("Карта сгенерирована: %dx%d тайлов", mapW, mapH)
	log.Printf("Безопасная зона в центре: 5x5 клеток")
}

func findSafeSpawn() (float64, float64) {
	centerMin := mapW/2 - 2
	centerMax := mapW/2 + 2

	var candidates []struct{ x, y int }
	for y := centerMin; y <= centerMax; y++ {
		for x := centerMin; x <= centerMax; x++ {
			if gameMap[y][x] == 0 {
				candidates = append(candidates, struct{ x, y int }{x, y})
			}
		}
	}
	if len(candidates) == 0 {
		return float64(centerMin*tileSize + tileSize/2), float64(centerMin*tileSize + tileSize/2)
	}

	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	for _, c := range candidates {
		px := float64(c.x*tileSize + tileSize/2)
		py := float64(c.y*tileSize + tileSize/2)
		valid := true

		mu.RLock()
		for _, p := range players {
			if p.Dead {
				continue
			}
			distX := px - p.X
			distY := py - p.Y
			dist := math.Sqrt(distX*distX + distY*distY)
			if dist < float64(tileSize)*1.5 {
				valid = false
				break
			}
		}
		mu.RUnlock()

		if valid {
			return px, py
		}
	}

	return float64(centerMin*tileSize + tileSize/2), float64(centerMin*tileSize + tileSize/2)
}

func randID() string {
	const charset = "abcdef0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func statsLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		mu.RLock()
		stats.Players = len(players)
		uptime := time.Since(stats.StartTime).Round(time.Second)
		mu.RUnlock()

		log.Printf("📊 Статистика: Игроки: %d, Сообщений: %d, Чат: %d, Аптайм: %v",
			stats.Players, stats.MessagesSent, stats.ChatMessages, uptime)
	}
}

func cleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		mu.Lock()
		now := time.Now()
		toRemove := []string{}

		for id, conn := range conns {
			conn.mu.Lock()
			if conn.closed {
				toRemove = append(toRemove, id)
			}
			conn.mu.Unlock()
		}

		for _, id := range toRemove {
			if conn, ok := conns[id]; ok {
				conn.conn.Close()
				delete(conns, id)
			}
			if p, ok := players[id]; ok {
				delete(playerNames, p.Name)
				delete(players, id)
				playerCount--
			}
		}

		for id, p := range players {
			if p.Dead && now.Sub(p.DeathTime) > 30*time.Second {
				delete(players, id)
				delete(playerNames, p.Name)
				if conn, ok := conns[id]; ok {
					conn.conn.Close()
					delete(conns, id)
				}
			}
		}
		mu.Unlock()
	}
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()

	uptime := time.Since(stats.StartTime).Round(time.Second)

	statsData := map[string]any{
		"players":       stats.Players,
		"connections":   stats.Connections,
		"messages_sent": stats.MessagesSent,
		"chat_messages": stats.ChatMessages,
		"uptime":        uptime.String(),
		"last_update":   stats.LastUpdate.Format("15:04:05"),
		"map_size":      fmt.Sprintf("%dx%d", mapW, mapH),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statsData)
}
