package main

import (
	"bytes"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"text/template"

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
	Token Token
	Conn  *websocket.Conn
}

func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		Token: Token(uuid.New().String()),
		Conn:  conn,
	}
}

type Clients struct {
	sync.RWMutex
	values map[Token]*Client
}

func NewClients() *Clients {
	return &Clients{
		values: make(map[Token]*Client),
	}
}

func (clients *Clients) Put(client *Client) {
	clients.Lock()
	defer clients.Unlock()
	clients.values[client.Token] = client
}

func (clients *Clients) Get(token Token) *Client {
	clients.Lock()
	defer clients.Unlock()
	return clients.values[token]
}

func (clients *Clients) Remove(client *Client) {
	clients.Lock()
	defer clients.Unlock()
	delete(clients.values, client.Token)
}

type Game struct {
	Clients   *Clients
	AddClient chan *Client
}

func NewGame() *Game {
	return &Game{
		Clients:   NewClients(),
		AddClient: make(chan *Client),
	}
}

func (g *Game) ManageClients() {
	for {
		select {
		case client := <-g.AddClient:
			g.Clients.Put(client)
		}
	}
}

func HandleClient(g *Game, c *Client) {
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
		case <-done:
			slog.Debug("Client disconnected.", "token", c.Token)
			c.Conn.Close()
			return

		}
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
	go HandleClient(game, client)
}

func main() {
	flag.Parse()
	if *debug || os.Getenv("DEBUG") != "" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	slog.Info("TicTacToe server started.", "port", *port)

	game := NewGame()
	go game.ManageClients()

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
