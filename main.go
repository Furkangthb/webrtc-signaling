package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Message struct {
	Type    string          `json:"type"`
	Room    string          `json:"room"`
	Payload json.RawMessage `json:"payload"`
}

var upgrade = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")

		if origin == "" || origin == "http://localhost:8080" || origin == "http://127.0.0.1:8080" || origin == "https://webrtc-signaling-kjw9.onrender.com" {
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

var roomSessions = make(map[string]string)
var sessionMutex sync.Mutex

func getOrCreateSessionID(room string) string {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	if sid, ok := roomSessions[room]; ok {
		return sid
	}
	sid := randomToken(9)
	roomSessions[room] = sid
	return sid
}

func clearSessionID(room string) {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	delete(roomSessions, room)
}

var googleOauthConfig *oauth2.Config

type SessionData struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	Exp     int64  `json:"exp"`
}

func sign(payload string) string {
	secret := os.Getenv("SESSION_SECRET")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func createSessionCookie(w http.ResponseWriter, data SessionData) {
	data.Exp = time.Now().Add(7 * 24 * time.Hour).Unix()
	raw, _ := json.Marshal(data)
	encoded := base64.URLEncoding.EncodeToString(raw)
	value := encoded + "." + sign(encoded)

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    value,
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func getSession(r *http.Request) (*SessionData, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil, false
	}

	parts := strings.SplitN(cookie.Value, ".", 2)
	if len(parts) != 2 {
		return nil, false
	}

	if !hmac.Equal([]byte(parts[1]), []byte(sign(parts[0]))) {
		return nil, false
	}

	raw, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}

	var data SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, false
	}

	if time.Now().Unix() > data.Exp {
		return nil, false
	}

	return &data, true
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func googleLoginHandler(w http.ResponseWriter, r *http.Request) {
	state := randomToken(16)
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
	})
	http.Redirect(w, r, googleOauthConfig.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

type googleUserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func googleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "Geçersiz state parametresi", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Kod bulunamadı", http.StatusBadRequest)
		return
	}

	token, err := googleOauthConfig.Exchange(context.Background(), code)
	if err != nil {
		fmt.Println("Token exchange hatası:", err)
		http.Error(w, "Google ile doğrulama başarısız", http.StatusInternalServerError)
		return
	}

	client := googleOauthConfig.Client(context.Background(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		http.Error(w, "Kullanıcı bilgisi alınamadı", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var userInfo googleUserInfo
	if err := json.Unmarshal(body, &userInfo); err != nil {
		http.Error(w, "Kullanıcı bilgisi işlenemedi", http.StatusInternalServerError)
		return
	}

	createSessionCookie(w, SessionData{
		Email:   userInfo.Email,
		Name:    userInfo.Name,
		Picture: userInfo.Picture,
	})

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	session, ok := getSession(r)
	if !ok {
		json.NewEncoder(w).Encode(map[string]interface{}{"loggedIn": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"loggedIn": true,
		"name":     session.Name,
		"email":    session.Email,
		"picture":  session.Picture,
	})
}

func createRoomHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	session, ok := getSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Oda kurmak için giriş yapmalısınız"})
		return
	}

	roomId := hex.EncodeToString([]byte(randomToken(6)))[:12]

	json.NewEncoder(w).Encode(map[string]string{
		"room":     roomId,
		"hostName": session.Name,
	})
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	session, loggedIn := getSession(r)
	var identity, displayName string
	if loggedIn {
		identity = session.Email
		displayName = session.Name
	} else {
		identity = "misafir-" + randomToken(4)
		displayName = identity
	}

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

				mutex.Lock()
				remaining := len(rooms[currentRoom])
				mutex.Unlock()
				if remaining == 0 {
					clearSessionID(currentRoom)
				}
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

			sessionId := getOrCreateSessionID(currentRoom)
			sessionInfo := struct {
				Type      string `json:"type"`
				Room      string `json:"room"`
				SessionId string `json:"sessionId"`
			}{Type: "session-info", Room: currentRoom, SessionId: sessionId}
			if sessionInfoPayload, err := json.Marshal(sessionInfo); err == nil {
				conn.WriteMessage(websocket.TextMessage, sessionInfoPayload)
			}

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
		if msg.Type == "chat" {
			var chatPayload struct {
				Kind     string `json:"kind"`
				Text     string `json:"text"`
				FileName string `json:"fileName"`
				FileUrl  string `json:"fileUrl"`
				Name     string `json:"name"`
			}
			if err := json.Unmarshal(msg.Payload, &chatPayload); err == nil {
				content := chatPayload.Text
				if chatPayload.Kind == "file" {
					content = fmt.Sprintf("[Dosya] %s (%s)", chatPayload.FileName, chatPayload.FileUrl)
				}
				sessionId := getOrCreateSessionID(msg.Room)
				forwardChatLog(msg.Room, sessionId, identity, displayName, content)
			}
		}

		broadcastToRoom(currentRoom, conn, rawMessage)

	}
}

var chatLogMutex sync.Mutex
var logHTTPClient = &http.Client{Timeout: 5 * time.Second}

type chatLogEntry struct {
	Room        string `json:"room"`
	SessionId   string `json:"sessionId"`
	Identity    string `json:"identity"`
	DisplayName string `json:"displayName"`
	Content     string `json:"content"`
	Timestamp   string `json:"timestamp"`
}

func forwardChatLog(room, sessionId, identity, displayName, content string) {
	entry := chatLogEntry{
		Room:        room,
		SessionId:   sessionId,
		Identity:    identity,
		DisplayName: displayName,
		Content:     content,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	logServerURL := os.Getenv("LOG_SERVER_URL")
	logSecret := os.Getenv("LOG_SHARED_SECRET")

	if logServerURL == "" || logSecret == "" {
		writeLocalChatLogFallback(entry)
		return
	}

	body, err := json.Marshal(entry)
	if err != nil {
		fmt.Println("Chat log JSON hatası:", err)
		writeLocalChatLogFallback(entry)
		return
	}

	go func() {
		req, err := http.NewRequest(http.MethodPost, logServerURL+"/api/chat-log", bytes.NewReader(body))
		if err != nil {
			fmt.Println("Log isteği oluşturulamadı:", err)
			writeLocalChatLogFallback(entry)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Log-Secret", logSecret)

		resp, err := logHTTPClient.Do(req)
		if err != nil {
			fmt.Println("Log sunucusuna ulaşılamadı, yerel yedeğe yazılıyor:", err)
			writeLocalChatLogFallback(entry)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			fmt.Println("Log sunucusu hata döndü, yerel yedeğe yazılıyor. Kod:", resp.StatusCode)
			writeLocalChatLogFallback(entry)
		}
	}()
}

func writeLocalChatLogFallback(entry chatLogEntry) {
	chatLogMutex.Lock()
	defer chatLogMutex.Unlock()

	os.MkdirAll("kayitlar", os.ModePerm)
	f, err := os.OpenFile("kayitlar/chat_log_fallback.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Yedek chat log dosyası açılamadı:", err)
		return
	}
	defer f.Close()

	line := fmt.Sprintf("[%s] Oda: %s | Oturum: %s | %s (%s): %s\n",
		entry.Timestamp, entry.Room, entry.SessionId, entry.DisplayName, entry.Identity, entry.Content)
	f.WriteString(line)
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

const maxUploadSize = 10 << 20 // 10 MB

func randomFileToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeForPath(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "oda"
	}
	return b.String()
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya çok büyük (maksimum 10 MB)"})
		return
	}

	room := r.FormValue("room")
	if room == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Oda bilgisi eksik"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya bulunamadı"})
		return
	}
	defer file.Close()

	safeRoom := sanitizeForPath(room)
	uploadDir := filepath.Join("public", "uploads", safeRoom)
	os.MkdirAll(uploadDir, os.ModePerm)

	ext := filepath.Ext(header.Filename)
	safeName := fmt.Sprintf("%d_%s%s", time.Now().UnixNano(), randomFileToken(4), ext)
	fullPath := filepath.Join(uploadDir, safeName)

	out, err := os.Create(fullPath)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya kaydedilemedi"})
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya yazılamadı"})
		return
	}

	fileUrl := fmt.Sprintf("/uploads/%s/%s", safeRoom, safeName)

	json.NewEncoder(w).Encode(map[string]string{
		"url":      fileUrl,
		"fileName": header.Filename,
		"fileType": header.Header.Get("Content-Type"),
	})
}

type RecordTokenClaims struct {
	Room      string `json:"room"`
	SessionId string `json:"sessionId"`
	Identity  string `json:"identity"`
	Name      string `json:"name"`
	Exp       int64  `json:"exp"`
}

func recordTokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	room := r.URL.Query().Get("room")
	if room == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "room parametresi eksik"})
		return
	}

	logSecret := os.Getenv("LOG_SHARED_SECRET")
	if logSecret == "" {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Sunucuda LOG_SHARED_SECRET tanımlı değil"})
		return
	}

	session, loggedIn := getSession(r)
	var identity, name string
	if loggedIn {
		identity = session.Email
		name = session.Name
	} else {
		identity = "misafir-" + randomToken(4)
		name = identity
	}

	sessionId := getOrCreateSessionID(room)

	claims := RecordTokenClaims{
		Room:      room,
		SessionId: sessionId,
		Identity:  identity,
		Name:      name,
		Exp:       time.Now().Add(2 * time.Hour).Unix(),
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Token oluşturulamadı"})
		return
	}
	encoded := base64.URLEncoding.EncodeToString(raw)

	mac := hmac.New(sha256.New, []byte(logSecret))
	mac.Write([]byte(encoded))
	signature := hex.EncodeToString(mac.Sum(nil))

	token := encoded + "." + signature

	json.NewEncoder(w).Encode(map[string]string{
		"token":     token,
		"sessionId": sessionId,
	})
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
	if err := godotenv.Load(); err != nil {
		fmt.Println("Uyarı: .env dosyası yüklenemedi (varsayılan ortam değişkenleri kullanılacak):", err)
	}

	googleOauthConfig = &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URL"),
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}

	fmt.Println("GOOGLE_CLIENT_ID yüklendi mi:", os.Getenv("GOOGLE_CLIENT_ID") != "")
	fmt.Println("SESSION_SECRET yüklendi mi:", os.Getenv("SESSION_SECRET") != "")

	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)

	http.HandleFunc("/ws", wsHandler)
	http.HandleFunc("/api/turn-credentials", turnCredentialsHandler)
	http.HandleFunc("/api/record-token", recordTokenHandler)
	http.HandleFunc("/api/record", recordHandler)
	http.HandleFunc("/api/upload", uploadHandler)

	http.HandleFunc("/auth/google/login", googleLoginHandler)
	http.HandleFunc("/auth/google/callback", googleCallbackHandler)
	http.HandleFunc("/auth/logout", logoutHandler)
	http.HandleFunc("/api/me", meHandler)
	http.HandleFunc("/api/create-room", createRoomHandler)

	fmt.Println("Sunucu 8080 portunda baslatılıyor...")

	http.ListenAndServe("0.0.0.0:8080", nil)
}
