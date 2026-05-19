package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// controlMsg is a JSON message from the browser. Currently only "resize".
type controlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Client is a single WebSocket connection attached to a session.
//
// Protocol:
//   - browser → tclaw: binary frames are raw keystrokes (written to the PTY);
//     text frames are JSON control messages (resize).
//   - tclaw → browser: binary frames carry raw PTY output.
type Client struct {
	session *Session
	conn    *websocket.Conn
	send    chan []byte
	once    sync.Once
}

// close tears the client down exactly once: detach from session, close the
// socket, close the send channel so writePump drains and exits.
func (c *Client) close() {
	c.once.Do(func() {
		c.session.removeClient(c)
		c.conn.Close()
		close(c.send)
	})
}

func (c *Client) readPump() {
	defer c.close()

	for {
		msgType, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		if msgType == websocket.TextMessage {
			var m controlMsg
			if json.Unmarshal(raw, &m) == nil && m.Type == "resize" {
				c.session.Resize(m.Rows, m.Cols)
				continue
			}
			continue
		}

		// Binary frame — raw keystrokes for the PTY.
		if len(raw) == 0 {
			continue
		}
		if err := c.session.Write(raw); err != nil {
			log.Printf("write to session %q: %v", c.session.Name, err)
			return
		}
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()

	for data := range c.send {
		if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			c.close()
			return
		}
	}
}
