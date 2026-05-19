package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// InMessage is what the browser sends.
//
//	text   → literal keystrokes (e.g. "@/path/file ")
//	input  → paste text + submit key (the composer)
//	key    → a single tmux key name (e.g. "Up", "C-c")
//	resize → set the tmux window size to cols×rows
//	pause  → stop the capture-pane polling
//	resume → restart the capture-pane polling
type InMessage struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	SubmitKey string `json:"submit_key,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

// OutMessage is what tclaw sends to the browser.
//
//	snapshot → full pane content (on connect / on resume)
//	update   → full pane content, sent when it changed
//	sync     → reports the pause state (Paused field)
type OutMessage struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Ready  bool   `json:"ready,omitempty"`
	Paused bool   `json:"paused"`
}

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func (c *Client) readPump() {
	defer func() {
		c.hub.removeClient(c)
		c.conn.Close()
	}()

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws read error: %v", err)
			}
			return
		}

		var msg InMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("bad message: %v", err)
			continue
		}

		switch msg.Type {
		case "text":
			if msg.Text != "" {
				if err := c.hub.session.SendText(msg.Text); err != nil {
					log.Printf("send text error: %v", err)
				}
				c.hub.notifyInput()
			}
		case "input":
			if msg.Text != "" {
				if err := c.hub.session.SendInput(msg.Text, msg.SubmitKey); err != nil {
					log.Printf("send input error: %v", err)
				}
				c.hub.notifyInput()
			}
		case "key":
			if msg.Text != "" {
				if err := c.hub.session.SendKey(msg.Text); err != nil {
					log.Printf("send key error: %v", err)
				}
				c.hub.notifyInput()
			}
		case "resize":
			if err := c.hub.session.Resize(msg.Cols, msg.Rows); err != nil {
				log.Printf("resize error: %v", err)
			}
			c.hub.notifyInput()
		case "pause":
			c.hub.setPaused(true)
		case "resume":
			c.hub.setPaused(false)
		}
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()

	for data := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws write error: %v", err)
			return
		}
	}
}
