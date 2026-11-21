// Package relay is the last way between an Agent and its Controller when
// they share no network (Proposal 13, Waymark 0.1.1): both open a WebSocket
// to a small relay (a Cloudflare Worker, or waymark-relay) and the relay
// joins them. The pairing's encrypted channel runs on top unchanged, so the
// relay sees only ciphertext; it never learns a key.
//
// Protocol, shared by the Worker in relay/ and Handler here:
//
//	GET <url>/r/<room>?s=c   the Controller waits in its room
//	GET <url>/r/<room>?s=a   an Agent joins the oldest waiting Controller
//	Authorization: Bearer <access key>   when the relay has one
//
// Binary messages go to the peer; the text message "ping" is answered with
// "pong" and goes nowhere. When one side closes, the relay closes the other.
// An Agent that finds no waiting Controller is closed at once.
package relay

import (
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config is a relay as the Controller sets it and the Agent learns it over
// the encrypted channel.
type Config struct {
	URL  string `json:"url"`
	Key  string `json:"key,omitempty"`
	Room string `json:"room"`
}

var roomRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Valid reports whether the config can be dialled.
func (c *Config) Valid() bool {
	if c == nil || !roomRe.MatchString(c.Room) {
		return false
	}
	u, err := url.Parse(c.URL)
	return err == nil && (u.Scheme == "wss" || u.Scheme == "ws") && u.Host != ""
}

// CleanURL accepts what a person types (https://x.workers.dev, x.workers.dev)
// and returns the WebSocket URL, or "" when it is not one.
func CleanURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(s, "https://"):
		s = "wss://" + s[len("https://"):]
	case strings.HasPrefix(s, "http://"):
		s = "ws://" + s[len("http://"):]
	case !strings.Contains(s, "://"):
		s = "wss://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return ""
	}
	return strings.TrimRight(u.String(), "/")
}

// Ping is how often a quiet socket says it is alive, below every idle
// timeout on the way.
var Ping = 25 * time.Second

// Dial opens one side of a link: "c" waits in the room, "a" joins.
func Dial(c Config, side string, timeout time.Duration) (net.Conn, error) {
	if !c.Valid() {
		return nil, errors.New("relay not set")
	}
	d := websocket.Dialer{HandshakeTimeout: timeout, Proxy: http.ProxyFromEnvironment}
	h := http.Header{}
	if c.Key != "" {
		h.Set("Authorization", "Bearer "+c.Key)
	}
	ws, resp, err := d.Dial(c.URL+"/r/"+c.Room+"?s="+side, h)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("the relay refused the access key")
		}
		return nil, err
	}
	return newConn(ws), nil
}

// Wait dials as the Controller and returns once an Agent has joined and
// sent its first bytes. Closing stop gives up.
func Wait(c Config, stop <-chan struct{}) (net.Conn, error) {
	conn, err := Dial(c, "c", 10*time.Second)
	if err != nil {
		return nil, err
	}
	wc := conn.(*Conn)
	got := make(chan error, 1)
	go func() { got <- wc.fill() }()
	select {
	case err := <-got:
		if err != nil {
			wc.Close()
			return nil, err
		}
		return wc, nil
	case <-stop:
		wc.Close()
		<-got
		return nil, errors.New("stopped")
	}
}

// Addr is the address of a relayed link; Network is "relay".
type Addr struct{}

func (Addr) Network() string { return "relay" }
func (Addr) String() string  { return "relay" }

// Conn is a net.Conn over a WebSocket: each Write is one binary message.
type Conn struct {
	ws   *websocket.Conn
	rmu  sync.Mutex
	buf  []byte
	wmu  sync.Mutex
	done chan struct{}
	once sync.Once
}

func newConn(ws *websocket.Conn) *Conn {
	c := &Conn{ws: ws, done: make(chan struct{})}
	go c.keepalive()
	return c
}

func (c *Conn) keepalive() {
	t := time.NewTicker(Ping)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.wmu.Lock()
			c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := c.ws.WriteMessage(websocket.TextMessage, []byte("ping"))
			c.ws.SetWriteDeadline(time.Time{})
			c.wmu.Unlock()
			if err != nil {
				c.Close()
				return
			}
		}
	}
}

// fill reads until there is data in buf; text messages ("pong") are skipped.
// The caller holds rmu or is the only reader.
func (c *Conn) fill() error {
	for len(c.buf) == 0 {
		t, b, err := c.ws.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				return io.EOF
			}
			return err
		}
		if t == websocket.BinaryMessage {
			c.buf = b
		}
	}
	return nil
}

func (c *Conn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if err := c.fill(); err != nil {
		return 0, err
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *Conn) Close() error {
	c.once.Do(func() { close(c.done) })
	c.wmu.Lock()
	c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	c.wmu.Unlock()
	return c.ws.Close()
}

func (c *Conn) LocalAddr() net.Addr  { return Addr{} }
func (c *Conn) RemoteAddr() net.Addr { return Addr{} }
func (c *Conn) SetDeadline(t time.Time) error {
	c.ws.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}
func (c *Conn) SetReadDeadline(t time.Time) error { return c.ws.SetReadDeadline(t) }

// SetWriteDeadline is taken under the write lock so the keepalive's own
// deadline never overrides it mid-write.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.ws.SetWriteDeadline(t)
}

// ---------- the relay itself (waymark-relay, tests) ----------

// MaxMessage bounds one relayed message; a frame of the channel is at most
// 1 MB plus its header.
const MaxMessage = 2 << 20

type side struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	peer *side
	pmu  sync.Mutex
	gone chan struct{}
	once sync.Once
}

func (s *side) send(t int, b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return s.ws.WriteMessage(t, b)
}

func (s *side) close() {
	s.once.Do(func() {
		close(s.gone)
		s.ws.Close()
	})
}

type server struct {
	key   string
	mu    sync.Mutex
	rooms map[string][]*side
	up    websocket.Upgrader
}

// Handler serves the relay protocol. An empty key lets anyone in.
func Handler(key string) http.Handler {
	return &server{key: key, rooms: map[string][]*side{}, up: websocket.Upgrader{
		ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10,
		CheckOrigin: func(*http.Request) bool { return true }, // no cookies, nothing to steal
	}}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	room, ok := strings.CutPrefix(r.URL.Path, "/r/")
	who := r.URL.Query().Get("s")
	if !ok || !roomRe.MatchString(room) || (who != "a" && who != "c") {
		http.Error(w, "Waymark relay", http.StatusNotFound)
		return
	}
	if s.key != "" {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.key)) != 1 {
			http.Error(w, "access key needed", http.StatusUnauthorized)
			return
		}
	}
	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(MaxMessage)
	me := &side{ws: ws, gone: make(chan struct{})}
	s.mu.Lock()
	if who == "c" {
		s.rooms[room] = append(s.rooms[room], me)
	} else {
		q := s.rooms[room]
		var c *side
		for len(q) > 0 && c == nil {
			c, q = q[0], q[1:]
			select {
			case <-c.gone:
				c = nil
			default:
			}
		}
		s.rooms[room] = q
		if len(q) == 0 {
			delete(s.rooms, room)
		}
		if c == nil {
			s.mu.Unlock()
			ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "no controller"), time.Now().Add(time.Second))
			ws.Close()
			return
		}
		c.pmu.Lock()
		c.peer = me
		c.pmu.Unlock()
		me.peer = c
	}
	s.mu.Unlock()
	defer func() {
		me.close()
		me.pmu.Lock()
		p := me.peer
		me.pmu.Unlock()
		if p != nil {
			p.close()
		}
		if who == "c" {
			s.drop(room, me)
		}
	}()
	for {
		t, b, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if t == websocket.TextMessage {
			if string(b) == "ping" {
				me.send(websocket.TextMessage, []byte("pong"))
			}
			continue
		}
		me.pmu.Lock()
		p := me.peer
		me.pmu.Unlock()
		if p != nil && p.send(websocket.BinaryMessage, b) != nil {
			return
		}
	}
}

func (s *server) drop(room string, me *side) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.rooms[room]
	for i, x := range q {
		if x == me {
			s.rooms[room] = append(q[:i:i], q[i+1:]...)
			break
		}
	}
	if len(s.rooms[room]) == 0 {
		delete(s.rooms, room)
	}
}
