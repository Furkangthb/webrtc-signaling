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

type appConfig struct {
	listenAddr     string
	publicHost     string
	publicOrigin   string
	cookieDomain   string
	cookieSecure   bool
	allowedOrigins map[string]bool
}

var cfg appConfig

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func loadConfig() {
	cfg.listenAddr = envOr("LISTEN_ADDR", "0.0.0.0:8080")
	cfg.publicHost = strings.TrimSpace(os.Getenv("PUBLIC_HOST"))
	cfg.publicOrigin = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_ORIGIN")), "/")
	if cfg.publicOrigin == "" && cfg.publicHost != "" {
		cfg.publicOrigin = "https://" + cfg.publicHost
	}
	if cfg.publicHost == "" && cfg.publicOrigin != "" {
		cfg.publicHost = strings.TrimPrefix(strings.TrimPrefix(cfg.publicOrigin, "https://"), "http://")
		if host, _, ok := strings.Cut(cfg.publicHost, "/"); ok {
			cfg.publicHost = host
		}
	}
	cfg.cookieDomain = strings.TrimSpace(os.Getenv("COOKIE_DOMAIN"))
	switch strings.ToLower(os.Getenv("COOKIE_SECURE")) {
	case "true", "1", "yes":
		cfg.cookieSecure = true
	case "false", "0", "no":
		cfg.cookieSecure = false
	default:
		cfg.cookieSecure = cfg.cookieDomain != "" || strings.HasPrefix(cfg.publicOrigin, "https://")
	}

	cfg.allowedOrigins = map[string]bool{
		"http://localhost:8080": true,
		"http://127.0.0.1:8080": true,
	}
	if cfg.publicOrigin != "" {
		cfg.allowedOrigins[cfg.publicOrigin] = true
	}
	for _, origin := range strings.Split(os.Getenv("ALLOWED_ORIGIN"), ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			cfg.allowedOrigins[origin] = true
		}
	}
}

func originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	return cfg.allowedOrigins[origin]
}

func applyCookieDefaults(c *http.Cookie) {
	if c.Path == "" {
		c.Path = "/"
	}
	c.HttpOnly = true
	c.Secure = cfg.cookieSecure
	c.SameSite = http.SameSiteLaxMode
	if cfg.cookieDomain != "" {
		c.Domain = cfg.cookieDomain
	}
}

var upgrade = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if originAllowed(origin) {
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

var validRooms = make(map[string]bool)
var validRoomsMutex sync.Mutex

func registerValidRoom(room string) {
	validRoomsMutex.Lock()
	defer validRoomsMutex.Unlock()
	validRooms[room] = true
}

func isValidRoom(room string) bool {
	validRoomsMutex.Lock()
	defer validRoomsMutex.Unlock()
	return validRooms[room]
}

func invalidateRoom(room string) {
	validRoomsMutex.Lock()
	defer validRoomsMutex.Unlock()
	delete(validRooms, room)
}

const roomGracePeriod = 30 * time.Second

var pendingRoomCleanup = make(map[string]*time.Timer)
var pendingCleanupMutex sync.Mutex

func scheduleRoomCleanup(room string) {
	pendingCleanupMutex.Lock()
	defer pendingCleanupMutex.Unlock()
	if existing, ok := pendingRoomCleanup[room]; ok {
		existing.Stop()
	}
	pendingRoomCleanup[room] = time.AfterFunc(roomGracePeriod, func() {
		mutex.Lock()
		stillEmpty := len(rooms[room]) == 0
		mutex.Unlock()
		if stillEmpty {
			clearSessionID(room)
			invalidateRoom(room)
			fmt.Println("Oda tolerans süresi doldu, geçersiz kılındı:", room)
		}
		pendingCleanupMutex.Lock()
		delete(pendingRoomCleanup, room)
		pendingCleanupMutex.Unlock()
	})
}

func cancelRoomCleanup(room string) {
	pendingCleanupMutex.Lock()
	defer pendingCleanupMutex.Unlock()
	if existing, ok := pendingRoomCleanup[room]; ok {
		existing.Stop()
		delete(pendingRoomCleanup, room)
	}
}

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
	expiration := time.Now().Add(7 * 24 * time.Hour)
	data.Exp = expiration.Unix()
	raw, _ := json.Marshal(data)
	encoded := base64.URLEncoding.EncodeToString(raw)
	value := encoded + "." + sign(encoded)

	cookie := &http.Cookie{
		Name:    "session",
		Value:   value,
		Expires: expiration,
		MaxAge:  7 * 24 * 3600,
	}
	applyCookieDefaults(cookie)
	http.SetCookie(w, cookie)
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
	expiration := time.Now().Add(10 * time.Minute)
	cookie := &http.Cookie{
		Name:    "oauth_state",
		Value:   state,
		Expires: expiration,
		MaxAge:  600,
	}
	applyCookieDefaults(cookie)
	http.SetCookie(w, cookie)
	http.Redirect(w, r, googleOauthConfig.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

type googleUserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func googleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "OAuth state çerezi bulunamadı. Tarayıcı çerezleri engelliyor olabilir.", http.StatusBadRequest)
		return
	}
	queryState := r.URL.Query().Get("state")
	if queryState == "" || queryState != stateCookie.Value {
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
	cookie := &http.Cookie{Name: "session", Value: "", MaxAge: -1, Expires: time.Unix(0, 0)}
	applyCookieDefaults(cookie)
	http.SetCookie(w, cookie)
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
	registerValidRoom(roomId)

	json.NewEncoder(w).Encode(map[string]string{
		"room":     roomId,
		"hostName": session.Name,
	})
}

type wsClient struct {
	conn        *websocket.Conn
	identity    string
	displayName string
	currentRoom string
	joinedRoom  bool
}

func (c *wsClient) handleDisconnect() {
	if !c.joinedRoom {
		return
	}
	msg := Message{Type: "peer-left", Room: c.currentRoom, Payload: nil}
	peerLeftPayload, marshalErr := json.Marshal(msg)
	if marshalErr == nil {
		broadcastToRoom(c.currentRoom, c.conn, peerLeftPayload)
	}
	removeFromRoom(c.currentRoom, c.conn)
	c.joinedRoom = false

	mutex.Lock()
	remaining := len(rooms[c.currentRoom])
	mutex.Unlock()
	if remaining == 0 {
		scheduleRoomCleanup(c.currentRoom)
	}
}

func (c *wsClient) handleJoin(msg Message) {
	if !isValidRoom(msg.Room) {
		invalidMessage := Message{Type: "room-not-found", Room: msg.Room, Payload: nil}
		invalidPayload, err := json.Marshal(invalidMessage)
		if err == nil {
			c.conn.WriteMessage(websocket.TextMessage, invalidPayload)
		}
		fmt.Println("Geçersiz oda ile katılma denemesi:", msg.Room)
		return
	}

	c.currentRoom = msg.Room
	cancelRoomCleanup(c.currentRoom)

	sessionId := getOrCreateSessionID(c.currentRoom)
	sessionInfo := struct {
		Type      string `json:"type"`
		Room      string `json:"room"`
		SessionId string `json:"sessionId"`
	}{Type: "session-info", Room: c.currentRoom, SessionId: sessionId}
	if sessionInfoPayload, err := json.Marshal(sessionInfo); err == nil {
		c.conn.WriteMessage(websocket.TextMessage, sessionInfoPayload)
	}

	mutex.Lock()
	roomSize := len(rooms[c.currentRoom])

	if roomSize >= 2 {
		mutex.Unlock()
		fullMessage := Message{Type: "room-full", Room: c.currentRoom, Payload: nil}
		fullPayload, err := json.Marshal(fullMessage)
		if err == nil {
			c.conn.WriteMessage(websocket.TextMessage, fullPayload)
		}
		return
	}

	if roomSize == 1 {
		readyMessage := Message{Type: "ready", Room: c.currentRoom, Payload: nil}
		readyPayload, err := json.Marshal(readyMessage)
		if err == nil {
			c.conn.WriteMessage(websocket.TextMessage, readyPayload)
		}
	}

	rooms[c.currentRoom] = append(rooms[c.currentRoom], c.conn)
	c.joinedRoom = true
	mutex.Unlock()
	fmt.Println("Client odaya katıldı", c.currentRoom)
}

func (c *wsClient) handleChat(msg Message) {
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
		forwardChatLog(msg.Room, sessionId, c.identity, c.displayName, content)
	}
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
	conn.SetReadLimit(64 * 1024)

	client := &wsClient{conn: conn, identity: identity, displayName: displayName}

	for {
		_, rawMessage, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("Client ayrıldı", err)
			client.handleDisconnect()
			break
		}

		var msg Message
		err = json.Unmarshal(rawMessage, &msg)
		if err != nil {
			fmt.Println("JSON parse hatası:", err)
			continue
		}

		if msg.Type == "join" {
			client.handleJoin(msg)
			continue
		}
		if msg.Type == "chat" {
			client.handleChat(msg)
		}

		broadcastToRoom(client.currentRoom, conn, rawMessage)
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

const maxUploadSize = 10 << 20

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

func verifyRecordToken(token string) (*RecordTokenClaims, bool) {
	logSecret := os.Getenv("LOG_SHARED_SECRET")
	if logSecret == "" || token == "" {
		return nil, false
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, false
	}
	encoded, signature := parts[0], parts[1]

	mac := hmac.New(sha256.New, []byte(logSecret))
	mac.Write([]byte(encoded))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return nil, false
	}

	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}

	var claims RecordTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, false
	}

	if time.Now().Unix() > claims.Exp {
		return nil, false
	}

	return &claims, true
}

var allowedUploadMimeTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"application/pdf": true,
	"text/plain":      true,
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token := r.URL.Query().Get("token")
	claims, ok := verifyRecordToken(token)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Geçersiz veya süresi dolmuş token"})
		return
	}
	room := claims.Room

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya çok büyük (maksimum 10 MB)"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya bulunamadı"})
		return
	}
	defer file.Close()

	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	detectedType := http.DetectContentType(sniff[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Dosya okunamadı"})
		return
	}

	if !allowedUploadMimeTypes[detectedType] {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		json.NewEncoder(w).Encode(map[string]string{"error": "Desteklenmeyen dosya türü: " + detectedType})
		return
	}

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
		"fileType": detectedType,
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

func iceServersPayload(username, credential string) []map[string]interface{} {
	host := cfg.publicHost
	if host == "" {
		host = "localhost"
	}
	servers := []map[string]interface{}{
		{"urls": []string{fmt.Sprintf("stun:%s:3478", host)}},
	}
	if username == "" || credential == "" {
		return servers
	}
	servers = append(servers, map[string]interface{}{
		"urls": []string{
			fmt.Sprintf("turn:%s:3478?transport=udp", host),
			fmt.Sprintf("turn:%s:3478?transport=tcp", host),
			fmt.Sprintf("turns:%s:5349?transport=tcp", host),
		},
		"username":   username,
		"credential": credential,
	})
	return servers
}

func turnCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" && !originAllowed(origin) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"success": false, "error": "Yetkisiz origin"}`))
		return
	}

	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Content-Type", "application/json")

	turnSecret := os.Getenv("TURN_SECRET")
	if turnSecret == "" {
		fmt.Println("HATA: TURN_SECRET ortam değişkeni bulunamadı!")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    false,
			"error":      "Sunucu yapılandırma hatası",
			"iceServers": iceServersPayload("", ""),
		})
		return
	}

	username, password := generateTurnCredentials(turnSecret, 86400)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"username":   username,
		"credential": password,
		"iceServers": iceServersPayload(username, password),
	})
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Uyarı: .env dosyası yüklenemedi (varsayılan ortam değişkenleri kullanılacak):", err)
	}
	loadConfig()

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
	fmt.Println("TURN_SECRET yüklendi mi:", os.Getenv("TURN_SECRET") != "")
	fmt.Println("Dinleme adresi:", cfg.listenAddr)

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("./public")))
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/ws", wsHandler)
	mux.HandleFunc("/api/turn-credentials", turnCredentialsHandler)
	mux.HandleFunc("/api/record-token", recordTokenHandler)
	mux.HandleFunc("/api/upload", uploadHandler)
	mux.HandleFunc("/auth/google/login", googleLoginHandler)
	mux.HandleFunc("/auth/google/callback", googleCallbackHandler)
	mux.HandleFunc("/auth/logout", logoutHandler)
	mux.HandleFunc("/api/me", meHandler)
	mux.HandleFunc("/api/create-room", createRoomHandler)

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Println("Sunucu başlatılıyor:", cfg.listenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Println("Sunucu hatası:", err)
		os.Exit(1)
	}
}
