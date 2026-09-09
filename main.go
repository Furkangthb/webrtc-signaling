package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
)

type Message struct {
	Type    string          `json:"type"`
	Room    string          `json:"room"`
	Payload json.RawMessage `json:"payload"`
}

var upgrade = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")

		if origin == "http://localhost:8080" {
			return true
		}

		allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
		if allowedOrigin != "" && origin == allowedOrigin {
			return true
		}

		fmt.Println("Reddedilen bağlantı denemesi (Origin):", origin)
		return false
	},
}
var rooms = make(map[string][]*websocket.Conn)
var mutex sync.Mutex

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Upgrade hatasi", err)
		return
	}
	defer conn.Close()

	var currentRoom string
	joinedRoom := false

	for {
		_, rawMessage, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("Client ayrıldı", err)
			if joinedRoom {
				msg := Message{Type: "peer-left", Room: currentRoom, Payload: nil}
				peerLeftPayload, marshalErr := json.Marshal(msg)
				if marshalErr == nil {
					broadcastToRoom(currentRoom, conn, peerLeftPayload)
				}
				removeFromRoom(currentRoom, conn)
				joinedRoom = false
			}
			break
		}

		var msg Message
		err = json.Unmarshal(rawMessage, &msg)
		if err != nil {
			fmt.Println("JSON parse hatası:", err)
			continue
		}

		if msg.Type == "join" {
			currentRoom = msg.Room

			mutex.Lock()
			roomSize := len(rooms[currentRoom])

			if roomSize >= 2 {
				mutex.Unlock()
				fullMessage := Message{Type: "room-full", Room: currentRoom, Payload: nil}
				fullPayload, err := json.Marshal(fullMessage)
				if err == nil {
					conn.WriteMessage(websocket.TextMessage, fullPayload)
				}
				continue
			}

			if roomSize == 1 {
				readyMessage := Message{Type: "ready", Room: currentRoom, Payload: nil}
				readyPayload, err := json.Marshal(readyMessage)
				if err == nil {
					conn.WriteMessage(websocket.TextMessage, readyPayload)
				}
			}

			rooms[currentRoom] = append(rooms[currentRoom], conn)
			joinedRoom = true
			mutex.Unlock()
			fmt.Println("Client odaya katıldı", currentRoom)
			continue
		}
		broadcastToRoom(currentRoom, conn, rawMessage)
	}
}

func broadcastToRoom(room string, sender *websocket.Conn, message []byte) {
	mutex.Lock()
	defer mutex.Unlock()
	for _, c := range rooms[room] {
		if c != sender {
			err := c.WriteMessage(websocket.TextMessage, message)
			if err != nil {
				fmt.Println("Yazma hatası", err)
			}
		}
	}
}

func removeFromRoom(room string, conn *websocket.Conn) {
	mutex.Lock()
	defer mutex.Unlock()

	connections := rooms[room]
	for i, c := range connections {
		if c == conn {
			rooms[room] = append(connections[:i], connections[i+1:]...)
			break
		}
	}
}

func main() {
	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)

	http.HandleFunc("/ws", wsHandler)

	fmt.Println("Sunucu 8080 portunda baslatılıyor...")

	http.ListenAndServe("0.0.0.0:8080", nil)
}
