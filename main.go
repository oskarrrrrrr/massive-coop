package main

import (
	"bytes"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var port = flag.String("port", "8080", "")
var debug = flag.Bool("debug", false, "")
var upgrader = websocket.Upgrader{}

func getIndexHandler() http.HandlerFunc {
	N := func(start, end int) chan int {
		stream := make(chan int)
		go func() {
			for i := start; i <= end; i++ {
				stream <- i
			}
			close(stream)
		}()
		return stream
	}

	t := template.New("index.html").Funcs(template.FuncMap{"N": N})
	t, err := t.ParseFiles("templates/index.html")
	if err != nil {
		panic(err)
	}

	b := bytes.Buffer{}
	err = t.Execute(&b, struct{ N int }{N: 3})
	if err != nil {
		panic(err)
	}

	index := func(w http.ResponseWriter, r *http.Request) {
		w.Write(b.Bytes())
	}
	return index
}

type Token string

type Client struct {
	Token           Token
	Conn            *websocket.Conn
	SendPreparedMsg chan *websocket.PreparedMessage
	SendBytes       chan []byte
}

func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		Token:           Token(uuid.New().String()),
		Conn:            conn,
		SendPreparedMsg: make(chan *websocket.PreparedMessage),
		SendBytes:       make(chan []byte),
	}
}

func (c *Client) HandleClient(g *Game) {
	done := make(chan struct{})

	go func() {
		for {
			messageType, p, err := c.Conn.ReadMessage()
			if err != nil {
				slog.Debug("Failed to read websocket.", "token", c.Token, "error", err)
				done <- struct{}{}
				return
			}
			if messageType == websocket.BinaryMessage {
				slog.Error("Accepting only textual messages.", "token", c.Token)
				continue
			}
			msg := string(p)
			slog.Debug("Got message.", "token", c.Token, "msg", msg)
			// incomingMessage <- msg
		}
	}()

	for {
		select {
		case msg := <-c.SendPreparedMsg:
			slog.Debug("Sending prepared message.")
			err := c.Conn.WritePreparedMessage(msg)
			if err != nil {
				slog.Warn("Failed to send prepared message.")
				return
			}
		case msg := <-c.SendBytes:
			err := c.Conn.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				slog.Warn("Failed to send message.")
				return
			}
		case <-done:
			slog.Debug("Client disconnected.", "token", c.Token)
			c.Conn.Close()
			return
		case <-time.After(time.Second):
			slog.Debug("Client handler running.")
		}
	}
}

type Clients struct {
	sync.RWMutex
	Values map[Token]*Client
}

func NewClients() *Clients {
	return &Clients{
		Values: make(map[Token]*Client),
	}
}

func (clients *Clients) Put(client *Client) {
	clients.Lock()
	defer clients.Unlock()
	clients.Values[client.Token] = client
}

func (clients *Clients) Get(token Token) *Client {
	clients.Lock()
	defer clients.Unlock()
	return clients.Values[token]
}

func (clients *Clients) Remove(client *Client) {
	clients.Lock()
	defer clients.Unlock()
	delete(clients.Values, client.Token)
}

type GameStateNotStarted struct {
}

type GameStateTransition struct {
	TransitionEnd time.Time
	Message       string
	TargetState   any
}

type Game struct {
	Clients   *Clients
	AddClient chan *Client
	Broadcast chan []byte

	GameState any
}

func NewGame() *Game {
	return &Game{
		Clients:   NewClients(),
		AddClient: make(chan *Client),
		Broadcast: make(chan []byte),
		GameState: GameStateNotStarted{},
	}
}

func (g *Game) ChangeState(newState any) {
	g.GameState = newState
}

func (g *Game) ManageClients() {
	for {
		select {
		case client := <-g.AddClient:
			g.Clients.Put(client)
		case msg := <-g.Broadcast:
			preparedMsg, err := websocket.NewPreparedMessage(websocket.TextMessage, msg)
			if err != nil {
				slog.Error("Failed to prepare message.")
			} else {
				slog.Debug("PreparedMessage ready!")
				for _, client := range g.Clients.Values {
					if client != nil {
						slog.Debug("Sending msg to client", "token", client.Token)
						client.SendPreparedMsg <- preparedMsg
					}
				}
			}
		}
	}
}

func (g *Game) Run() {
	const REFRESH_RATE = 50 * time.Millisecond
	for {
		select {
		case <-time.After(REFRESH_RATE):
			g.run()
		}
	}
}

func (g *Game) run() {
	const GAME_START_TRANSITION_TIME = 5 * time.Second

	switch state := g.GameState.(type) {
	case GameStateNotStarted:
		if len(g.Clients.Values) > 0 {
			transition := GameStateTransition{
				TransitionEnd: time.Now().UTC().Add(GAME_START_TRANSITION_TIME),
				Message:       "Waiting to start a new game.",
				TargetState:   GameStateNotStarted{},
			}
			g.GameState = transition
			dateStr, err := transition.TransitionEnd.MarshalText()
			if err != nil {
				slog.Error("Failed to marshal transition end.")
				return
			}
			msg := fmt.Sprintf("Transition\n%v\n%v", string(dateStr), transition.Message)
			g.Broadcast <- []byte(msg)
		}
	case GameStateTransition:
		now := time.Now().UTC()
		if now.After(state.TransitionEnd) {
			g.GameState = state.TargetState
		}

	default:
		slog.Error("Unexpected game state.", "GameState", g.GameState)
	}
}

func makeWsConnection(game *Game, w http.ResponseWriter, r *http.Request) {
	const TOKEN_COOKIE_NAME = "AnonClientToken"

	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	cookie, err := r.Cookie(TOKEN_COOKIE_NAME)
	var client *Client
	header := make(http.Header)
	if err == nil {
		client = game.Clients.Get(Token(cookie.Value))
	}
	if err != nil || client == nil {
		client = NewClient(nil)
		cookie = &http.Cookie{
			Name:  "AnonClientToken",
			Value: string(client.Token),
			Path:  "/ws",
		}
	}
	header.Add("Set-Cookie", cookie.String())

	client.Conn, err = upgrader.Upgrade(w, r, header)
	if err != nil {
		slog.Error("Failed to upgrade connection.", slog.Any("error", err))
		return
	}
	slog.Debug("Client connected.", "token", client.Token)
	game.AddClient <- client
	go client.HandleClient(game)
}

func main() {
	flag.Parse()
	if *debug || os.Getenv("DEBUG") != "" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	slog.Info("TicTacToe server started.", "port", *port)

	game := NewGame()
	go game.ManageClients()
	go game.Run()

	http.HandleFunc("GET /{$}", getIndexHandler())
	http.Handle(
		"/static/",
		http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))),
	)

	http.HandleFunc(
		"/api/ws",
		func(w http.ResponseWriter, r *http.Request) {
			makeWsConnection(game, w, r)
		},
	)

	err := http.ListenAndServe(":"+*port, nil)
	if err != nil {
		slog.Error("Server error.", "error", err)
	}
}
