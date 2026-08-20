package gui

import (
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// The server is bound to 127.0.0.1; any local origin may connect.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.add(c)
	defer s.hub.remove(c)
	s.sendInit()
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		s.handleClientFrame(data)
	}
}
