package main

import (
	"bytes"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
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
	Team            Team
}

func NewClient(conn *websocket.Conn, team Team) *Client {
	return &Client{
		Token:           Token(uuid.New().String()),
		Conn:            conn,
		SendPreparedMsg: make(chan *websocket.PreparedMessage),
		SendBytes:       make(chan []byte),
		Team:            team,
	}
}

func (c *Client) HandleIncomingMessage(g *Game, msg string) {
	lines := strings.Split(msg, "\n")
	if len(lines) == 0 {
		slog.Debug("Got an empty message.")
		return
	}
	switch lines[0] {
	case "Vote":
		token := lines[1]
		line := strings.Split(lines[2], ",")
		row, rowErr := strconv.Atoi(line[0])
		if rowErr != nil {
			slog.Error("Failed to parse vote msg.", "error", rowErr)
		}
		col, colErr := strconv.Atoi(line[1])
		if colErr != nil {
			slog.Error("Failed to parse vote msg.", "error", colErr)
		}
		g.CountVote <- Vote{
			Token: token,
			Team:  c.Team,
			Move:  Move{Row: row, Col: col},
		}
	}
}

func (c *Client) HandleClient(g *Game) {
	g.ClientCounter.Inc()
	defer g.ClientCounter.Dec()

	incomingMessage := make(chan string)
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
			incomingMessage <- msg
		}
	}()

	for {
		select {
		case msg := <-incomingMessage:
			c.HandleIncomingMessage(g, string(msg))
		case msg := <-c.SendPreparedMsg:
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

type Team uint8

const (
	TeamEmpty = 0
	TeamO     = 1
	TeamX     = 2
)

func (t Team) OppositeTeam() Team {
	return Team(3 - t)
}

func (t Team) String() string {
	switch t {
	case TeamO:
		return "O"
	case TeamX:
		return "X"
	}
	return ""
}

type TeamCounter struct {
	sync.RWMutex
	TeamO int
	TeamX int
}

func (tc *TeamCounter) LeaveTeam(team Team) {
	tc.Lock()
	defer tc.Unlock()
	switch team {
	case TeamO:
		tc.TeamO -= 1
	case TeamX:
		tc.TeamX -= 1
	}
}

func (tc *TeamCounter) AssignTeam() Team {
	tc.Lock()
	defer tc.Unlock()
	if tc.TeamO == 0 || tc.TeamO <= tc.TeamX {
		tc.TeamO += 1
		return TeamO
	}
	tc.TeamX += 1
	return TeamX
}

type Board [9]Team

func (board *Board) At(row, col int) Team {
	return board[row*3+col]
}

func (board *Board) Set(row, col int, team Team) {
    board[row*3+col] = team
}

func (board *Board) Clear() {
    for row := range 3 {
        for col := range 3 {
            board.Set(row, col, TeamEmpty)
        }
    }
}

type Move struct {
	Row int
	Col int
}

func (m Move) IsValid(board Board) bool {
	return board[m.Row*3+m.Col] == TeamEmpty
}

type Vote struct {
	Token string
	Team  Team
	Move  Move
}

type GameStateRound struct {
	End        time.Time
	Token      string
	Team       Team
	VotesCount map[Move]int
}

func NewGameStateRound(team Team) GameStateRound {
	return GameStateRound{
		End:        time.Now().UTC().Add(5 * time.Second),
		Token:      strconv.Itoa(rand.Int()),
		Team:       team,
		VotesCount: make(map[Move]int),
	}
}

func (gs GameStateRound) Message() string {
	endStr, err := gs.End.MarshalText()
	if err != nil {
		slog.Error("Failed to marshal date.", "error", err)
		return ""
	}
	return fmt.Sprintf("Round\n%v\n%v\n%v", gs.Token, string(endStr), gs.Team.String())
}

func (g *Game) VoteCounter() {
	var gs *GameStateRound = nil
	var stopBroadcaster = make(chan struct{})

	voteBroadcaster := func() {
		for {
			select {
			case <-time.After(200 * time.Millisecond):
				tempGs := gs
				if tempGs != nil {
					sb := strings.Builder{}
					sb.WriteString("VoteCounts\n")
					sb.WriteString(tempGs.Token)
					sb.WriteString("\n")
					for row := range 3 {
						for col := range 3 {
							count := tempGs.VotesCount[Move{Row: row, Col: col}]
							if row != 0 || col != 0 {
								sb.WriteString(" ")
							}
							sb.WriteString(strconv.Itoa(count))
						}
					}
                    // TODO: uncomment to broadcast votes
					// g.Broadcast <- []byte(sb.String())
				}
			case <-stopBroadcaster:
				return
			}
		}
	}

	for {
		select {
		case newGameState := <-g.CountVoteGameState:
			if newGameState == nil {
				stopBroadcaster <- struct{}{}
			} else if gs == nil {
				go voteBroadcaster()
			}
			gs = newGameState
		case vote := <-g.CountVote:
			if gs != nil && gs.Token == vote.Token && gs.Team == vote.Team && vote.Move.IsValid(g.Board) {
				gs.VotesCount[vote.Move] += 1
			}
		}
	}
}

type GameStateGameOver struct {
	End    time.Time
	Result Team
}

type ClientCounter struct {
	sync.RWMutex
	Value int
}

func (c *ClientCounter) Inc() {
	c.Lock()
	defer c.Unlock()
	c.Value += 1
}

func (c *ClientCounter) Dec() {
	c.Lock()
	defer c.Unlock()
	c.Value -= 1
}

type Game struct {
	Clients       *Clients
	ClientCounter ClientCounter
	AddClient     chan *Client
	Broadcast     chan []byte

	GameState          any
	Board              Board
	CountVote          chan Vote
	CountVoteGameState chan *GameStateRound
	TeamCounter        TeamCounter
}

func NewGame() *Game {
	return &Game{
		Clients:            NewClients(),
		AddClient:          make(chan *Client),
		Broadcast:          make(chan []byte),
		GameState:          GameStateNotStarted{},
		CountVote:          make(chan Vote, 100),
		CountVoteGameState: make(chan *GameStateRound),
	}
}

func (g *Game) ChangeState(newState any) {
	g.GameState = newState
}

func (g *Game) BoardMessage() string {
	b := g.Board
	sb := strings.Builder{}
	sb.WriteString("Board\n")
	for i, v := range b {
		if i != 0 {
			sb.WriteString(",")
		}
		sb.WriteString(v.String())
	}
	return sb.String()
}

func (g *Game) GameOver() (bool, Team) {
	for row := range 3 {
		if g.Board.At(row, 0) != TeamEmpty &&
			g.Board.At(row, 0) == g.Board.At(row, 1) &&
			g.Board.At(row, 1) == g.Board.At(row, 2) {
			return true, g.Board.At(row, 0)
		}
	}

	for col := range 3 {
		if g.Board.At(0, col) != TeamEmpty &&
			g.Board.At(0, col) == g.Board.At(1, col) &&
			g.Board.At(1, col) == g.Board.At(2, col) {
			return true, g.Board.At(0, col)
		}
	}

	if g.Board.At(1, 1) != TeamEmpty &&
		((g.Board.At(0, 0) == g.Board.At(1, 1) && g.Board.At(1, 1) == g.Board.At(2, 2)) ||
			(g.Board.At(0, 2) == g.Board.At(1, 1) && g.Board.At(1, 1) == g.Board.At(2, 0))) {
		return true, g.Board.At(1, 1)
	}

    filled := 0
    for row := range 3 {
        for col := range 3 {
            if g.Board.At(row, col) != TeamEmpty {
                filled += 1
            }
        }
    }

	return filled == 9, TeamEmpty
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
	const GAME_START_TRANSITION_TIME = 1 * time.Second

	switch state := g.GameState.(type) {
	case GameStateNotStarted:
		if len(g.Clients.Values) > 0 {
			transition := GameStateTransition{
				TransitionEnd: time.Now().UTC().Add(GAME_START_TRANSITION_TIME),
				Message:       "Waiting to start a new game.",
				TargetState:   NewGameStateRound(TeamO),
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
			switch state := g.GameState.(type) {
			case GameStateRound:
				g.Broadcast <- []byte(state.Message())
                g.CountVoteGameState <- &state
			}
		}
	case GameStateRound:
		now := time.Now().UTC()
		if now.After(state.End) {
            slog.Info("Round END!")
			var maxCount = 0
			var maxMove = Move{Row: -1, Col: -1}
			for row := range 3 {
				for col := range 3 {
					move := Move{Row: row, Col: col}
					count := state.VotesCount[move]
					if count > maxCount {
						maxCount = count
						maxMove = move
					}
				}
			}

            if maxCount == 0 {
                r := rand.Int() % 9;
                maxMove = Move{}
                for r >= 0 {
                    if maxMove.IsValid(g.Board) {
                        r -= 1
                    }
                    if r >= 0 {
                        if maxMove.Col < 2 {
                            maxMove.Col += 1
                        } else {
                            maxMove.Row = (maxMove.Row + 1) % 3
                            maxMove.Col = 0
                        }
                    }
                }
            }

            g.Board.Set(maxMove.Row, maxMove.Col, state.Team)
            msg := fmt.Sprintf(
                "Move\n%v,%v\n%v",
                maxMove.Row, maxMove.Col, state.Team.String(),
            )
            g.Broadcast <- []byte(msg)
            if gameOver, result := g.GameOver(); gameOver {
                g.GameState = GameStateGameOver{
                    End:    time.Now().UTC().Add(5 * time.Second),
                    Result: result,
                }
                sb := strings.Builder{}
                sb.WriteString("GameOver\n")
                switch result {
                case TeamEmpty:
                    sb.WriteString("Draw")
                case TeamO:
                    sb.WriteString("O")
                case TeamX:
                    sb.WriteString("X")
                }
                g.Broadcast <- []byte(sb.String())
            } else {
                nextTeam := state.Team.OppositeTeam()
                g.GameState = GameStateTransition{
                    TransitionEnd: time.Now().UTC().Add(3 * time.Second),
                    Message:       "Perpare for the next move.",
                    TargetState:   NewGameStateRound(nextTeam),
                }
            }
		}
	case GameStateGameOver:
		now := time.Now().UTC()
		if now.After(state.End) {
			g.GameState = GameStateNotStarted{}
            g.Board.Clear()
		}
	default:
		slog.Error("Unexpected game state.", "GameState", g.GameState)
	}
}

func makeWsConnection(game *Game, w http.ResponseWriter, r *http.Request) {
	// TODO: fix counting number of teammates (what happens when we lose connection?)
	const TOKEN_COOKIE_NAME = "AnonClientToken"

	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	cookie, err := r.Cookie(TOKEN_COOKIE_NAME)
	var client *Client
	header := make(http.Header)
	if err == nil {
		client = game.Clients.Get(Token(cookie.Value))
	}
	if err != nil || client == nil {
		client = NewClient(nil, game.TeamCounter.AssignTeam())
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

	client.SendBytes <- []byte(game.BoardMessage())

	gs := game.GameState
	switch state := gs.(type) {
	case GameStateRound:
		client.SendBytes <- []byte(state.Message())
	}

	teamMsg := fmt.Sprintf("Team\n%v", client.Team)
	client.SendBytes <- []byte(teamMsg)
}

func getGameStatus(w http.ResponseWriter, _ *http.Request, game *Game) {
    sb := strings.Builder{}

    indent := func () { sb.WriteString("    ") }
    line := func(format string, a ...any) {
        fmt.Fprintf(&sb, format, a...)
        sb.WriteByte('\n')
    }
    dateFormat := "15:04:05 2006-01-02"
    currentTime := func() {
        indent(); line("Current Time: %v", time.Now().UTC().Format(dateFormat))
    }

    printBoard := func() {
        at := func(row, col int) string {
            if game.Board.At(row, col) == TeamEmpty {
                return " "
            }
            return game.Board.At(row, col).String()
        }

        indent(); fmt.Fprintf(&sb, "%v|%v|%v\n", at(0, 0), at(0, 1), at(0, 2))
        indent(); line("-+-+-")
        indent(); fmt.Fprintf(&sb, "%v|%v|%v\n", at(1, 0), at(1, 1), at(1, 2))
        indent(); line("-+-+-")
        indent(); fmt.Fprintf(&sb, "%v|%v|%v\n", at(2, 0), at(2, 1), at(2, 2))
        line("");
    }

    switch gs := game.GameState.(type) {
        case GameStateNotStarted:
            line("Game State: not started")
        case GameStateTransition:
            line("Game State: in transition")
            indent(); line("Target State: %v", reflect.TypeOf(gs.TargetState).Name())
            timeLeft := gs.TransitionEnd.Sub(time.Now().UTC()).Round(time.Millisecond)
            indent(); line("Time Left: %v", timeLeft)
            indent(); line("Transition End: %v", gs.TransitionEnd.Format(dateFormat))
            currentTime()
        case GameStateRound:
            printBoard()
            line("Game State: round in progress")
            indent(); line("Round End: %v", gs.End.Format(dateFormat))
            currentTime()
            indent(); line("Token: %v", gs.Token)
            indent(); line("Team: %v", gs.Team.String())
            indent(); line("Votes: %v", len(gs.VotesCount))
        case GameStateGameOver:
            printBoard()
            line("Game State: game over")
            indent(); line("Result: %v", gs.Result.String())
        default:
            line("GameState: %v\n", reflect.TypeOf(gs).Name())
    }
    line("Clients: %v", len(game.Clients.Values))
    indent(); line("TeamO: %v", game.TeamCounter.TeamO)
    indent(); line("TeamX: %v", game.TeamCounter.TeamX)
    w.Write([]byte(sb.String()))
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
	go game.VoteCounter()

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

	http.HandleFunc(
		"/api/getGameStatus",
		func(w http.ResponseWriter, r *http.Request) { getGameStatus(w, r, game) },
	)

	err := http.ListenAndServe(":"+*port, nil)
	if err != nil {
		slog.Error("Server error.", "error", err)
	}
}
