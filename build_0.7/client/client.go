package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/text"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

const (
	screenW  = 1920
	screenH  = 1080
	tileSize = 32
	speed    = 4.0
	scale    = 1.5

	chatHeightFixed = 400
	turnTimeout     = 20.0

	swordHiltLen    = 16.0
	swordHiltW      = 6.0
	swordCrossLen   = 24.0
	swordCrossW     = 4.0
	swordBladeLen   = 35.0
	swordBladeBaseW = 14.0
	swordBladeTipW  = 5.0

	spearHeadLen  = 30.0
	spearHeadW    = 12.0
	spearShaftLen = 50.0
	spearShaftW   = 5.0
	spearButtLen  = 10.0
	spearButtW    = 8.0

	moveDuration = 0.05
)

type NetColor struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
	A uint8 `json:"a"`
}

type Player struct {
	ID          string
	Name        string
	Race        string
	X, Y        float64
	TargetX     float64
	TargetY     float64
	HP          int
	Color       NetColor
	Image       *ebiten.Image
	LastUpdate  time.Time
	Initialized bool
	IsMe        bool

	Moving        bool
	MoveStartTime time.Time
	MoveStartX    float64
	MoveStartY    float64
	MoveEndX      float64
	MoveEndY      float64
}

type ChatMessage struct {
	From  string
	Text  string
	Time  int64
	Color NetColor
}

type Game struct {
	state string

	mu             sync.RWMutex
	conn           *websocket.Conn
	id             string
	players        map[string]*Player
	gameMap        [][]int
	camX, camY     float64
	name           string
	ready          bool
	connected      bool
	tileCache      map[int]*ebiten.Image
	lastMove       time.Time
	lastKeys       struct{ w, a, s, d bool }
	debugInfo      string
	myPlayer       *Player
	serverTime     time.Time
	quit           chan bool
	disconnectTime time.Time
	lastPing       time.Time
	moveQueue      []MoveCommand
	fontFace       font.Face
	chatFontFace   font.Face
	logoFontFace   font.Face
	nameFontFace   font.Face
	showDebug      bool
	connectionLost bool
	lastF1Press    time.Time
	lastF11Press   time.Time

	mySwordCurrentAngle float64
	mySwordTargetAngle  float64

	chatOpen         bool
	chatBuffer       string
	chatHistory      []ChatMessage
	chatLastToggle   time.Time
	lastChatMessage  time.Time
	chatCursor       bool
	chatCursorTimer  time.Time
	chatJustOpened   bool
	chatScrollOffset int
	chatLineHeight   int
	chatUserScrolled bool

	charName           string
	charRace           string
	charWeapon         string
	charColors         []color.RGBA
	colorTaken         []bool
	charSelectedColor  int
	charError          string
	charConnecting     bool
	charNameEdit       bool
	charColorButtons   []image.Rectangle
	charConnectButton  image.Rectangle
	charRaceHumanBtn   image.Rectangle
	charRaceCatBtn     image.Rectangle
	charWeaponSwordBtn image.Rectangle
	charWeaponSpearBtn image.Rectangle
	charNameInputRect  image.Rectangle
	charPreviewImg     *ebiten.Image
	colorsFetched      bool

	mainMenuMap         [][]int
	mainMenuOffsetX     float64
	mainMenuOffsetY     float64
	mainMenuButtons     []MainMenuButton
	mainMenuButtonRects []image.Rectangle

	showQuitConfirm  bool
	quitConfirmRects struct {
		bg   image.Rectangle
		yes  image.Rectangle
		no   image.Rectangle
		exit image.Rectangle
	}
	prevEscPressed bool

	showDeathScreen  bool
	deathScreenRects struct {
		bg image.Rectangle
		ok image.Rectangle
	}

	currentTurn  string
	turnTimeLeft float64
	myTurn       bool

	hoveredEnemyID string
	glowImage      *ebiten.Image

	volume       int
	fullscreen   bool
	volumeSlider struct {
		rect     image.Rectangle
		dragging bool
	}
	fullscreenBtn image.Rectangle
	backBtn       image.Rectangle
}

type MainMenuButton struct {
	Text   string
	Action func(g *Game)
}

type MoveCommand struct {
	dx, dy float64
	ts     time.Time
}

var tileColors = map[int]color.RGBA{
	0: {80, 160, 80, 255},
	1: {60, 100, 200, 255},
	2: {100, 100, 100, 255},
}

func createGlowImage(size int) *ebiten.Image {
	img := ebiten.NewImage(size, size)
	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			if x < 2 || x >= size-2 || y < 2 || y >= size-2 {
				img.Set(x, y, color.RGBA{255, 0, 0, 150})
			}
		}
	}
	return img
}

func createPlayerImage(col NetColor) *ebiten.Image {
	img := ebiten.NewImage(tileSize, tileSize)
	img.Fill(color.RGBA{col.R, col.G, col.B, 255})
	return img
}

func generateMenuColors() []color.RGBA {
	colors := make([]color.RGBA, 20)
	for i := 0; i < 20; i++ {
		hue := float64(i) * (360.0 / 20)
		r, g, b := hsvToRgb(hue, 0.8, 0.8)
		colors[i] = color.RGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255}
	}
	return colors
}

func hsvToRgb(h, s, v float64) (r, g, b float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c

	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func rgbaToNetColor(c color.RGBA) NetColor {
	return NetColor{R: c.R, G: c.G, B: c.B, A: c.A}
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
			if nx >= 0 && nx < len(gameMap[0]) && ny >= 0 && ny < len(gameMap) && gameMap[ny][nx] == 0 {
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

func generateMainMenuMap() [][]int {
	fmt.Println("Генерация карты главного меню...")
	mapW := 100
	mapH := 100
	gameMap := make([][]int, mapH)
	for y := 0; y < mapH; y++ {
		gameMap[y] = make([]int, mapW)
		for x := 0; x < mapW; x++ {
			gameMap[y][x] = 0
		}
	}

	numLakes := rand.Intn(5) + 5
	fmt.Printf("Генерация %d озёр...\n", numLakes)
	for i := 0; i < numLakes; i++ {
		cx := rand.Intn(mapW-20) + 10
		cy := rand.Intn(mapH-20) + 10
		rx := rand.Intn(6) + 4
		ry := rand.Intn(6) + 4
		for dy := -ry; dy <= ry; dy++ {
			for dx := -rx; dx <= rx; dx++ {
				if dx*dx*ry*ry+dy*dy*rx*rx <= rx*rx*ry*ry {
					x := cx + dx
					y := cy + dy
					if x >= 0 && x < mapW && y >= 0 && y < mapH {
						gameMap[y][x] = 1
					}
				}
			}
		}
	}

	numRocks := rand.Intn(10) + 10
	fmt.Printf("Генерация %d камней...\n", numRocks)
	for i := 0; i < numRocks; i++ {
		cx := rand.Intn(mapW-12) + 6
		cy := rand.Intn(mapH-12) + 6
		size := rand.Intn(8) + 5
		generateRock(gameMap, cx, cy, size)
	}
	fmt.Println("Карта главного меню готова.")
	return gameMap
}

func main() {
	fmt.Println("=== Клиент ===")

	var fontFace font.Face
	var err error

	ttfData, err := os.ReadFile("medieval.ttf")
	if err == nil {
		tt, err := opentype.Parse(ttfData)
		if err == nil {
			fontFace, err = opentype.NewFace(tt, &opentype.FaceOptions{
				Size:    32 * scale,
				DPI:     72,
				Hinting: font.HintingFull,
			})
		}
	}
	if fontFace == nil {
		tt, err := opentype.Parse(goregular.TTF)
		if err != nil {
			log.Fatal("Ошибка загрузки запасного шрифта:", err)
		}
		fontFace, err = opentype.NewFace(tt, &opentype.FaceOptions{
			Size:    32 * scale,
			DPI:     72,
			Hinting: font.HintingFull,
		})
		if err != nil {
			log.Fatal("Ошибка создания запасного шрифта:", err)
		}
		fmt.Println("Используется запасной шрифт")
	} else {
		fmt.Println("Загружен шрифт medieval.ttf")
	}

	ttChat, err := opentype.Parse(goregular.TTF)
	if err != nil {
		log.Fatal("Ошибка загрузки шрифта чата:", err)
	}
	chatFontFace, err := opentype.NewFace(ttChat, &opentype.FaceOptions{
		Size:    22,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		log.Fatal("Ошибка создания шрифта чата:", err)
	}

	var logoFontFace font.Face
	if ttfData != nil {
		tt, err := opentype.Parse(ttfData)
		if err == nil {
			logoFontFace, err = opentype.NewFace(tt, &opentype.FaceOptions{
				Size:    96,
				DPI:     72,
				Hinting: font.HintingFull,
			})
		}
	}
	if logoFontFace == nil {
		logoFontFace, _ = opentype.NewFace(ttChat, &opentype.FaceOptions{
			Size:    96,
			DPI:     72,
			Hinting: font.HintingFull,
		})
	}

	var nameFontFace font.Face
	if ttfData != nil {
		tt, err := opentype.Parse(ttfData)
		if err == nil {
			nameFontFace, err = opentype.NewFace(tt, &opentype.FaceOptions{
				Size:    28,
				DPI:     72,
				Hinting: font.HintingFull,
			})
		}
	}
	if nameFontFace == nil {
		nameFontFace, _ = opentype.NewFace(ttChat, &opentype.FaceOptions{
			Size:    28,
			DPI:     72,
			Hinting: font.HintingFull,
		})
	}

	fmt.Println("Создание объекта игры...")
	game := &Game{
		state:               "mainmenu",
		players:             make(map[string]*Player),
		name:                "",
		ready:               false,
		connected:           false,
		tileCache:           make(map[int]*ebiten.Image),
		lastMove:            time.Now(),
		serverTime:          time.Now(),
		quit:                make(chan bool, 1),
		fontFace:            fontFace,
		chatFontFace:        chatFontFace,
		logoFontFace:        logoFontFace,
		nameFontFace:        nameFontFace,
		showDebug:           false,
		connectionLost:      false,
		moveQueue:           make([]MoveCommand, 0),
		chatHistory:         make([]ChatMessage, 0),
		chatOpen:            false,
		chatJustOpened:      false,
		chatScrollOffset:    0,
		chatLineHeight:      22,
		chatUserScrolled:    false,
		mySwordCurrentAngle: 0,
		mySwordTargetAngle:  0,

		charName:          "",
		charRace:          "human",
		charWeapon:        "sword",
		charColors:        generateMenuColors(),
		colorTaken:        make([]bool, 20),
		charSelectedColor: -1,
		charError:         "",
		charConnecting:    false,
		charNameEdit:      false,
		charColorButtons:  make([]image.Rectangle, 20),
		colorsFetched:     false,

		mainMenuMap:     generateMainMenuMap(),
		mainMenuOffsetX: 0,
		mainMenuOffsetY: 0,
		mainMenuButtons: []MainMenuButton{
			{Text: "Играть", Action: func(g *Game) {
				g.charSelectedColor = -1
				g.colorsFetched = false
				g.charError = ""
				g.charConnecting = false
				g.state = "character"
			}},
			{Text: "Настройки", Action: func(g *Game) {
				g.state = "settings"
			}},
			{Text: "Выход", Action: func(g *Game) { os.Exit(0) }},
		},
		mainMenuButtonRects: make([]image.Rectangle, 3),

		glowImage: createGlowImage(36),

		volume:     50,
		fullscreen: ebiten.IsFullscreen(),
	}

	game.quitConfirmRects.bg = image.Rect(0, 0, 600, 250)
	game.quitConfirmRects.yes = image.Rect(0, 0, 250, 40)
	game.quitConfirmRects.no = image.Rect(0, 0, 250, 40)
	game.quitConfirmRects.exit = image.Rect(0, 0, 250, 40)

	game.deathScreenRects.bg = image.Rect(0, 0, 400, 200)
	game.deathScreenRects.ok = image.Rect(0, 0, 100, 40)

	fmt.Println("Инициализация кэша тайлов...")
	game.initTileCache()

	ebiten.SetWindowSize(screenW, screenH)
	ebiten.SetWindowTitle("RPG")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetFullscreen(game.fullscreen)
	ebiten.SetTPS(60)
	ebiten.SetWindowClosingHandled(true)

	fmt.Println("Запуск ebiten...")
	if err := ebiten.RunGame(game); err != nil {
		if strings.Contains(err.Error(), "потеряно соединение") {
			fmt.Println("\nСоединение с сервером потеряно.")
			fmt.Println("Нажмите Enter для выхода...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}
		log.Fatal("Ошибка запуска игры:", err)
	}
}

func (g *Game) initTileCache() {
	for tileType, col := range tileColors {
		img := ebiten.NewImage(tileSize, tileSize)
		img.Fill(col)

		border := ebiten.NewImage(tileSize, tileSize)
		border.Fill(color.RGBA{0, 0, 0, 50})
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(0.9, 0.9)
		op.GeoM.Translate(tileSize*0.05, tileSize*0.05)
		img.DrawImage(border, op)

		g.tileCache[tileType] = img
	}
}

func splitLongWord(face font.Face, word string, maxWidth int) []string {
	var parts []string
	runes := []rune(word)
	current := ""
	for _, r := range runes {
		test := current + string(r)
		bounds := text.BoundString(face, test)
		if bounds.Dx() <= maxWidth {
			current = test
		} else {
			if current != "" {
				parts = append(parts, current)
			}
			current = string(r)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func wrapText(face font.Face, s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	var lines []string
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}

	currentLine := words[0]
	for _, word := range words[1:] {
		testLine := currentLine + " " + word
		bounds := text.BoundString(face, testLine)
		if bounds.Dx() <= maxWidth {
			currentLine = testLine
			continue
		}

		wordBounds := text.BoundString(face, word)
		if wordBounds.Dx() > maxWidth {
			if currentLine != "" {
				lines = append(lines, currentLine)
			}
			parts := splitLongWord(face, word, maxWidth)
			if len(parts) > 0 {
				currentLine = parts[0]
				for i := 1; i < len(parts); i++ {
					lines = append(lines, parts[i])
				}
			}
			continue
		}

		lines = append(lines, currentLine)
		currentLine = word
	}
	lines = append(lines, currentLine)
	return lines
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (g *Game) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("Паника в readLoop:", r)
		}
		g.mu.Lock()
		g.connected = false
		g.connectionLost = true
		g.disconnectTime = time.Now()
		if g.state == "game" {
			g.state = "mainmenu"
			g.charError = "Потеряно соединение с сервером"
			g.charConnecting = false
		}
		g.mu.Unlock()
	}()

	for {
		messageType, message, err := g.conn.ReadMessage()
		if err != nil {
			log.Println("Ошибка чтения сообщения:", err)
			return
		}

		if messageType != websocket.TextMessage {
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Println("Ошибка парсинга JSON:", err)
			continue
		}

		if errorMsg, ok := msg["error"].(string); ok {
			log.Printf("Ошибка от сервера: %s", errorMsg)
			g.mu.Lock()
			g.charError = errorMsg
			g.charConnecting = false
			if g.conn != nil {
				g.conn.Close()
				g.conn = nil
			}
			g.state = "character"
			g.mu.Unlock()
			return
		}

		if msgType, ok := msg["type"].(string); ok {
			switch msgType {
			case "init":
				g.handleInit(msg)
			case "map":
				g.handleMap(msg)
			case "state":
				g.handleState(msg)
			case "move_ack":
				g.handleMoveAck(msg)
			case "chat":
				g.handleChatMessage(msg)
			}
		}
	}
}

func (g *Game) handleInit(msg map[string]interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id, ok := msg["id"].(string); ok {
		g.id = id
		g.ready = true
		g.state = "game"
		g.connected = true
		g.showDeathScreen = false

		startX, startY := 0.0, 0.0
		if x, ok := msg["x"].(float64); ok {
			startX = x
		}
		if y, ok := msg["y"].(float64); ok {
			startY = y
		}

		race := "human"
		if r, ok := msg["race"].(string); ok {
			race = r
		}

		var playerColor NetColor
		if colorData, ok := msg["color"].(map[string]interface{}); ok {
			if r, ok := colorData["r"].(float64); ok {
				playerColor.R = uint8(r)
			}
			if g, ok := colorData["g"].(float64); ok {
				playerColor.G = uint8(g)
			}
			if b, ok := colorData["b"].(float64); ok {
				playerColor.B = uint8(b)
			}
			if a, ok := colorData["a"].(float64); ok {
				playerColor.A = uint8(a)
			}
		}
		if playerColor.R == 0 && playerColor.G == 0 && playerColor.B == 0 {
			playerColor = NetColor{
				R: uint8(100 + time.Now().UnixNano()%155),
				G: uint8(100 + time.Now().UnixNano()%155),
				B: uint8(100 + time.Now().UnixNano()%155),
				A: 255,
			}
		}

		img := createPlayerImage(playerColor)

		player := &Player{
			ID:          id,
			Name:        g.charName,
			Race:        race,
			X:           startX,
			Y:           startY,
			TargetX:     startX,
			TargetY:     startY,
			Image:       img,
			Initialized: true,
			HP:          10,
			Color:       playerColor,
			IsMe:        true,
			LastUpdate:  time.Now(),
			Moving:      false,
		}

		g.players[id] = player
		g.myPlayer = player
		g.name = g.charName

		log.Printf("Инициализирован с ID: %s, позиция: %.0f,%.0f, раса: %s", id, startX, startY, race)
	}
}

func (g *Game) handleMap(msg map[string]interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if data, ok := msg["data"].([]interface{}); ok {
		g.gameMap = make([][]int, len(data))
		for i, row := range data {
			if rowSlice, ok := row.([]interface{}); ok {
				g.gameMap[i] = make([]int, len(rowSlice))
				for j, val := range rowSlice {
					if num, ok := val.(float64); ok {
						g.gameMap[i][j] = int(num)
					}
				}
			}
		}
		log.Printf("Карта получена: %dx%d", len(g.gameMap[0]), len(g.gameMap))
	}
}

func (g *Game) handleState(msg map[string]interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if currentTurn, ok := msg["current_turn"].(string); ok {
		g.currentTurn = currentTurn
		g.myTurn = (g.currentTurn == g.id)
	}
	if timeLeft, ok := msg["turn_time_left"].(float64); ok {
		if timeLeft < 0 {
			timeLeft = 0
		}
		g.turnTimeLeft = timeLeft
	}

	if data, ok := msg["data"].([]interface{}); ok {
		ts := time.Now()

		seen := make(map[string]bool)

		for _, pData := range data {
			if playerMap, ok := pData.(map[string]interface{}); ok {
				id, _ := playerMap["id"].(string)
				name, _ := playerMap["name"].(string)
				race, _ := playerMap["race"].(string)
				x, _ := playerMap["x"].(float64)
				y, _ := playerMap["y"].(float64)
				tx, _ := playerMap["tx"].(float64)
				ty, _ := playerMap["ty"].(float64)
				hp, _ := playerMap["hp"].(float64)

				pl, exists := g.players[id]

				var col NetColor
				if colorData, ok := playerMap["color"].(map[string]interface{}); ok {
					if r, ok := colorData["r"].(float64); ok {
						col.R = uint8(r)
					}
					if g, ok := colorData["g"].(float64); ok {
						col.G = uint8(g)
					}
					if b, ok := colorData["b"].(float64); ok {
						col.B = uint8(b)
					}
					if a, ok := colorData["a"].(float64); ok {
						col.A = uint8(a)
					}
				}
				if col.R == 0 && col.G == 0 && col.B == 0 {
					col = NetColor{
						R: uint8(100 + ts.UnixNano()%155),
						G: uint8(100 + ts.UnixNano()%155),
						B: uint8(100 + ts.UnixNano()%155),
						A: 255,
					}
				}

				if !exists {
					img := createPlayerImage(col)

					pl = &Player{
						ID:          id,
						Name:        name,
						Race:        race,
						Image:       img,
						X:           x,
						Y:           y,
						TargetX:     tx,
						TargetY:     ty,
						Initialized: true,
						HP:          int(hp),
						Color:       col,
						IsMe:        id == g.id,
						LastUpdate:  ts,
						Moving:      false,
					}

					g.players[id] = pl

					if id == g.id {
						g.myPlayer = pl
					}
				} else {
					if pl.Color.R != col.R || pl.Color.G != col.G || pl.Color.B != col.B || pl.Color.A != col.A {
						pl.Image = createPlayerImage(col)
						pl.Color = col
					}
					pl.Race = race

					if math.Abs(pl.X-tx) > 0.1 || math.Abs(pl.Y-ty) > 0.1 {
						pl.Moving = true
						pl.MoveStartTime = time.Now()
						pl.MoveStartX = pl.X
						pl.MoveStartY = pl.Y
						pl.MoveEndX = tx
						pl.MoveEndY = ty
						pl.TargetX = tx
						pl.TargetY = ty
					} else {
						pl.Moving = false
						pl.TargetX = tx
						pl.TargetY = ty
					}

					pl.HP = int(hp)
					pl.Name = name
					pl.LastUpdate = ts
				}

				seen[id] = true
			}
		}

		if _, ok := seen[g.id]; !ok && g.myPlayer != nil {
			g.showDeathScreen = true
			g.myPlayer = nil
		}

		for id := range g.players {
			if !seen[id] && id != g.id {
				delete(g.players, id)
			}
		}
	}
}

func (g *Game) handleMoveAck(msg map[string]interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if x, ok := msg["x"].(float64); ok {
		if y, ok := msg["y"].(float64); ok {
			if g.myPlayer != nil {
				g.myPlayer.TargetX = x
				g.myPlayer.TargetY = y
				g.myPlayer.LastUpdate = time.Now()
			}
		}
	}
}

func (g *Game) handleChatMessage(msg map[string]interface{}) {
	from, _ := msg["from"].(string)
	text, _ := msg["text"].(string)
	msgTime, _ := msg["time"].(float64)

	var msgColor NetColor
	if colorData, ok := msg["color"].(map[string]interface{}); ok {
		if r, ok := colorData["r"].(float64); ok {
			msgColor.R = uint8(r)
		}
		if g, ok := colorData["g"].(float64); ok {
			msgColor.G = uint8(g)
		}
		if b, ok := colorData["b"].(float64); ok {
			msgColor.B = uint8(b)
		}
		if a, ok := colorData["a"].(float64); ok {
			msgColor.A = uint8(a)
		}
	}

	chatMsg := ChatMessage{
		From:  from,
		Text:  text,
		Time:  int64(msgTime),
		Color: msgColor,
	}

	g.mu.Lock()
	g.chatHistory = append(g.chatHistory, chatMsg)
	if len(g.chatHistory) > 200 {
		g.chatHistory = g.chatHistory[len(g.chatHistory)-200:]
	}
	g.lastChatMessage = time.Now()

	if !g.chatOpen {
		g.chatScrollOffset = 0
	}
	g.mu.Unlock()
}

func (g *Game) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyF11) {
		now := time.Now()
		if now.Sub(g.lastF11Press) > 200*time.Millisecond {
			g.fullscreen = !g.fullscreen
			ebiten.SetFullscreen(g.fullscreen)
			g.lastF11Press = now
		}
	}

	escPressed := ebiten.IsKeyPressed(ebiten.KeyEscape)

	if ebiten.IsWindowBeingClosed() {
		if !g.showQuitConfirm {
			g.showQuitConfirm = true
		}
		ebiten.SetWindowClosingHandled(true)
	}

	if g.showQuitConfirm {
		g.handleQuitConfirm()
		g.prevEscPressed = escPressed
		return nil
	}

	if g.showDeathScreen {
		g.handleDeathScreen()
		g.prevEscPressed = escPressed
		return nil
	}

	switch g.state {
	case "mainmenu":
		g.updateMainMenu()
	case "character":
		g.updateCharacterMenu()
	case "game":
		g.updateGame()
	case "settings":
		g.updateSettings()
	}

	g.prevEscPressed = escPressed
	return nil
}

func (g *Game) handleDeathScreen() {
	dw, dh := 400, 200
	dx := (screenW - dw) / 2
	dy := (screenH - dh) / 2
	g.deathScreenRects.bg = image.Rect(dx, dy, dx+dw, dy+dh)

	btnW, btnH := 150, 50
	btnX := dx + (dw-btnW)/2
	btnY := dy + 130
	g.deathScreenRects.ok = image.Rect(btnX, btnY, btnX+btnW, btnY+btnH)

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		pt := image.Pt(x, y)
		if pt.In(g.deathScreenRects.ok) {
			g.disconnect()
			g.state = "mainmenu"
			g.showDeathScreen = false
		}
	}
}

func (g *Game) handleQuitConfirm() {
	dw, dh := 600, 300
	dx := (screenW - dw) / 2
	dy := (screenH - dh) / 2
	g.quitConfirmRects.bg = image.Rect(dx, dy, dx+dw, dy+dh)

	btnW, btnH := 190, 50
	spacing := 15
	totalBtnWidth := 3*btnW + 2*spacing
	startX := dx + (dw-totalBtnWidth)/2
	btnY := dy + 180

	g.quitConfirmRects.yes = image.Rect(startX, btnY, startX+btnW, btnY+btnH)
	g.quitConfirmRects.no = image.Rect(startX+btnW+spacing, btnY, startX+2*btnW+spacing, btnY+btnH)
	g.quitConfirmRects.exit = image.Rect(startX+2*btnW+2*spacing, btnY, startX+3*btnW+2*spacing, btnY+btnH)

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		pt := image.Pt(x, y)
		if pt.In(g.quitConfirmRects.yes) {
			if g.state == "game" {
				g.disconnect()
				g.state = "mainmenu"
			} else {
				os.Exit(0)
			}
			g.showQuitConfirm = false
		} else if pt.In(g.quitConfirmRects.no) {
			g.showQuitConfirm = false
		} else if pt.In(g.quitConfirmRects.exit) {
			os.Exit(0)
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyEscape) && !g.prevEscPressed {
		g.showQuitConfirm = false
	}
}

func (g *Game) updateMainMenu() {
	g.mainMenuOffsetX += 1.0
	g.mainMenuOffsetY += 0.5

	btnW, btnH := 400, 80
	startY := 400
	for i := range g.mainMenuButtons {
		x := (screenW - btnW) / 2
		y := startY + i*(btnH+20)
		g.mainMenuButtonRects[i] = image.Rect(x, y, x+btnW, y+btnH)
	}

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		pt := image.Pt(x, y)
		for i, rect := range g.mainMenuButtonRects {
			if pt.In(rect) {
				g.mainMenuButtons[i].Action(g)
				break
			}
		}
	}
}

func (g *Game) updateSettings() {
	sliderX, sliderY := 400, 250
	sliderW, sliderH := 400, 20
	g.volumeSlider.rect = image.Rect(sliderX, sliderY, sliderX+sliderW, sliderY+sliderH)

	btnX, btnY := 400, 350
	btnW, btnH := 200, 40
	g.fullscreenBtn = image.Rect(btnX, btnY, btnX+btnW, btnY+btnH)

	backX, backY := screenW/2-100, 800
	backW, backH := 200, 60
	g.backBtn = image.Rect(backX, backY, backX+backW, backY+backH)

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		pt := image.Pt(x, y)

		if pt.In(g.volumeSlider.rect) {
			g.volumeSlider.dragging = true
		}

		if pt.In(g.fullscreenBtn) {
			g.fullscreen = !g.fullscreen
			ebiten.SetFullscreen(g.fullscreen)
		}

		if pt.In(g.backBtn) {
			g.state = "mainmenu"
		}
	}

	if g.volumeSlider.dragging {
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			x, _ := ebiten.CursorPosition()
			relX := x - g.volumeSlider.rect.Min.X
			if relX < 0 {
				relX = 0
			}
			if relX > g.volumeSlider.rect.Dx() {
				relX = g.volumeSlider.rect.Dx()
			}
			g.volume = int(float64(relX) / float64(g.volumeSlider.rect.Dx()) * 100)
		} else {
			g.volumeSlider.dragging = false
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyLeft) {
		g.volume -= 1
		if g.volume < 0 {
			g.volume = 0
		}
	}
	if ebiten.IsKeyPressed(ebiten.KeyRight) {
		g.volume += 1
		if g.volume > 100 {
			g.volume = 100
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		g.state = "mainmenu"
	}
}

func (g *Game) updateCharacterMenu() error {
	if g.charSelectedColor == -1 && !g.charConnecting {
		if !g.colorsFetched {
			go g.fetchUsedColors()
		} else {
			available := []int{}
			for i, taken := range g.colorTaken {
				if !taken {
					available = append(available, i)
				}
			}
			if len(available) > 0 {
				g.charSelectedColor = available[rand.Intn(len(available))]
			} else {
				g.charSelectedColor = rand.Intn(len(g.charColors))
			}
			g.updatePreview()

			races := []string{"human", "cat"}
			g.charRace = races[rand.Intn(len(races))]
		}
	}

	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		pt := image.Pt(x, y)

		if pt.In(g.charNameInputRect) {
			g.charNameEdit = true
		} else {
			g.charNameEdit = false
		}

		if pt.In(g.charRaceHumanBtn) {
			g.charRace = "human"
			g.updatePreview()
		}
		if pt.In(g.charRaceCatBtn) {
			g.charRace = "cat"
			g.updatePreview()
		}
		if pt.In(g.charWeaponSwordBtn) {
			g.charWeapon = "sword"
		}
		if pt.In(g.charWeaponSpearBtn) {
			g.charWeapon = "spear"
		}

		for i, rect := range g.charColorButtons {
			if pt.In(rect) {
				if !g.colorTaken[i] {
					g.charSelectedColor = i
					g.updatePreview()
				}
				break
			}
		}

		if pt.In(g.charConnectButton) && !g.charConnecting && g.charName != "" && g.charSelectedColor >= 0 {
			g.connect()
		}
	}

	if g.charNameEdit {
		for _, r := range ebiten.InputChars() {
			if unicode.IsPrint(r) && r != '�' {
				if len(g.charName) < 20 {
					g.charName += string(r)
				}
			}
		}
		if ebiten.IsKeyPressed(ebiten.KeyBackspace) {
			now := time.Now()
			if now.Sub(g.lastChatMessage) > 100*time.Millisecond || len(g.charName) == 1 {
				if len(g.charName) > 0 {
					runes := []rune(g.charName)
					runes = runes[:len(runes)-1]
					g.charName = string(runes)
				}
				g.lastChatMessage = now
			}
		}
		if ebiten.IsKeyPressed(ebiten.KeyEnter) && g.charName != "" && g.charSelectedColor >= 0 && !g.charConnecting {
			g.connect()
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		g.state = "mainmenu"
		g.charError = ""
		g.charConnecting = false
	}

	return nil
}

func (g *Game) fetchUsedColors() {
	resp, err := http.Get("http://localhost:8080/colors")
	if err != nil {
		log.Println("Не удалось получить список цветов:", err)
		return
	}
	defer resp.Body.Close()
	var colors []NetColor
	if err := json.NewDecoder(resp.Body).Decode(&colors); err != nil {
		log.Println("Ошибка парсинга цветов:", err)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.colorTaken {
		g.colorTaken[i] = false
	}
	for _, c := range colors {
		for i, col := range g.charColors {
			if col.R == c.R && col.G == c.G && col.B == c.B && col.A == c.A {
				g.colorTaken[i] = true
			}
		}
	}
	g.colorsFetched = true
}

func (g *Game) updatePreview() {
	if g.charSelectedColor < 0 {
		return
	}
	col := g.charColors[g.charSelectedColor]
	netCol := NetColor{R: col.R, G: col.G, B: col.B, A: col.A}
	g.charPreviewImg = createPlayerImage(netCol)
}

func (g *Game) connect() {
	g.charConnecting = true
	g.charError = ""

	u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/ws"}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		g.charError = "Ошибка подключения: " + err.Error()
		g.charConnecting = false
		return
	}
	g.conn = conn

	col := g.charColors[g.charSelectedColor]
	netColor := NetColor{R: col.R, G: col.G, B: col.B, A: col.A}

	err = conn.WriteJSON(map[string]interface{}{
		"name":   g.charName,
		"race":   g.charRace,
		"weapon": g.charWeapon,
		"color":  netColor,
	})
	if err != nil {
		g.charError = "Ошибка отправки данных: " + err.Error()
		g.charConnecting = false
		conn.Close()
		g.conn = nil
		return
	}

	go g.readLoop()
}

func (g *Game) updateGame() error {
	g.mu.RLock()
	connected := g.connected
	ready := g.ready
	myTurn := g.myTurn
	g.mu.RUnlock()

	if !connected && g.connectionLost {
		if time.Since(g.disconnectTime) > 3*time.Second {
			g.disconnect()
			g.state = "mainmenu"
			g.connectionLost = false
			return nil
		}
		return nil
	}

	if !ready || g.id == "" {
		return nil
	}

	if ebiten.IsKeyPressed(ebiten.KeyEscape) && !g.prevEscPressed && !g.chatOpen && !g.showQuitConfirm {
		g.showQuitConfirm = true
	}

	if ebiten.IsKeyPressed(ebiten.KeyT) && !g.chatOpen {
		now := time.Now()
		if now.Sub(g.chatLastToggle) > 200*time.Millisecond {
			g.chatOpen = true
			g.chatJustOpened = true
			g.chatCursor = true
			g.chatCursorTimer = now
			g.chatLastToggle = now
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyF1) {
		now := time.Now()
		if now.Sub(g.lastF1Press) > 200*time.Millisecond {
			g.showDebug = !g.showDebug
			g.lastF1Press = now
		}
	}

	if myTurn && ebiten.IsKeyPressed(ebiten.KeySpace) {
		now := time.Now()
		if now.Sub(g.lastMove) > 200*time.Millisecond {
			g.conn.WriteJSON(map[string]any{
				"action": "turn_action",
				"type":   "skip",
			})
			g.lastMove = now
		}
	}

	if g.chatOpen {
		_, yoff := ebiten.Wheel()
		if yoff != 0 {
			g.chatScrollOffset += int(yoff * 3)
			g.chatUserScrolled = true
		}
		g.handleChatInput()
		return nil
	}

	g.mu.Lock()
	myPlayer := g.myPlayer
	if myPlayer != nil && myTurn {
		mx, my := ebiten.CursorPosition()
		worldX := float64(mx) + g.camX
		worldY := float64(my) + g.camY
		tileX := int(worldX / tileSize)
		tileY := int(worldY / tileSize)

		myTileX := int(myPlayer.X / tileSize)
		myTileY := int(myPlayer.Y / tileSize)

		attackRange := 1
		if g.charWeapon == "spear" {
			attackRange = 2
		}

		hoveredID := ""
		for _, pl := range g.players {
			if pl.ID != g.id {
				plTileX := int(pl.X / tileSize)
				plTileY := int(pl.Y / tileSize)
				if plTileX == tileX && plTileY == tileY {
					dx := math.Abs(float64(tileX - myTileX))
					dy := math.Abs(float64(tileY - myTileY))
					if dx+dy <= float64(attackRange) && !(dx == 0 && dy == 0) {
						hoveredID = pl.ID
					}
					break
				}
			}
		}
		g.hoveredEnemyID = hoveredID
	} else {
		g.hoveredEnemyID = ""
	}
	g.mu.Unlock()

	if myTurn && ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		worldX := float64(x) + g.camX
		worldY := float64(y) + g.camY
		tileX := int(worldX / tileSize)
		tileY := int(worldY / tileSize)

		if g.myPlayer != nil {
			myTileX := int(g.myPlayer.X / tileSize)
			myTileY := int(g.myPlayer.Y / tileSize)

			attackRange := 1
			if g.charWeapon == "spear" {
				attackRange = 2
			}

			var targetPlayer *Player
			for _, pl := range g.players {
				if pl.ID != g.id {
					plTileX := int(pl.X / tileSize)
					plTileY := int(pl.Y / tileSize)
					if plTileX == tileX && plTileY == tileY {
						targetPlayer = pl
						break
					}
				}
			}

			if targetPlayer != nil {
				dx := math.Abs(float64(tileX - myTileX))
				dy := math.Abs(float64(tileY - myTileY))
				if dx+dy <= float64(attackRange) && !(dx == 0 && dy == 0) {
					g.conn.WriteJSON(map[string]any{
						"action":   "turn_action",
						"type":     "attack",
						"targetID": targetPlayer.ID,
					})
				}
			} else {
				dx := tileX - myTileX
				dy := tileY - myTileY
				if math.Abs(float64(dx)) <= 1 && math.Abs(float64(dy)) <= 1 && !(dx == 0 && dy == 0) {
					if tileX >= 0 && tileX < len(g.gameMap[0]) && tileY >= 0 && tileY < len(g.gameMap) && g.gameMap[tileY][tileX] == 0 {
						targetWorldX := float64(tileX*tileSize + tileSize/2)
						targetWorldY := float64(tileY*tileSize + tileSize/2)
						g.conn.WriteJSON(map[string]any{
							"action":  "turn_action",
							"type":    "move",
							"targetX": targetWorldX,
							"targetY": targetWorldY,
						})
					}
				}
			}
		}
	}

	g.mu.RLock()
	me := g.myPlayer
	g.mu.RUnlock()

	g.mu.Lock()
	now := time.Now()
	for _, pl := range g.players {
		if pl.Initialized {
			if pl.Moving {
				elapsed := now.Sub(pl.MoveStartTime).Seconds()
				if elapsed >= moveDuration {
					pl.X = pl.MoveEndX
					pl.Y = pl.MoveEndY
					pl.Moving = false
				} else {
					t := elapsed / moveDuration
					pl.X = pl.MoveStartX + (pl.MoveEndX-pl.MoveStartX)*t
					pl.Y = pl.MoveStartY + (pl.MoveEndY-pl.MoveStartY)*t
				}
			} else {
				if math.Abs(pl.X-pl.TargetX) > 1 || math.Abs(pl.Y-pl.TargetY) > 1 {
					pl.X = pl.TargetX
					pl.Y = pl.TargetY
				}
			}
		}
	}
	g.mu.Unlock()

	if me != nil {
		targetCamX := me.TargetX - screenW/2
		targetCamY := me.TargetY - screenH/2
		g.camX += (targetCamX - g.camX) * 0.1
		g.camY += (targetCamY - g.camY) * 0.1

		mx, my := ebiten.CursorPosition()
		px := me.X - g.camX
		py := me.Y - g.camY
		targetAngle := math.Atan2(float64(my)-py, float64(mx)-px)

		g.mySwordTargetAngle = targetAngle

		diff := g.mySwordTargetAngle - g.mySwordCurrentAngle
		for diff > math.Pi {
			diff -= 2 * math.Pi
		}
		for diff < -math.Pi {
			diff += 2 * math.Pi
		}
		g.mySwordCurrentAngle += diff * 0.4
		if math.Abs(diff) < 0.01 {
			g.mySwordCurrentAngle = g.mySwordTargetAngle
		}
	}

	if now.Sub(g.chatCursorTimer) > 500*time.Millisecond {
		g.chatCursor = !g.chatCursor
		g.chatCursorTimer = now
	}

	return nil
}

func (g *Game) handleChatInput() {
	if g.chatJustOpened {
		g.chatBuffer = ""
		g.chatJustOpened = false
		return
	}

	for _, r := range ebiten.InputChars() {
		if unicode.IsPrint(r) && r != '�' {
			g.chatBuffer += string(r)
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyBackspace) {
		now := time.Now()
		if now.Sub(g.lastChatMessage) > 100*time.Millisecond || len(g.chatBuffer) == 1 {
			if len(g.chatBuffer) > 0 {
				runes := []rune(g.chatBuffer)
				runes = runes[:len(runes)-1]
				g.chatBuffer = string(runes)
			}
			g.lastChatMessage = now
		}
	}

	if ebiten.IsKeyPressed(ebiten.KeyEnter) {
		if len(g.chatBuffer) > 0 && g.connected {
			g.conn.WriteJSON(map[string]any{
				"action": "chat",
				"text":   g.chatBuffer,
			})
			g.chatBuffer = ""
		}
		g.chatOpen = false
	}

	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		g.chatBuffer = ""
		g.chatOpen = false
	}
}

func (g *Game) canMove(x, y float64) bool {
	if g.gameMap == nil {
		return true
	}

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

		if ty < 0 || ty >= len(g.gameMap) || tx < 0 || tx >= len(g.gameMap[0]) {
			return false
		}

		if g.gameMap[ty][tx] != 0 {
			return false
		}
	}

	return true
}

func (g *Game) Draw(screen *ebiten.Image) {
	switch g.state {
	case "mainmenu":
		g.drawMainMenu(screen)
	case "character":
		g.drawCharacterMenu(screen)
	case "game":
		g.drawGame(screen)
	case "settings":
		g.drawSettings(screen)
	}
	g.drawQuitConfirm(screen)
	g.drawDeathScreen(screen)
}

func (g *Game) drawDeathScreen(screen *ebiten.Image) {
	if !g.showDeathScreen {
		return
	}
	if g.deathScreenRects.bg.Dx() <= 0 {
		return
	}

	bg := ebiten.NewImage(screenW, screenH)
	bg.Fill(color.RGBA{0x40, 0x00, 0x00, 200})
	opBg := &ebiten.DrawImageOptions{}
	screen.DrawImage(bg, opBg)

	dialogImg := ebiten.NewImage(g.deathScreenRects.bg.Dx(), g.deathScreenRects.bg.Dy())
	dialogImg.Fill(color.RGBA{0x80, 0x00, 0x00, 255})
	opDialog := &ebiten.DrawImageOptions{}
	opDialog.GeoM.Translate(float64(g.deathScreenRects.bg.Min.X), float64(g.deathScreenRects.bg.Min.Y))
	screen.DrawImage(dialogImg, opDialog)

	title := "Вы погибли!"
	titleBounds := text.BoundString(g.fontFace, title)
	titleX := g.deathScreenRects.bg.Min.X + (g.deathScreenRects.bg.Dx()-titleBounds.Dx())/2
	titleY := g.deathScreenRects.bg.Min.Y + 70
	text.Draw(screen, title, g.fontFace, titleX, titleY, color.Black)

	btnImg := ebiten.NewImage(g.deathScreenRects.ok.Dx(), g.deathScreenRects.ok.Dy())
	btnImg.Fill(color.RGBA{0xa1, 0x92, 0x59, 255})
	opBtn := &ebiten.DrawImageOptions{}
	opBtn.GeoM.Translate(float64(g.deathScreenRects.ok.Min.X), float64(g.deathScreenRects.ok.Min.Y))
	screen.DrawImage(btnImg, opBtn)

	btnText := "В меню"
	btnBounds := text.BoundString(g.fontFace, btnText)
	btnX := g.deathScreenRects.ok.Min.X + (g.deathScreenRects.ok.Dx()-btnBounds.Dx())/2
	btnY := g.deathScreenRects.ok.Min.Y + (g.deathScreenRects.ok.Dy()+btnBounds.Dy())/2
	text.Draw(screen, btnText, g.fontFace, btnX, btnY, color.Black)
}

func (g *Game) drawMainMenu(screen *ebiten.Image) {
	if g.mainMenuMap == nil {
		ebitenutil.DrawRect(screen, 0, 0, screenW, screenH, color.RGBA{80, 120, 80, 255})
		return
	}

	mapW := len(g.mainMenuMap[0])
	mapH := len(g.mainMenuMap)

	startX := int(g.mainMenuOffsetX/float64(tileSize)) - 2
	startY := int(g.mainMenuOffsetY/float64(tileSize)) - 2
	endX := startX + screenW/tileSize + 4
	endY := startY + screenH/tileSize + 4

	for y := startY; y <= endY; y++ {
		for x := startX; x <= endX; x++ {
			tileX := x % mapW
			if tileX < 0 {
				tileX += mapW
			}
			tileY := y % mapH
			if tileY < 0 {
				tileY += mapH
			}
			tileType := g.mainMenuMap[tileY][tileX]
			if tileImg, ok := g.tileCache[tileType]; ok {
				op := &ebiten.DrawImageOptions{}
				op.GeoM.Translate(
					float64(x*tileSize)-g.mainMenuOffsetX,
					float64(y*tileSize)-g.mainMenuOffsetY,
				)
				screen.DrawImage(tileImg, op)
			}
		}
	}

	title := "cats&slaps"
	bounds := text.BoundString(g.logoFontFace, title)
	x := (screenW - bounds.Dx()) / 2
	y := 200
	text.Draw(screen, title, g.logoFontFace, x, y, color.RGBA{200, 180, 100, 255})

	for i, rect := range g.mainMenuButtonRects {
		ebitenutil.DrawRect(screen, float64(rect.Min.X), float64(rect.Min.Y), float64(rect.Dx()), float64(rect.Dy()), color.RGBA{100, 80, 50, 200})
		b := text.BoundString(g.fontFace, g.mainMenuButtons[i].Text)
		tx := rect.Min.X + (rect.Dx()-b.Dx())/2
		ty := rect.Min.Y + (rect.Dy()+b.Dy())/2
		text.Draw(screen, g.mainMenuButtons[i].Text, g.fontFace, tx, ty, color.White)
	}
}

func (g *Game) drawCharacterMenu(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0xe5, 0xdb, 0xb8, 0xff})

	title := "Создание персонажа"
	bounds := text.BoundString(g.fontFace, title)
	text.Draw(screen, title, g.fontFace, (screenW-bounds.Dx())/2, 100, color.Black)

	nameLabel := "Имя:"
	text.Draw(screen, nameLabel, g.fontFace, 200, 180, color.Black)
	inputRect := image.Rect(400, 140, 900, 200)
	g.charNameInputRect = inputRect
	ebitenutil.DrawRect(screen, float64(inputRect.Min.X), float64(inputRect.Min.Y), float64(inputRect.Dx()), float64(inputRect.Dy()), color.RGBA{0xa1, 0x92, 0x59, 0xff})
	if g.charNameEdit {
		ebitenutil.DrawRect(screen, float64(inputRect.Min.X), float64(inputRect.Min.Y), float64(inputRect.Dx()), float64(inputRect.Dy()), color.RGBA{0xc0, 0xb0, 0x70, 0xff})
	}
	displayName := g.charName
	if g.charNameEdit && g.chatCursor && time.Since(g.chatCursorTimer) < 500*time.Millisecond {
		displayName += "_"
	}
	text.Draw(screen, displayName, g.fontFace, inputRect.Min.X+10, inputRect.Min.Y+45, color.Black)

	raceLabel := "Раса:"
	text.Draw(screen, raceLabel, g.fontFace, 200, 280, color.Black)

	humanBtn := image.Rect(400, 240, 600, 300)
	g.charRaceHumanBtn = humanBtn
	btnCol := color.RGBA{0xa1, 0x92, 0x59, 0xff}
	if g.charRace == "human" {
		btnCol = color.RGBA{0xc0, 0xb0, 0x70, 0xff}
	}
	ebitenutil.DrawRect(screen, float64(humanBtn.Min.X), float64(humanBtn.Min.Y), float64(humanBtn.Dx()), float64(humanBtn.Dy()), btnCol)
	humanText := "Человек"
	boundsHuman := text.BoundString(g.fontFace, humanText)
	txHuman := humanBtn.Min.X + (humanBtn.Dx()-boundsHuman.Dx())/2
	tyHuman := humanBtn.Min.Y + (humanBtn.Dy()+boundsHuman.Dy())/2
	text.Draw(screen, humanText, g.fontFace, txHuman, tyHuman, color.Black)

	catBtn := image.Rect(620, 240, 820, 300)
	g.charRaceCatBtn = catBtn
	btnCol = color.RGBA{0xa1, 0x92, 0x59, 0xff}
	if g.charRace == "cat" {
		btnCol = color.RGBA{0xc0, 0xb0, 0x70, 0xff}
	}
	ebitenutil.DrawRect(screen, float64(catBtn.Min.X), float64(catBtn.Min.Y), float64(catBtn.Dx()), float64(catBtn.Dy()), btnCol)
	catText := "Кот"
	boundsCat := text.BoundString(g.fontFace, catText)
	txCat := catBtn.Min.X + (catBtn.Dx()-boundsCat.Dx())/2
	tyCat := catBtn.Min.Y + (catBtn.Dy()+boundsCat.Dy())/2
	text.Draw(screen, catText, g.fontFace, txCat, tyCat, color.Black)

	weaponLabel := "Оружие:"
	text.Draw(screen, weaponLabel, g.fontFace, 200, 380, color.Black)

	swordBtn := image.Rect(400, 340, 600, 400)
	g.charWeaponSwordBtn = swordBtn
	btnCol = color.RGBA{0xa1, 0x92, 0x59, 0xff}
	if g.charWeapon == "sword" {
		btnCol = color.RGBA{0xc0, 0xb0, 0x70, 0xff}
	}
	ebitenutil.DrawRect(screen, float64(swordBtn.Min.X), float64(swordBtn.Min.Y), float64(swordBtn.Dx()), float64(swordBtn.Dy()), btnCol)

	swordCenterX := float64(swordBtn.Min.X + swordBtn.Dx()/2)
	swordCenterY := float64(swordBtn.Min.Y + swordBtn.Dy()/2)
	g.drawSwordScaled(screen, swordCenterX, swordCenterY, 0, 1.4)

	spearBtn := image.Rect(620, 340, 820, 400)
	g.charWeaponSpearBtn = spearBtn
	btnCol = color.RGBA{0xa1, 0x92, 0x59, 0xff}
	if g.charWeapon == "spear" {
		btnCol = color.RGBA{0xc0, 0xb0, 0x70, 0xff}
	}
	ebitenutil.DrawRect(screen, float64(spearBtn.Min.X), float64(spearBtn.Min.Y), float64(spearBtn.Dx()), float64(spearBtn.Dy()), btnCol)

	spearCenterX := float64(spearBtn.Min.X + spearBtn.Dx()/2)
	spearCenterY := float64(spearBtn.Min.Y + spearBtn.Dy()/2)
	g.drawSpearScaled(screen, spearCenterX, spearCenterY, 0, 1.4)

	colorLabel := "Цвет:"
	text.Draw(screen, colorLabel, g.fontFace, 200, 480, color.Black)

	startX, startY := 400, 440
	sw, sh := 60, 60
	spacing := 15
	for i, c := range g.charColors {
		row := i / 5
		colIdx := i % 5
		x := startX + colIdx*(sw+spacing)
		y := startY + row*(sh+spacing)
		rect := image.Rect(x, y, x+sw, y+sh)
		g.charColorButtons[i] = rect
		ebitenutil.DrawRect(screen, float64(x), float64(y), float64(sw), float64(sh), c)
		if i == g.charSelectedColor {
			ebitenutil.DrawRect(screen, float64(x-2), float64(y-2), float64(sw+4), float64(sh+4), color.White)
		}
		if g.colorTaken[i] {
			ebitenutil.DrawLine(screen, float64(x), float64(y), float64(x+sw), float64(y+sh), color.RGBA{255, 0, 0, 255})
			ebitenutil.DrawLine(screen, float64(x+sw), float64(y), float64(x), float64(y+sh), color.RGBA{255, 0, 0, 255})
		}
	}

	if g.charPreviewImg != nil {
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(4, 4)
		op.GeoM.Translate(1000, 300)
		screen.DrawImage(g.charPreviewImg, op)
		if g.charRace == "cat" && g.charSelectedColor >= 0 {
			col := g.charColors[g.charSelectedColor]
			netCol := NetColor{R: col.R, G: col.G, B: col.B, A: col.A}
			centerX := 1000 + float64(tileSize)*2
			centerY := 300 + float64(tileSize)*2
			g.drawCatEarsScaled(screen, centerX, centerY, netCol, 4.0)
		}
	}

	connectBtn := image.Rect(screenW/2-150, 700, screenW/2+150, 780)
	g.charConnectButton = connectBtn
	btnCol = color.RGBA{0xa1, 0x92, 0x59, 0xff}
	if g.charName != "" && g.charSelectedColor >= 0 && !g.charConnecting {
		btnCol = color.RGBA{0xc0, 0xb0, 0x70, 0xff}
	}
	ebitenutil.DrawRect(screen, float64(connectBtn.Min.X), float64(connectBtn.Min.Y), float64(connectBtn.Dx()), float64(connectBtn.Dy()), btnCol)
	connectText := "Подключиться"
	boundsConn := text.BoundString(g.fontFace, connectText)
	txConn := connectBtn.Min.X + (connectBtn.Dx()-boundsConn.Dx())/2
	tyConn := connectBtn.Min.Y + (connectBtn.Dy()+boundsConn.Dy())/2
	text.Draw(screen, connectText, g.fontFace, txConn, tyConn, color.Black)

	if g.charError != "" {
		text.Draw(screen, "Ошибка: "+g.charError, g.fontFace, 200, 850, color.RGBA{255, 0, 0, 255})
	}

	text.Draw(screen, "Esc - назад", g.fontFace, 200, 900, color.Black)
	text.Draw(screen, "F11 - полноэкранный режим", g.fontFace, 200, 950, color.Black)
}

func (g *Game) drawSwordScaled(screen *ebiten.Image, x, y, angle, scale float64) {
	hiltLen := swordHiltLen * scale
	hiltW := swordHiltW * scale
	crossLen := swordCrossLen * scale
	crossW := swordCrossW * scale
	bladeLen := swordBladeLen * scale
	bladeBaseW := swordBladeBaseW * scale
	bladeTipW := swordBladeTipW * scale

	hiltCenterX := x + (hiltLen/2)*math.Cos(angle)
	hiltCenterY := y + (hiltLen/2)*math.Sin(angle)
	hiltImg := ebiten.NewImage(int(hiltLen), int(hiltW))
	hiltImg.Fill(color.RGBA{139, 69, 19, 255})
	opHilt := &ebiten.DrawImageOptions{}
	opHilt.GeoM.Translate(-hiltLen/2, -hiltW/2)
	opHilt.GeoM.Rotate(angle)
	opHilt.GeoM.Translate(hiltCenterX, hiltCenterY)
	screen.DrawImage(hiltImg, opHilt)

	guardX := x + hiltLen*math.Cos(angle)
	guardY := y + hiltLen*math.Sin(angle)
	crossImg := ebiten.NewImage(int(crossLen), int(crossW))
	crossImg.Fill(color.RGBA{80, 80, 80, 255})
	opCross := &ebiten.DrawImageOptions{}
	opCross.GeoM.Translate(-crossLen/2, -crossW/2)
	opCross.GeoM.Rotate(angle + math.Pi/2)
	opCross.GeoM.Translate(guardX, guardY)
	screen.DrawImage(crossImg, opCross)

	bladeStartX := guardX
	bladeStartY := guardY
	bladeEndX := guardX + bladeLen*math.Cos(angle)
	bladeEndY := guardY + bladeLen*math.Sin(angle)

	perpX := -math.Sin(angle)
	perpY := math.Cos(angle)

	baseLeftX := bladeStartX + (bladeBaseW/2)*perpX
	baseLeftY := bladeStartY + (bladeBaseW/2)*perpY
	baseRightX := bladeStartX - (bladeBaseW/2)*perpX
	baseRightY := bladeStartY - (bladeBaseW/2)*perpY

	tipLeftX := bladeEndX + (bladeTipW/2)*perpX
	tipLeftY := bladeEndY + (bladeTipW/2)*perpY
	tipRightX := bladeEndX - (bladeTipW/2)*perpX
	tipRightY := bladeEndY - (bladeTipW/2)*perpY

	whiteTex := ebiten.NewImage(1, 1)
	whiteTex.Fill(color.White)
	col := color.RGBA{200, 200, 200, 255}
	cr, cg, cb, ca := col.RGBA()
	rf := float32(cr) / 65535.0
	gf := float32(cg) / 65535.0
	bf := float32(cb) / 65535.0
	af := float32(ca) / 65535.0

	vertices1 := []ebiten.Vertex{
		{DstX: float32(baseLeftX), DstY: float32(baseLeftY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(baseRightX), DstY: float32(baseRightY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(tipLeftX), DstY: float32(tipLeftY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
	}
	vertices2 := []ebiten.Vertex{
		{DstX: float32(baseRightX), DstY: float32(baseRightY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(tipRightX), DstY: float32(tipRightY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(tipLeftX), DstY: float32(tipLeftY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
	}
	indices := []uint16{0, 1, 2}
	screen.DrawTriangles(vertices1, indices, whiteTex, nil)
	screen.DrawTriangles(vertices2, indices, whiteTex, nil)
}

func (g *Game) drawSpearScaled(screen *ebiten.Image, x, y, angle, scale float64) {
	shaftLen := spearShaftLen * scale
	shaftW := spearShaftW * scale
	headLen := spearHeadLen * scale
	headW := spearHeadW * scale

	shaftEndX := x + shaftLen*math.Cos(angle)
	shaftEndY := y + shaftLen*math.Sin(angle)

	whiteTex := ebiten.NewImage(1, 1)
	whiteTex.Fill(color.White)
	col := color.RGBA{160, 82, 45, 255}
	cr, cg, cb, ca := col.RGBA()
	rf := float32(cr) / 65535.0
	gf := float32(cg) / 65535.0
	bf := float32(cb) / 65535.0
	af := float32(ca) / 65535.0

	perpX := -math.Sin(angle)
	perpY := math.Cos(angle)

	halfW := shaftW / 2
	vertices := []ebiten.Vertex{
		{DstX: float32(x + halfW*perpX), DstY: float32(y + halfW*perpY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(x - halfW*perpX), DstY: float32(y - halfW*perpY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(shaftEndX - halfW*perpX), DstY: float32(shaftEndY - halfW*perpY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(shaftEndX + halfW*perpX), DstY: float32(shaftEndY + halfW*perpY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
	}
	indices := []uint16{0, 1, 2, 2, 3, 0}
	screen.DrawTriangles(vertices, indices, whiteTex, nil)

	headEndX := shaftEndX + headLen*math.Cos(angle)
	headEndY := shaftEndY + headLen*math.Sin(angle)

	headCol := color.RGBA{192, 192, 192, 255}
	hr, hg, hb, ha := headCol.RGBA()
	hrf := float32(hr) / 65535.0
	hgf := float32(hg) / 65535.0
	hbf := float32(hb) / 65535.0
	haf := float32(ha) / 65535.0

	headHalfW := headW / 2
	headVertices := []ebiten.Vertex{
		{DstX: float32(shaftEndX + headHalfW*perpX), DstY: float32(shaftEndY + headHalfW*perpY), SrcX: 0, SrcY: 0, ColorR: hrf, ColorG: hgf, ColorB: hbf, ColorA: haf},
		{DstX: float32(shaftEndX - headHalfW*perpX), DstY: float32(shaftEndY - headHalfW*perpY), SrcX: 0, SrcY: 0, ColorR: hrf, ColorG: hgf, ColorB: hbf, ColorA: haf},
		{DstX: float32(headEndX), DstY: float32(headEndY), SrcX: 0, SrcY: 0, ColorR: hrf, ColorG: hgf, ColorB: hbf, ColorA: haf},
	}
	screen.DrawTriangles(headVertices, []uint16{0, 1, 2}, whiteTex, nil)
}

func (g *Game) drawSettings(screen *ebiten.Image) {
	if g.volumeSlider.rect.Dx() == 0 {
		sliderX, sliderY := 400, 250
		sliderW, sliderH := 400, 20
		g.volumeSlider.rect = image.Rect(sliderX, sliderY, sliderX+sliderW, sliderY+sliderH)
	}
	if g.fullscreenBtn.Dx() == 0 {
		btnX, btnY := 400, 350
		btnW, btnH := 200, 40
		g.fullscreenBtn = image.Rect(btnX, btnY, btnX+btnW, btnY+btnH)
	}
	if g.backBtn.Dx() == 0 {
		backX, backY := screenW/2-100, 800
		backW, backH := 200, 60
		g.backBtn = image.Rect(backX, backY, backX+backW, backY+backH)
	}

	screen.Fill(color.RGBA{0xe5, 0xdb, 0xb8, 0xff})

	title := "Настройки"
	bounds := text.BoundString(g.fontFace, title)
	text.Draw(screen, title, g.fontFace, (screenW-bounds.Dx())/2, 100, color.Black)

	volText := fmt.Sprintf("Громкость музыки: %d%%", g.volume)
	text.Draw(screen, volText, g.fontFace, 200, 200, color.Black)

	sliderBg := ebiten.NewImage(g.volumeSlider.rect.Dx(), g.volumeSlider.rect.Dy())
	sliderBg.Fill(color.RGBA{100, 100, 100, 255})
	opSliderBg := &ebiten.DrawImageOptions{}
	opSliderBg.GeoM.Translate(float64(g.volumeSlider.rect.Min.X), float64(g.volumeSlider.rect.Min.Y))
	screen.DrawImage(sliderBg, opSliderBg)

	fillW := int(float64(g.volume) / 100.0 * float64(g.volumeSlider.rect.Dx()))
	ebitenutil.DrawRect(screen, float64(g.volumeSlider.rect.Min.X), float64(g.volumeSlider.rect.Min.Y),
		float64(fillW), float64(g.volumeSlider.rect.Dy()), color.RGBA{200, 180, 100, 255})

	btnCol := color.RGBA{0xa1, 0x92, 0x59, 0xff}
	ebitenutil.DrawRect(screen, float64(g.fullscreenBtn.Min.X), float64(g.fullscreenBtn.Min.Y),
		float64(g.fullscreenBtn.Dx()), float64(g.fullscreenBtn.Dy()), btnCol)

	fullText := "Оконный режим"
	if g.fullscreen {
		fullText = "Полноэкранный режим"
	}
	boundsFull := text.BoundString(g.chatFontFace, fullText)
	txFull := g.fullscreenBtn.Min.X + (g.fullscreenBtn.Dx()-boundsFull.Dx())/2
	tyFull := g.fullscreenBtn.Min.Y + (g.fullscreenBtn.Dy()+boundsFull.Dy())/2
	text.Draw(screen, fullText, g.chatFontFace, txFull, tyFull, color.Black)

	ebitenutil.DrawRect(screen, float64(g.backBtn.Min.X), float64(g.backBtn.Min.Y),
		float64(g.backBtn.Dx()), float64(g.backBtn.Dy()), color.RGBA{0xa1, 0x92, 0x59, 0xff})
	backText := "Назад"
	boundsBack := text.BoundString(g.fontFace, backText)
	txBack := g.backBtn.Min.X + (g.backBtn.Dx()-boundsBack.Dx())/2
	tyBack := g.backBtn.Min.Y + (g.backBtn.Dy()+boundsBack.Dy())/2
	text.Draw(screen, backText, g.fontFace, txBack, tyBack, color.Black)
}

func (g *Game) drawCatEarsScaled(screen *ebiten.Image, centerX, centerY float64, col NetColor, scale float64) {
	darkCol := color.RGBA{
		R: uint8(float64(col.R) * 0.7),
		G: uint8(float64(col.G) * 0.7),
		B: uint8(float64(col.B) * 0.7),
		A: 255,
	}

	x1 := centerX - float64(tileSize)/2*scale
	y1 := centerY - float64(tileSize)/2*scale
	x2 := centerX - float64(tileSize)/6*scale
	y2 := centerY - float64(tileSize)/2*scale
	x3 := centerX - float64(tileSize)/3*scale
	y3 := centerY - float64(tileSize)/2*scale - float64(tileSize)/3*scale
	g.fillTriangle(screen, x1, y1, x2, y2, x3, y3, darkCol)

	x1 = centerX + float64(tileSize)/6*scale
	y1 = centerY - float64(tileSize)/2*scale
	x2 = centerX + float64(tileSize)/2*scale
	y2 = centerY - float64(tileSize)/2*scale
	x3 = centerX + float64(tileSize)/3*scale
	y3 = centerY - float64(tileSize)/2*scale - float64(tileSize)/3*scale
	g.fillTriangle(screen, x1, y1, x2, y2, x3, y3, darkCol)
}

func (g *Game) drawGame(screen *ebiten.Image) {
	screen.Fill(color.RGBA{20, 20, 40, 255})

	g.mu.RLock()

	if !g.connected && g.connectionLost {
		msg := "❌ Потеряно соединение с сервером"
		msg2 := "Возврат в меню..."

		bounds := text.BoundString(g.fontFace, msg)
		x := (screenW - bounds.Dx()) / 2
		y := screenH / 2

		text.Draw(screen, msg, g.fontFace, x, y, color.White)
		text.Draw(screen, msg2, g.fontFace, x, y+40, color.White)

		g.mu.RUnlock()
		return
	}

	if !g.ready {
		msg := "🔄 Подключение к серверу..."
		bounds := text.BoundString(g.fontFace, msg)
		x := (screenW - bounds.Dx()) / 2
		y := screenH / 2
		text.Draw(screen, msg, g.fontFace, x, y, color.White)
		g.mu.RUnlock()
		return
	}

	if g.gameMap == nil {
		msg := "🗺️ Загрузка карты..."
		bounds := text.BoundString(g.fontFace, msg)
		x := (screenW - bounds.Dx()) / 2
		y := screenH / 2
		text.Draw(screen, msg, g.fontFace, x, y, color.White)
		g.mu.RUnlock()
		return
	}

	me := g.myPlayer

	playersCopy := make(map[string]*Player)
	for id, pl := range g.players {
		playersCopy[id] = pl
	}
	meCopy := me
	gameMapCopy := g.gameMap
	camX, camY := g.camX, g.camY
	showDebug := g.showDebug
	myTurn := g.myTurn
	currentTurn := g.currentTurn
	turnTimeLeft := g.turnTimeLeft
	if turnTimeLeft < 0 {
		turnTimeLeft = 0
	}
	hoveredEnemyID := g.hoveredEnemyID

	chatHistoryCopy := make([]ChatMessage, len(g.chatHistory))
	copy(chatHistoryCopy, g.chatHistory)
	chatOpen := g.chatOpen
	chatBuffer := g.chatBuffer
	chatCursor := g.chatCursor
	lastChatMessage := g.lastChatMessage
	chatCursorTimer := g.chatCursorTimer

	g.mu.RUnlock()

	startX := int(camX/float64(tileSize)) - 2
	startY := int(camY/float64(tileSize)) - 2
	endX := int((camX+screenW)/float64(tileSize)) + 3
	endY := int((camY+screenH)/float64(tileSize)) + 3

	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}
	if endX > len(gameMapCopy[0]) {
		endX = len(gameMapCopy[0])
	}
	if endY > len(gameMapCopy) {
		endY = len(gameMapCopy)
	}

	for y := startY; y < endY; y++ {
		for x := startX; x < endX; x++ {
			tileType := gameMapCopy[y][x]
			if tileImg, ok := g.tileCache[tileType]; ok {
				op := &ebiten.DrawImageOptions{}
				op.GeoM.Translate(
					float64(x*tileSize)-camX,
					float64(y*tileSize)-camY,
				)
				screen.DrawImage(tileImg, op)
			}
		}
	}

	if myTurn && meCopy != nil {
		myTileX := int(meCopy.X / tileSize)
		myTileY := int(meCopy.Y / tileSize)
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				tileX := myTileX + dx
				tileY := myTileY + dy
				if tileX >= 0 && tileX < len(gameMapCopy[0]) && tileY >= 0 && tileY < len(gameMapCopy) {
					if gameMapCopy[tileY][tileX] == 0 {
						occupied := false
						for _, pl := range playersCopy {
							if pl.ID != g.id {
								plTileX := int(pl.X / tileSize)
								plTileY := int(pl.Y / tileSize)
								if plTileX == tileX && plTileY == tileY {
									occupied = true
									break
								}
							}
						}
						if !occupied {
							highlight := ebiten.NewImage(tileSize, tileSize)
							highlight.Fill(color.RGBA{0, 40, 0, 20})
							op := &ebiten.DrawImageOptions{}
							op.GeoM.Translate(float64(tileX*tileSize)-camX, float64(tileY*tileSize)-camY)
							screen.DrawImage(highlight, op)
						}
					}
				}
			}
		}
	}

	for _, pl := range playersCopy {
		if !pl.Initialized {
			continue
		}
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(pl.X-camX-float64(tileSize)/2, pl.Y-camY-float64(tileSize)/2)
		screen.DrawImage(pl.Image, op)
		if pl.Race == "cat" {
			g.drawCatEarsScaled(screen, pl.X-camX, pl.Y-camY, pl.Color, 1.0)
		}
		if hoveredEnemyID == pl.ID && myTurn && meCopy != nil {
			glowOp := &ebiten.DrawImageOptions{}
			half := float64(g.glowImage.Bounds().Dx()) / 2
			glowOp.GeoM.Translate(pl.X-camX-half, pl.Y-camY-half)
			screen.DrawImage(g.glowImage, glowOp)
		}
	}

	for _, pl := range playersCopy {
		if !pl.Initialized {
			continue
		}
		nameText := pl.Name
		nameBounds := text.BoundString(g.nameFontFace, nameText)
		nameX := int(pl.X-camX) - nameBounds.Dx()/2
		nameY := int(pl.Y - camY - float64(tileSize) - 20)
		if pl.IsMe {
			text.Draw(screen, nameText, g.nameFontFace, nameX+1, nameY+1, color.Black)
			text.Draw(screen, nameText, g.nameFontFace, nameX, nameY, color.RGBA{173, 216, 230, 255})
		} else {
			text.Draw(screen, nameText, g.nameFontFace, nameX, nameY, color.White)
		}
	}

	for _, pl := range playersCopy {
		if pl.IsMe || !pl.Initialized {
			continue
		}
		g.drawSword(screen, pl.X-camX, pl.Y-camY, math.Pi/4)
	}

	if meCopy != nil {
		if g.charWeapon == "sword" {
			g.drawSword(screen, meCopy.X-camX, meCopy.Y-camY, g.mySwordCurrentAngle)
		} else {
			g.drawSpearScaled(screen, meCopy.X-camX, meCopy.Y-camY, g.mySwordCurrentAngle, 1.0)
		}
	}

	currentPlayerName := ""
	if p, ok := playersCopy[currentTurn]; ok {
		currentPlayerName = p.Name
	}
	g.drawTurnTimer(screen, turnTimeLeft, myTurn, currentPlayerName)

	g.drawChat(screen, chatHistoryCopy, chatOpen, chatBuffer, chatCursor, lastChatMessage, chatCursorTimer)

	if showDebug {
		var xCoord, yCoord float64
		if meCopy != nil {
			xCoord, yCoord = meCopy.X, meCopy.Y
		}
		debugText := fmt.Sprintf("FPS: %.1f | Игроков: %d | X: %.0f Y: %.0f | Оружие: %s",
			ebiten.ActualFPS(),
			len(playersCopy),
			xCoord, yCoord,
			g.charWeapon)

		if meCopy != nil {
			debugText += fmt.Sprintf(" | HP: %d", meCopy.HP)
		}
		debugText += "\nF1 - отладка | ЛКМ - движение/атака | Space - пропустить ход | T - открыть чат | Esc - закрыть чат/меню | F11 - полноэкранный режим"

		lines := strings.Split(debugText, "\n")
		for i, line := range lines {
			text.Draw(screen, line, g.chatFontFace, 20, 40+i*30, color.White)
		}
	}
}

func (g *Game) drawSpear(screen *ebiten.Image, x, y float64, angle float64) {
	g.drawSpearScaled(screen, x, y, angle, 1.0)
}

func (g *Game) drawSword(screen *ebiten.Image, x, y float64, angle float64) {
	g.drawSwordScaled(screen, x, y, angle, 1.0)
}

func (g *Game) drawTurnTimer(screen *ebiten.Image, timeLeft float64, myTurn bool, currentPlayerName string) {
	const (
		timerX = screenW - 250
		timerY = screenH - 200
		width  = 150.0
		height = 120.0
	)

	if timeLeft <= 0 && currentPlayerName == "" {
		return
	}

	textX := timerX - 330
	textY := timerY - 40
	textW := 300.0
	textH := 170.0
	vector.DrawFilledRect(screen, float32(textX), float32(textY), float32(textW), float32(textH), color.RGBA{0, 0, 0, 150}, false)

	if myTurn {
		text.Draw(screen, "ВАШ ХОД", g.fontFace, int(textX)+10, int(textY)+40, color.RGBA{255, 255, 0, 255})
	} else {
		text.Draw(screen, "Ход игрока", g.fontFace, int(textX)+10, int(textY)+40, color.White)
		text.Draw(screen, currentPlayerName, g.fontFace, int(textX)+10, int(textY)+85, color.White)
	}
	timeStr := fmt.Sprintf("%.1f", timeLeft)
	text.Draw(screen, timeStr, g.fontFace, int(textX)+10, int(textY)+135, color.White)

	tipWidth := 10.0
	baseW := width * 0.7
	topY := float64(timerY)
	bottomY := float64(timerY) + height
	waistY := float64(timerY) + height/2
	cx := float64(timerX) + width/2

	borderColor := color.Gray{Y: 100}

	vector.StrokeLine(screen, float32(cx-tipWidth/2), float32(waistY), float32(cx+tipWidth/2), float32(waistY), 2, borderColor, true)
	vector.StrokeLine(screen, float32(cx+tipWidth/2), float32(waistY), float32(cx+baseW/2), float32(topY), 2, borderColor, true)
	vector.StrokeLine(screen, float32(cx+baseW/2), float32(topY), float32(cx-baseW/2), float32(topY), 2, borderColor, true)
	vector.StrokeLine(screen, float32(cx-baseW/2), float32(topY), float32(cx-tipWidth/2), float32(waistY), 2, borderColor, true)

	vector.StrokeLine(screen, float32(cx+tipWidth/2), float32(waistY), float32(cx+baseW/2), float32(bottomY), 2, borderColor, true)
	vector.StrokeLine(screen, float32(cx+baseW/2), float32(bottomY), float32(cx-baseW/2), float32(bottomY), 2, borderColor, true)
	vector.StrokeLine(screen, float32(cx-baseW/2), float32(bottomY), float32(cx-tipWidth/2), float32(waistY), 2, borderColor, true)

	fillRatio := timeLeft / turnTimeout
	if fillRatio < 0 {
		fillRatio = 0
	}
	if fillRatio > 1 {
		fillRatio = 1
	}

	whiteTex := ebiten.NewImage(1, 1)
	whiteTex.Fill(color.White)

	sandColor := color.RGBA{210, 180, 140, 200}
	cr, cg, cb, ca := sandColor.RGBA()
	rf := float32(cr) / 65535.0
	gf := float32(cg) / 65535.0
	bf := float32(cb) / 65535.0
	af := float32(ca) / 65535.0

	halfHeight := height / 2

	if fillRatio > 0 {
		fillYTop := waistY - fillRatio*halfHeight
		t := (waistY - fillYTop) / halfHeight
		widthAtY := (1-t)*tipWidth + t*baseW

		vertices1 := []ebiten.Vertex{
			{DstX: float32(cx - tipWidth/2), DstY: float32(waistY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx + tipWidth/2), DstY: float32(waistY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx - widthAtY/2), DstY: float32(fillYTop), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		}
		indices := []uint16{0, 1, 2}
		screen.DrawTriangles(vertices1, indices, whiteTex, nil)

		vertices2 := []ebiten.Vertex{
			{DstX: float32(cx + tipWidth/2), DstY: float32(waistY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx + widthAtY/2), DstY: float32(fillYTop), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx - widthAtY/2), DstY: float32(fillYTop), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		}
		screen.DrawTriangles(vertices2, indices, whiteTex, nil)
	}

	if fillRatio < 1 {
		fillYBottom := bottomY - (1-fillRatio)*halfHeight
		t := (fillYBottom - waistY) / halfHeight
		widthAtY := (1-t)*tipWidth + t*baseW

		vertices1 := []ebiten.Vertex{
			{DstX: float32(cx - widthAtY/2), DstY: float32(fillYBottom), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx + widthAtY/2), DstY: float32(fillYBottom), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx - baseW/2), DstY: float32(bottomY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		}
		indices := []uint16{0, 1, 2}
		screen.DrawTriangles(vertices1, indices, whiteTex, nil)

		vertices2 := []ebiten.Vertex{
			{DstX: float32(cx + widthAtY/2), DstY: float32(fillYBottom), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx + baseW/2), DstY: float32(bottomY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
			{DstX: float32(cx - baseW/2), DstY: float32(bottomY), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		}
		screen.DrawTriangles(vertices2, indices, whiteTex, nil)
	}
}

func (g *Game) fillTriangle(screen *ebiten.Image, x1, y1, x2, y2, x3, y3 float64, col color.Color) {
	whiteTex := ebiten.NewImage(1, 1)
	whiteTex.Fill(color.White)

	cr, cg, cb, ca := col.RGBA()
	rf := float32(cr) / 65535.0
	gf := float32(cg) / 65535.0
	bf := float32(cb) / 65535.0
	af := float32(ca) / 65535.0

	vertices := []ebiten.Vertex{
		{DstX: float32(x1), DstY: float32(y1), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(x2), DstY: float32(y2), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
		{DstX: float32(x3), DstY: float32(y3), SrcX: 0, SrcY: 0, ColorR: rf, ColorG: gf, ColorB: bf, ColorA: af},
	}
	indices := []uint16{0, 1, 2}
	screen.DrawTriangles(vertices, indices, whiteTex, nil)
}

func (g *Game) drawChat(screen *ebiten.Image, chatHistory []ChatMessage, chatOpen bool, chatBuffer string, chatCursor bool, _ time.Time, chatCursorTimer time.Time) {
	const (
		chatWidth    = 500
		margin       = 10
		lineHeight   = 22
		textLeftPad  = 10
		textRightPad = 10
	)

	chatHeight := chatHeightFixed

	chatBg := ebiten.NewImage(chatWidth, chatHeight)
	chatBg.Fill(color.RGBA{0, 0, 0, 180})
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(margin), float64(screenH-chatHeight-margin))
	screen.DrawImage(chatBg, op)

	type displayLine struct {
		nick      string
		nickColor color.Color
		text      string
	}
	displayLines := []displayLine{}

	for _, msg := range chatHistory {
		nick := "[" + msg.From + "]: "
		nickColor := color.RGBA{msg.Color.R, msg.Color.G, msg.Color.B, 255}
		textMaxWidth := chatWidth - textLeftPad - textRightPad - text.BoundString(g.chatFontFace, nick).Dx() - 5
		textLines := wrapText(g.chatFontFace, msg.Text, textMaxWidth)
		if len(textLines) == 0 {
			textLines = []string{""}
		}

		for i, line := range textLines {
			if i == 0 {
				displayLines = append(displayLines, displayLine{
					nick:      nick,
					nickColor: nickColor,
					text:      line,
				})
			} else {
				indent := strings.Repeat(" ", len(nick)/2)
				displayLines = append(displayLines, displayLine{
					nick:      "",
					nickColor: nil,
					text:      indent + line,
				})
			}
		}
	}

	totalLines := len(displayLines)

	var maxLines int
	if chatOpen {
		inputMaxWidth := chatWidth - textLeftPad - textRightPad
		inputLines := wrapText(g.chatFontFace, "> "+chatBuffer, inputMaxWidth)
		if len(inputLines) == 0 {
			inputLines = []string{"> "}
		}
		inputHeight := len(inputLines)*lineHeight + 10
		maxLines = (chatHeight - 20 - inputHeight) / lineHeight
		if maxLines < 1 {
			maxLines = 1
		}
	} else {
		maxLines = (chatHeight - 20) / lineHeight
		if maxLines < 1 {
			maxLines = 1
		}
	}

	maxOffset := max(0, totalLines-maxLines)

	if g.chatUserScrolled {
		if g.chatScrollOffset > maxOffset {
			g.chatScrollOffset = maxOffset
		}
		if g.chatScrollOffset < 0 {
			g.chatScrollOffset = 0
		}
	} else {
		g.chatScrollOffset = 0
	}

	startIdx := totalLines - maxLines - g.chatScrollOffset
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + maxLines
	if endIdx > totalLines {
		endIdx = totalLines
	}

	yPos := screenH - chatHeight + 5
	for i := startIdx; i < endIdx; i++ {
		line := displayLines[i]
		xPos := margin + textLeftPad
		if line.nick != "" {
			text.Draw(screen, line.nick, g.chatFontFace, xPos, yPos, line.nickColor)
			xPos += text.BoundString(g.chatFontFace, line.nick).Dx()
		}
		text.Draw(screen, line.text, g.chatFontFace, xPos, yPos, color.White)
		yPos += lineHeight
	}

	if chatOpen {
		inputMaxWidth := chatWidth - textLeftPad - textRightPad
		inputLines := wrapText(g.chatFontFace, "> "+chatBuffer, inputMaxWidth)
		if len(inputLines) == 0 {
			inputLines = []string{"> "}
		}
		inputHeight := len(inputLines)*lineHeight + 10
		inputX := margin + 5
		inputY := screenH - margin - inputHeight - 5

		inputBg := ebiten.NewImage(chatWidth-10, inputHeight)
		inputBg.Fill(color.RGBA{40, 40, 40, 220})
		opInput := &ebiten.DrawImageOptions{}
		opInput.GeoM.Translate(float64(inputX), float64(inputY))
		screen.DrawImage(inputBg, opInput)

		yInput := inputY + 5
		for _, line := range inputLines {
			text.Draw(screen, line, g.chatFontFace, inputX+5, yInput+lineHeight, color.White)
			yInput += lineHeight
		}

		if chatCursor && time.Since(chatCursorTimer) < 500*time.Millisecond {
			lastLine := inputLines[len(inputLines)-1]
			cursorX := inputX + 5 + text.BoundString(g.chatFontFace, lastLine).Dx()
			cursorY := yInput - lineHeight + 5
			text.Draw(screen, "_", g.chatFontFace, cursorX, cursorY, color.White)
		}
	}
}

func (g *Game) drawQuitConfirm(screen *ebiten.Image) {
	if !g.showQuitConfirm {
		return
	}
	if g.quitConfirmRects.bg.Dx() <= 0 {
		return
	}

	bg := ebiten.NewImage(screenW, screenH)
	bg.Fill(color.RGBA{0, 0, 0, 150})
	opBg := &ebiten.DrawImageOptions{}
	screen.DrawImage(bg, opBg)

	dialogImg := ebiten.NewImage(g.quitConfirmRects.bg.Dx(), g.quitConfirmRects.bg.Dy())
	dialogImg.Fill(color.RGBA{0xc0, 0xb0, 0x70, 255})
	opDialog := &ebiten.DrawImageOptions{}
	opDialog.GeoM.Translate(float64(g.quitConfirmRects.bg.Min.X), float64(g.quitConfirmRects.bg.Min.Y))
	screen.DrawImage(dialogImg, opDialog)

	title := "Сдаёшься?"
	titleBounds := text.BoundString(g.fontFace, title)
	titleX := g.quitConfirmRects.bg.Min.X + (g.quitConfirmRects.bg.Dx()-titleBounds.Dx())/2
	titleY := g.quitConfirmRects.bg.Min.Y + 50
	text.Draw(screen, title, g.fontFace, titleX, titleY, color.Black)

	btnColor := color.RGBA{0xa1, 0x92, 0x59, 255}
	exitBtnColor := color.RGBA{0xc0, 0x80, 0x80, 255}

	yesImg := ebiten.NewImage(g.quitConfirmRects.yes.Dx(), g.quitConfirmRects.yes.Dy())
	yesImg.Fill(btnColor)
	opYes := &ebiten.DrawImageOptions{}
	opYes.GeoM.Translate(float64(g.quitConfirmRects.yes.Min.X), float64(g.quitConfirmRects.yes.Min.Y))
	screen.DrawImage(yesImg, opYes)
	yesText := "Главное меню"
	yesBounds := text.BoundString(g.fontFace, yesText)
	yesX := g.quitConfirmRects.yes.Min.X + (g.quitConfirmRects.yes.Dx()-yesBounds.Dx())/2
	yesY := g.quitConfirmRects.yes.Min.Y + (g.quitConfirmRects.yes.Dy()+yesBounds.Dy())/2
	text.Draw(screen, yesText, g.fontFace, yesX, yesY, color.Black)

	noImg := ebiten.NewImage(g.quitConfirmRects.no.Dx(), g.quitConfirmRects.no.Dy())
	noImg.Fill(btnColor)
	opNo := &ebiten.DrawImageOptions{}
	opNo.GeoM.Translate(float64(g.quitConfirmRects.no.Min.X), float64(g.quitConfirmRects.no.Min.Y))
	screen.DrawImage(noImg, opNo)
	noText := "Нет"
	noBounds := text.BoundString(g.fontFace, noText)
	noX := g.quitConfirmRects.no.Min.X + (g.quitConfirmRects.no.Dx()-noBounds.Dx())/2
	noY := g.quitConfirmRects.no.Min.Y + (g.quitConfirmRects.no.Dy()+noBounds.Dy())/2
	text.Draw(screen, noText, g.fontFace, noX, noY, color.Black)

	exitImg := ebiten.NewImage(g.quitConfirmRects.exit.Dx(), g.quitConfirmRects.exit.Dy())
	exitImg.Fill(exitBtnColor)
	opExit := &ebiten.DrawImageOptions{}
	opExit.GeoM.Translate(float64(g.quitConfirmRects.exit.Min.X), float64(g.quitConfirmRects.exit.Min.Y))
	screen.DrawImage(exitImg, opExit)
	exitText := "Выйти"
	exitBounds := text.BoundString(g.fontFace, exitText)
	exitX := g.quitConfirmRects.exit.Min.X + (g.quitConfirmRects.exit.Dx()-exitBounds.Dx())/2
	exitY := g.quitConfirmRects.exit.Min.Y + (g.quitConfirmRects.exit.Dy()+exitBounds.Dy())/2
	text.Draw(screen, exitText, g.fontFace, exitX, exitY, color.Black)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenW, screenH
}

func (g *Game) disconnect() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn != nil {
		g.conn.Close()
		g.conn = nil
	}
	g.connected = false
	g.ready = false
	g.id = ""
	g.players = make(map[string]*Player)
	g.myPlayer = nil
	g.gameMap = nil
}
