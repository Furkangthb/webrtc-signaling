package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

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

		if origin == "" || origin == "http://localhost:8080" || origin == "http://127.0.0.1:8080" {
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

func generateTurnCredentials(secret string, ttlSeconds int) (string, string) {
	timestamp := time.Now().Unix() + int64(ttlSeconds)
	username := strconv.FormatInt(timestamp, 10)

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	password := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return username, password
}

func recordHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Kayıt için Upgrade hatası:", err)
		return
	}
	defer conn.Close()

	os.MkdirAll("kayitlar", os.ModePerm)

	fileName := fmt.Sprintf("kayitlar/gorusme_%d.webm", time.Now().Unix())

	file, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Dosya açılamadı:", err)
		return
	}
	defer file.Close()

	fmt.Println("🎥 Yeni kayıt akışı başladı. Dosya:", fileName)

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("Kayıt bağlantısı koptu veya tamamlandı:", err)
			break
		}

		if messageType == websocket.BinaryMessage {
			_, err := file.Write(message)
			if err != nil {
				fmt.Println("Dosyaya yazma hatası:", err)
				break
			}
		}
	}
}

func turnCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")

	if origin != "" && origin != "http://localhost:8080" && origin != "http://127.0.0.1:8080" {
		if allowedOrigin == "" || origin != allowedOrigin {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"success": false, "error": "Yetkisiz origin"}`))
			return
		}
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	turnSecret := os.Getenv("TURN_SECRET")
	if turnSecret == "" {
		fmt.Println("HATA: TURN_SECRET ortam değişkeni bulunamadı!")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"success": false, "error": "Sunucu yapılandırma hatası"}`))
		return
	}

	username, password := generateTurnCredentials(turnSecret, 86400)

	response := map[string]interface{}{
		"success":    true,
		"username":   username,
		"credential": password,
	}

	json.NewEncoder(w).Encode(response)
}

func main() {
	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)

	http.HandleFunc("/ws", wsHandler)
	http.HandleFunc("/api/turn-credentials", turnCredentialsHandler)
	http.HandleFunc("/api/record", recordHandler)

	fmt.Println("Sunucu 8080 portunda baslatılıyor...")

	http.ListenAndServe("0.0.0.0:8080", nil)
}
