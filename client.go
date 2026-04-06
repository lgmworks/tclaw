package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // No auth for now
	},
}

// InMessage is what the browser sends.
type InMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// OutMessage is what tclaw sends to the browser.
type OutMessage struct {
	Type          string   `json:"type"`
	Text          string   `json:"text,omitempty"`
	Lines         []string `json:"lines,omitempty"`
	FullLineCount int      `json:"full_line_count,omitempty"`
	Ready         bool     `json:"ready,omitempty"`
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

		if msg.Type == "input" && msg.Text != "" {
			if err := c.hub.session.SendInput(msg.Text); err != nil {
				log.Printf("send input error: %v", err)
			}
			c.hub.notifyInput()
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
