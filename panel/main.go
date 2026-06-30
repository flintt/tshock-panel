package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var (
	apiURL         = getEnv("TSHOCK_API_URL", "http://terraria:7878")
	appToken       = getEnv("TSHOCK_APP_TOKEN", "opencode-panel-key-2024")
	port           = getEnv("PANEL_PORT", "4891")
	containerName  = getEnv("CONTAINER_NAME", "terraria-server")
	serverPort     = getEnv("SERVER_PORT", "7777")
	apiPort        = getEnv("API_PORT", "7878")
	networkName    = getEnv("NETWORK_NAME", "terraria-server_default")
	hostBasePath   = getEnv("HOST_BASE_PATH", "/root/terraria-server")
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

var (
	joinTimes = make(map[string]time.Time)
	joinMu    sync.RWMutex

	trailMaxPerPlayer = func() int { n, _ := strconv.Atoi(getEnv("TRAIL_MAX_PER_PLAYER", "1000")); if n < 10 { n = 10 }; return n }()
	trailDir          = getEnv("TRAIL_DIR", "/trails")
	playerTrails      = make(map[string]*trailData)
	trailMu           sync.RWMutex
)

type TrailPoint struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Time  int64   `json:"time"`
}

type trailData struct {
	points  []TrailPoint
	online  bool
	lastAdd time.Time
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

type Session struct {
	Token    string
	Username string
	Password string
}

var (
	sessions = make(map[string]*Session)
	mu       sync.RWMutex
)

func refreshToken(s *Session) error {
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func getSession(r *http.Request) *Session {
	c, err := r.Cookie("sid")
	if err != nil {
		return nil
	}
	mu.RLock()
	s := sessions[c.Value]
	mu.RUnlock()
	return s
}

// Docker API helper
func dockerRestartContainer(containerName string) error {
	// Use Docker API directly via socket
	socketPath := "/var/run/docker.sock"
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("cannot connect to docker socket: %v", err)
	}
	defer conn.Close()

	// Restart the container
	req := fmt.Sprintf("POST /containers/%s/restart HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n", containerName)
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("cannot send restart request: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("cannot read response: %v", err)
	}

	response := string(buf[:n])
	if !strings.Contains(response, "204") && !strings.Contains(response, "200") {
		return fmt.Errorf("docker restart failed: %s", response[:min(100, len(response))])
	}

	return nil
}

func recreateContainer(worldName string, size, difficulty int) error {
	// Stop, remove, and recreate the container with new world settings
	socketPath := "/var/run/docker.sock"
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("cannot connect to docker socket: %v", err)
	}
	defer conn.Close()

	// Stop the container
	stopReq := fmt.Sprintf("POST /containers/%s/stop HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n", containerName)
	_, err = conn.Write([]byte(stopReq))
	if err != nil {
		return fmt.Errorf("cannot send stop request: %v", err)
	}
	buf := make([]byte, 4096)
	conn.Read(buf) // Read response
	
	time.Sleep(2 * time.Second)
	
	// Remove the container
	removeReq := fmt.Sprintf("DELETE /containers/%s HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n", containerName)
	_, err = conn.Write([]byte(removeReq))
	if err != nil {
		return fmt.Errorf("cannot send remove request: %v", err)
	}
	conn.Read(buf) // Read response
	
	// Create new container with updated command
	tshockPath := hostBasePath + "/tshock"
	worldsPath := hostBasePath + "/worlds"
	pluginsPath := hostBasePath + "/plugins"
	presetsPath := hostBasePath + "/presets"
	
	createBody := fmt.Sprintf(`{
		"Image": "ghcr.io/pryaxis/tshock:stable",
		"Cmd": ["-world", "/worlds/%s.wld", "-autocreate", "%d", "-worldname", "%s", "-maxplayers", "8", "-difficulty", "%d"],
		"HostConfig": {
			"Binds": [
				"%s:/tshock",
				"%s:/worlds",
				"%s:/plugins",
				"%s:/presets",
				"/etc/localtime:/etc/localtime:ro"
			],
			"PortBindings": {
				"%s/tcp": [{"HostPort": "%s"}],
				"%s/tcp": [{"HostPort": "%s"}]
			},
			"RestartPolicy": {"Name": "unless-stopped"},
			"NetworkMode": "%s"
		},
		"NetworkConfig": {
			"%s": {}
		}
	}`, worldName, size, worldName, difficulty,
		tshockPath, worldsPath, pluginsPath, presetsPath,
		serverPort, serverPort, apiPort, apiPort,
		networkName, networkName)
	
	createReq := fmt.Sprintf("POST /containers/create?name=%s HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		containerName, len(createBody), createBody)
	_, err = conn.Write([]byte(createReq))
	if err != nil {
		return fmt.Errorf("cannot send create request: %v", err)
	}
	n, _ := conn.Read(buf)
	response := string(buf[:n])
	if !strings.Contains(response, "201") {
		return fmt.Errorf("container creation failed: %s", response[:min(200, len(response))])
	}
	
	// Start the new container
	time.Sleep(1 * time.Second)
	startReq := fmt.Sprintf("POST /containers/%s/start HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n", containerName)
	_, err = conn.Write([]byte(startReq))
	if err != nil {
		return fmt.Errorf("cannot send start request: %v", err)
	}
	conn.Read(buf) // Read response
	
	return nil
}

func dockerCreateWorld(name string, size, difficulty int) error {
	// First, update docker-compose.yml
	composePath := "/app/docker-compose.yml"
	data, err := os.ReadFile(composePath)
	if err != nil {
		return fmt.Errorf("cannot read docker-compose.yml: %v", err)
	}
	
	compose := string(data)
	lines := strings.Split(compose, "\n")
	for i, line := range lines {
		if strings.Contains(line, "command:") {
			lines[i] = fmt.Sprintf("    command: -world /worlds/%s.wld -autocreate %d -worldname \"%s\" -maxplayers 8 -difficulty %d",
				name, size, name, difficulty)
			break
		}
	}
	
	if err := os.WriteFile(composePath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("cannot write docker-compose.yml: %v", err)
	}
	
	// Recreate container
	return recreateContainer(name, size, difficulty)
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	// Validate TShock is reachable using the app token
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, appToken))
	if err != nil {
		http.Error(w, `{"error":"cannot connect"}`, 502)
		return
	}
	resp.Body.Close()

	id := generateID()
	mu.Lock()
	sessions[id] = &Session{Token: appToken, Username: body.Username, Password: body.Password}
	mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: id, Path: "/", SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": appToken})
}

func rawcmdHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	var body struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Cmd == "" {
		http.Error(w, `{"error":"no cmd"}`, 400)
		return
	}
	u := fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s",
		apiURL, s.Token, url.QueryEscape(body.Cmd))
	resp, err := http.Get(u)
	if err != nil {
		http.Error(w, `{"error":"request failed"}`, 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	// Try with current token
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		// Check if response is valid (not a token error)
		var check map[string]interface{}
		if json.Unmarshal(b, &check) == nil && check["status"] == "200" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
			return
		}
	}
	// Token might be expired, refresh it
	if err := refreshToken(s); err != nil {
		http.Error(w, `{"error":"token refresh failed"}`, 502)
		return
	}
	// Retry with new token
	resp, err = http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err != nil {
		http.Error(w, `{"error":"request failed"}`, 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	resp, err := http.Get(fmt.Sprintf("%s/users/list?token=%s", apiURL, s.Token))
	if err != nil {
		http.Error(w, `{"error":"request failed"}`, 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func bansHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	resp, err := http.Get(fmt.Sprintf("%s/bans/list?token=%s", apiURL, s.Token))
	if err != nil {
		http.Error(w, `{"error":"request failed"}`, 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func userCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
		Group    string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.User == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	u := fmt.Sprintf("%s/users/create?token=%s&user=%s&password=%s&group=%s",
		apiURL, s.Token, url.QueryEscape(body.User), url.QueryEscape(body.Password), url.QueryEscape(body.Group))
	resp, err := http.Get(u)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func userDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { User string `json:"user"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.User == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	u := fmt.Sprintf("%s/users/destroy?token=%s&user=%s", apiURL, s.Token, url.QueryEscape(body.User))
	req, _ := http.NewRequest("DELETE", u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func userUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
		Group    string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.User == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	u := fmt.Sprintf("%s/users/update?token=%s&user=%s&group=%s",
		apiURL, s.Token, url.QueryEscape(body.User), url.QueryEscape(body.Group))
	if body.Password != "" {
		u += "&password=" + url.QueryEscape(body.Password)
	}
	resp, err := http.Get(u)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func groupListHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	resp, err := http.Get(fmt.Sprintf("%s/groups/list?token=%s", apiURL, s.Token))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func groupCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct {
		Group       string `json:"group"`
		Permissions string `json:"permissions"`
		Parent      string `json:"parent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Group == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	u := fmt.Sprintf("%s/groups/create?token=%s&group=%s", apiURL, s.Token, url.QueryEscape(body.Group))
	if body.Permissions != "" {
		u += "&permissions=" + url.QueryEscape(body.Permissions)
	}
	if body.Parent != "" {
		u += "&parent=" + url.QueryEscape(body.Parent)
	}
	resp, err := http.Get(u)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func groupDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Group string `json:"group"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Group == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	u := fmt.Sprintf("%s/groups/destroy?token=%s&group=%s", apiURL, s.Token, url.QueryEscape(body.Group))
	resp, err := http.Get(u)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func groupReadHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	group := r.URL.Query().Get("group")
	if group == "" { http.Error(w, `{"error":"no group"}`, 400); return }
	resp, err := http.Get(fmt.Sprintf("%s/groups/read?token=%s&group=%s", apiURL, s.Token, url.QueryEscape(group)))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func playerInfoHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	player := r.URL.Query().Get("player")
	if player == "" {
		http.Error(w, `{"error":"no player"}`, 400)
		return
	}
	// Use v4 API for complete item data with prefixes
	resp, err := http.Get(fmt.Sprintf("%s/v4/players/read?token=%s&player=%s", apiURL, s.Token, url.QueryEscape(player)))
	if err != nil {
		http.Error(w, `{"error":"request failed"}`, 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func worldReadHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	resp, err := http.Get(fmt.Sprintf("%s/world/read?token=%s", apiURL, s.Token))
	if err != nil {
		http.Error(w, `{"error":"request failed"}`, 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func autosaveHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	enable := r.URL.Query().Get("enable")
	resp, err := http.Get(fmt.Sprintf("%s/world/autosave/state/%s?token=%s", apiURL, enable, s.Token))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func motdHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	resp, err := http.Get(fmt.Sprintf("%s/server/motd?token=%s", apiURL, s.Token))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func rulesHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	resp, err := http.Get(fmt.Sprintf("%s/server/rules?token=%s", apiURL, s.Token))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func bloodmoonHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	// Use v3 API for bloodmoon
	resp, err := http.Get(fmt.Sprintf("%s/v3/world/bloodmoon?token=%s", apiURL, s.Token))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func broadcastHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Msg string `json:"msg"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Msg == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	resp, err := http.Get(fmt.Sprintf("%s/server/broadcast?token=%s&msg=%s", apiURL, s.Token, url.QueryEscape(body.Msg)))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// TShock API helper with auto token refresh
func tshockGet(path string, s *Session) ([]byte, error) {
	// Try with current token
	resp, err := http.Get(fmt.Sprintf("%s%s?token=%s", apiURL, path, s.Token))
	if err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		// Check if response is valid
		var check map[string]interface{}
		if json.Unmarshal(b, &check) == nil {
			if status, ok := check["status"].(string); ok && status == "200" {
				return b, nil
			}
			// Check for token errors
			if errMsg, ok := check["error"].(string); ok && (strings.Contains(errMsg, "token") || strings.Contains(errMsg, "not authorized")) {
				// Token might be expired, try refresh
				if err := refreshToken(s); err != nil {
					return nil, err
				}
				// Retry with new token
				resp, err = http.Get(fmt.Sprintf("%s%s?token=%s", apiURL, path, s.Token))
				if err != nil {
					return nil, err
				}
				defer resp.Body.Close()
				return io.ReadAll(resp.Body)
			}
		}
		return b, nil
	}
	// Request failed, try token refresh
	if err := refreshToken(s); err != nil {
		return nil, err
	}
	// Retry with new token
	resp, err = http.Get(fmt.Sprintf("%s%s?token=%s", apiURL, path, s.Token))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func rawcmdProxy(cmd string, s *Session) (map[string]interface{}, error) {
	u := fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, s.Token, url.QueryEscape(cmd))
	resp, err := http.Get(u)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	return result, nil
}

// World operations
func worldHardmodeHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	result, err := rawcmdProxy("/hardmode", s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldModeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Mode string `json:"mode"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Mode == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	result, err := rawcmdProxy("/worldmode "+body.Mode, s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldSpawnrateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Rate int `json:"rate"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	result, err := rawcmdProxy(fmt.Sprintf("/spawnrate %d", body.Rate), s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldMaxspawnsHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	result, err := rawcmdProxy("/maxspawns", s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldSetspawnHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	result, err := rawcmdProxy("/setspawn", s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldProtectspawnHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	result, err := rawcmdProxy("/protectspawn", s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldAntibuildHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	result, err := rawcmdProxy("/antibuild", s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func worldButcherHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	// Use dedicated /v2/world/butcher endpoint
	resp, err := http.Get(fmt.Sprintf("%s/v2/world/butcher?token=%s&killfriendly=false", apiURL, s.Token))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// World management operations
func worldsListHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	currentWorld := ""
	composePath := "/app/docker-compose.yml"
	data, err := os.ReadFile(composePath)
	if err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "command:") && strings.Contains(line, "-world") {
				parts := strings.Fields(line)
				for i, p := range parts {
					if p == "-world" && i+1 < len(parts) {
						worldFile := filepath.Base(parts[i+1])
						currentWorld = strings.TrimSuffix(worldFile, ".wld")
						break
					}
				}
				break
			}
		}
	}
	
	internalName := ""
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var status map[string]interface{}
		json.Unmarshal(b, &status)
		if name, ok := status["world"].(string); ok {
			internalName = name
		}
	}
	
	worldDir := "/worlds"
	entries, err := os.ReadDir(worldDir)
	if err != nil { http.Error(w, `{"error":"cannot read worlds directory"}`, 500); return }
	
	type WorldInfo struct {
		Name         string `json:"name"`
		Size         int64  `json:"size"`
		ModTime      string `json:"modTime"`
		Active       bool   `json:"active"`
		InternalName string `json:"internalName,omitempty"`
	}
	
	var worlds []WorldInfo
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".wld") {
			info, _ := e.Info()
			worldName := strings.TrimSuffix(e.Name(), ".wld")
			wi := WorldInfo{
				Name:    worldName,
				Size:    info.Size(),
				ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
				Active:  worldName == currentWorld,
			}
			if internalName != "" && worldName == currentWorld && internalName != worldName {
				wi.InternalName = internalName
			}
			worlds = append(worlds, wi)
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "200",
		"worlds": worlds,
	})
}

func worldDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	// Check if world file exists
	worldPath := fmt.Sprintf("/worlds/%s.wld", body.Name)
	if _, err := os.Stat(worldPath); os.IsNotExist(err) {
		http.Error(w, `{"error":"world file not found"}`, 404); return
	}
	
	// Check if this is the current world
	currentWorld := ""
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var status map[string]interface{}
		json.Unmarshal(b, &status)
		if name, ok := status["world"].(string); ok {
			currentWorld = name
		}
	}
	
	if body.Name == currentWorld {
		http.Error(w, `{"error":"cannot delete current world"}`, 409); return
	}
	
	// Delete world file and backups
	os.Remove(worldPath)
	os.Remove(worldPath + ".bak")
	os.Remove(worldPath + ".bak2")
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": fmt.Sprintf("世界 %s 已删除", body.Name),
	})
}

func worldRenameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct {
		OldName string `json:"oldName"`
		NewName string `json:"newName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OldName == "" || body.NewName == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	// Check if old world file exists
	oldPath := fmt.Sprintf("/worlds/%s.wld", body.OldName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		http.Error(w, `{"error":"world file not found"}`, 404); return
	}
	
	// Check if new name already exists
	newPath := fmt.Sprintf("/worlds/%s.wld", body.NewName)
	if _, err := os.Stat(newPath); err == nil {
		http.Error(w, `{"error":"world name already exists"}`, 409); return
	}
	
	// Check if this is the current world
	currentWorld := ""
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var status map[string]interface{}
		json.Unmarshal(b, &status)
		if name, ok := status["world"].(string); ok {
			currentWorld = name
		}
	}
	
	// Rename world file
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, `{"error":"rename failed"}`, 500); return
	}
	
	// Rename backup files too (ignore if they don't exist)
	os.Rename(oldPath+".bak", newPath+".bak")
	os.Rename(oldPath+".bak2", newPath+".bak2")
	
	// If this is the current world, update docker-compose.yml and restart
	if body.OldName == currentWorld {
		composePath := "/app/docker-compose.yml"
		data, _ := os.ReadFile(composePath)
		compose := string(data)
		lines := strings.Split(compose, "\n")
		for i, line := range lines {
			if strings.Contains(line, "command:") {
				lines[i] = strings.ReplaceAll(lines[i], "/worlds/"+body.OldName+".wld", "/worlds/"+body.NewName+".wld")
				lines[i] = strings.ReplaceAll(lines[i], "\""+body.OldName+"\"", "\""+body.NewName+"\"")
				break
			}
		}
		os.WriteFile(composePath, []byte(strings.Join(lines, "\n")), 0644)
		
		go func() {
			time.Sleep(500 * time.Millisecond)
			recreateContainer(body.NewName, 0, 0)
		}()
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": fmt.Sprintf("世界 %s 已重命名为 %s", body.OldName, body.NewName),
	})
}

func worldSwitchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct {
		Name     string `json:"name"`
		Size     int    `json:"size,omitempty"`
		Difficulty int  `json:"difficulty,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	// Check if world file exists
	worldPath := fmt.Sprintf("/worlds/%s.wld", body.Name)
	if _, err := os.Stat(worldPath); os.IsNotExist(err) {
		http.Error(w, `{"error":"world file not found"}`, 404); return
	}
	
	// Read docker-compose.yml
	composePath := "/app/docker-compose.yml"
	data, err := os.ReadFile(composePath)
	if err != nil { http.Error(w, `{"error":"cannot read docker-compose.yml"}`, 500); return }
	
	// Update command line
	compose := string(data)
	lines := strings.Split(compose, "\n")
	for i, line := range lines {
		if strings.Contains(line, "command:") {
			lines[i] = fmt.Sprintf("    command: -world /worlds/%s.wld -autocreate %d -worldname \"%s\" -maxplayers 8 -difficulty %d",
				body.Name, body.Size, body.Name, body.Difficulty)
			break
		}
	}
	
	// Write back
	if err := os.WriteFile(composePath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		http.Error(w, `{"error":"cannot write docker-compose.yml"}`, 500); return
	}
	
	// Recreate container with new world
	go func() {
		time.Sleep(500 * time.Millisecond)
		recreateContainer(body.Name, body.Size, body.Difficulty)
	}()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": fmt.Sprintf("世界已切换到 %s，服务器正在重启...", body.Name),
	})
}

func worldCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct {
		Name       string `json:"name"`
		Size       int    `json:"size"`
		Difficulty int    `json:"difficulty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	if body.Size <= 0 { body.Size = 2 } // Default: medium
	if body.Difficulty < 0 || body.Difficulty > 3 { body.Difficulty = 0 }
	
	// Check if world already exists
	worldPath := fmt.Sprintf("/worlds/%s.wld", body.Name)
	if _, err := os.Stat(worldPath); err == nil {
		http.Error(w, `{"error":"world already exists"}`, 409); return
	}
	
	// Create world using shell script
	go func() {
		dockerCreateWorld(body.Name, body.Size, body.Difficulty)
	}()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": fmt.Sprintf("世界 %s 创建中，服务器正在重启...", body.Name),
	})
}

// Event operations
func worldBackupsListHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }

	worldName := r.URL.Query().Get("world")
	if worldName == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}

	backupDir := "/worldbackups"
	os.MkdirAll(backupDir, 0755)
	entries, err := os.ReadDir(backupDir)
	if err != nil { http.Error(w, `{"error":"cannot read backups directory"}`, 500); return }

	type BackupInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Timestamp string `json:"timestamp"`
		Size      int64  `json:"size"`
		Label     string `json:"label"`
	}

	var backups []BackupInfo
	prefix := worldName + "_"
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".meta.json")
		metaPath := filepath.Join(backupDir, e.Name())
		metaData, err := os.ReadFile(metaPath)
		if err != nil { continue }
		var meta struct {
			Name      string `json:"name"`
			Timestamp string `json:"timestamp"`
			Size      int64  `json:"size"`
			Label     string `json:"label"`
		}
		if err := json.Unmarshal(metaData, &meta); err != nil { continue }

		wldPath := filepath.Join(backupDir, id+".wld")
		var wldSize int64
		if info, err := os.Stat(wldPath); err == nil {
			wldSize = info.Size()
		}

		backups = append(backups, BackupInfo{
			ID:        id,
			Name:      meta.Name,
			Timestamp: meta.Timestamp,
			Size:      wldSize,
			Label:     meta.Label,
		})
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp > backups[j].Timestamp
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"backups": backups,
	})
}

func worldBackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		s := getSession(r)
		if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }

		var body struct {
			Name  string `json:"name"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, `{"error":"bad request"}`, 400); return
		}

		worldPath := fmt.Sprintf("/worlds/%s.wld", body.Name)
		if _, err := os.Stat(worldPath); os.IsNotExist(err) {
			http.Error(w, `{"error":"world file not found"}`, 404); return
		}

		rawcmdProxy("/save", s)
		time.Sleep(2 * time.Second)

		timestamp := time.Now().Format("20060102_150405")
		backupID := fmt.Sprintf("%s_%s", body.Name, timestamp)

		backupDir := "/worldbackups"
		os.MkdirAll(backupDir, 0755)

		src, err := os.Open(worldPath)
		if err != nil { http.Error(w, `{"error":"cannot read world file"}`, 500); return }
		defer src.Close()

		dstPath := filepath.Join(backupDir, backupID+".wld")
		dst, err := os.Create(dstPath)
		if err != nil { http.Error(w, `{"error":"cannot create backup file"}`, 500); return }
		defer dst.Close()

		copied, err := io.Copy(dst, src)
		if err != nil {
			os.Remove(dstPath)
			http.Error(w, `{"error":"cannot copy world file"}`, 500); return
		}

		meta := struct {
			Name      string `json:"name"`
			Timestamp string `json:"timestamp"`
			Size      int64  `json:"size"`
			Label     string `json:"label"`
		}{
			Name:      body.Name,
			Timestamp: time.Now().Format("2006-01-02 15:04:05"),
			Size:      copied,
			Label:     body.Label,
		}
		metaData, _ := json.Marshal(meta)
		os.WriteFile(filepath.Join(backupDir, backupID+".meta.json"), metaData, 0644)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "200",
			"message": "世界已备份",
			"id":      backupID,
		})
		return
	}

	if r.Method == "DELETE" {
		s := getSession(r)
		if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }

		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			http.Error(w, `{"error":"bad request"}`, 400); return
		}

		backupDir := "/worldbackups"
		wldPath := filepath.Join(backupDir, body.ID+".wld")
		metaPath := filepath.Join(backupDir, body.ID+".meta.json")

		if _, err := os.Stat(metaPath); os.IsNotExist(err) {
			http.Error(w, `{"error":"backup not found"}`, 404); return
		}

		os.Remove(wldPath)
		os.Remove(metaPath)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "200",
			"message": "备份已删除",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, 405)
}

func worldRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }

	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}

	backupDir := "/worldbackups"
	metaPath := filepath.Join(backupDir, body.ID+".meta.json")
	wldPath := filepath.Join(backupDir, body.ID+".wld")

	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		http.Error(w, `{"error":"backup not found"}`, 404); return
	}

	metaData, err := os.ReadFile(metaPath)
	if err != nil { http.Error(w, `{"error":"cannot read backup metadata"}`, 500); return }
	var meta struct {
		Name      string `json:"name"`
		Timestamp string `json:"timestamp"`
		Size      int64  `json:"size"`
		Label     string `json:"label"`
	}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		http.Error(w, `{"error":"invalid backup metadata"}`, 500); return
	}

	rawcmdProxy("/save", s)
	time.Sleep(2 * time.Second)

	worldPath := fmt.Sprintf("/worlds/%s.wld", meta.Name)
	src, err := os.Open(wldPath)
	if err != nil { http.Error(w, `{"error":"cannot read backup file"}`, 500); return }
	defer src.Close()

	dst, err := os.Create(worldPath)
	if err != nil { http.Error(w, `{"error":"cannot write world file"}`, 500); return }
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		http.Error(w, `{"error":"cannot restore world file"}`, 500); return
	}

	rawcmdProxy("/off", s)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": "世界已恢复，服务器将重启",
	})
}

func worldBackupLabelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }

	var body struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}

	backupDir := "/worldbackups"
	metaPath := filepath.Join(backupDir, body.ID+".meta.json")

	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		http.Error(w, `{"error":"backup not found"}`, 404); return
	}

	metaData, err := os.ReadFile(metaPath)
	if err != nil { http.Error(w, `{"error":"cannot read backup metadata"}`, 500); return }
	var meta struct {
		Name      string `json:"name"`
		Timestamp string `json:"timestamp"`
		Size      int64  `json:"size"`
		Label     string `json:"label"`
	}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		http.Error(w, `{"error":"invalid backup metadata"}`, 500); return
	}

	meta.Label = body.Label
	updatedData, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, updatedData, 0644); err != nil {
		http.Error(w, `{"error":"cannot update backup metadata"}`, 500); return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": "标签已更新",
	})
}

func eventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct {
		Event string `json:"event"`
		Type  string `json:"type,omitempty"`
		Wave  int    `json:"wave,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Event == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	cmd := "/worldevent " + body.Event
	if body.Type != "" { cmd += " " + body.Type }
	if body.Wave > 0 { cmd += fmt.Sprintf(" %d", body.Wave) }
	result, err := rawcmdProxy(cmd, s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Player operations
func playerHealHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Player string `json:"player"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Player == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	result, err := rawcmdProxy(fmt.Sprintf("/heal \"%s\"", body.Player), s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func playerKillHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Player string `json:"player"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Player == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	// Use dedicated /v2/players/kill endpoint
	resp, err := http.Get(fmt.Sprintf("%s/v2/players/kill?token=%s&player=%s", apiURL, s.Token, url.QueryEscape(body.Player)))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func playerKickHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Player string `json:"player"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Player == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	// Use dedicated /v2/players/kick endpoint
	resp, err := http.Get(fmt.Sprintf("%s/v2/players/kick?token=%s&player=%s", apiURL, s.Token, url.QueryEscape(body.Player)))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func playerBanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Player string `json:"player"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Player == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	// Use dedicated /v3/bans/create endpoint with identifier format
	resp, err := http.Get(fmt.Sprintf("%s/v3/bans/create?token=%s&identifier=%s", apiURL, s.Token, url.QueryEscape("Name:"+body.Player)))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func playerMuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Player string `json:"player"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Player == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	// Use dedicated /v2/players/mute endpoint
	resp, err := http.Get(fmt.Sprintf("%s/v2/players/mute?token=%s&player=%s", apiURL, s.Token, url.QueryEscape(body.Player)))
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func playerGiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct {
		Player string `json:"player"`
		Item   int    `json:"item"`
		Amount int    `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Player == "" || body.Item <= 0 {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	if body.Amount <= 0 { body.Amount = 1 }
	result, err := rawcmdProxy(fmt.Sprintf("/give %d \"%s\" %d", body.Item, body.Player, body.Amount), s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func playerTpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	cmd := "/tprest"
	if body.From != "" && body.To != "" {
		cmd = fmt.Sprintf("/tprest %s %s", body.From, body.To)
	} else if body.To != "" {
		cmd = fmt.Sprintf("/tprest %s", body.To)
	}
	result, err := rawcmdProxy(cmd, s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Test handler - runs comprehensive tests
func testHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }

	type TestResult struct {
		Name    string `json:"name"`
		Passed  bool   `json:"passed"`
		Message string `json:"message"`
	}

	var results []TestResult
	passed := 0
	failed := 0

	runTest := func(name string, testFunc func() (bool, string)) {
		ok, msg := testFunc()
		results = append(results, TestResult{Name: name, Passed: ok, Message: msg})
		if ok { passed++ } else { failed++ }
	}

	// Test server status
	runTest("服务器状态", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" { return true, "正常" }
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test world read
	runTest("世界信息", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/world/read?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" {
			return true, fmt.Sprintf("%v", result["name"])
		}
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test users list
	runTest("用户列表", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/users/list?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" {
			users := result["users"].([]interface{})
			return true, fmt.Sprintf("%d 个用户", len(users))
		}
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test groups list
	runTest("分组列表", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/groups/list?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" {
			groups := result["groups"].([]interface{})
			return true, fmt.Sprintf("%d 个分组", len(groups))
		}
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test MOTD
	runTest("MOTD", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/server/motd?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" { return true, "正常" }
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test broadcast
	runTest("广播功能", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/server/broadcast?token=%s&msg=面板测试", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" { return true, "发送成功" }
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test rawcmd
	runTest("命令执行", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, s.Token, "/playing"))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" { return true, "正常" }
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test world mode
	runTest("世界模式", func() (bool, string) {
		result, err := rawcmdProxy("/worldmode expert", s)
		if err != nil { return false, "请求失败" }
		if result["status"] == "200" { return true, "设置成功" }
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test bloodmoon
	runTest("血月状态", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/v3/world/bloodmoon?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" { return true, fmt.Sprintf("%v", result["response"]) }
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test player list
	runTest("玩家列表", func() (bool, string) {
		resp, err := http.Get(fmt.Sprintf("%s/v2/players/list?token=%s", apiURL, s.Token))
		if err != nil { return false, "请求失败" }
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(b, &result)
		if result["status"] == "200" {
			players := result["players"].([]interface{})
			return true, fmt.Sprintf("%d 个玩家在线", len(players))
		}
		return false, fmt.Sprintf("%v", result["error"])
	})

	// Test log file access
	runTest("日志文件", func() (bool, string) {
		logDir := "/tshock/logs"
		entries, err := os.ReadDir(logDir)
		if err != nil { return false, "无法读取日志目录" }
		count := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") { count++ }
		}
		return true, fmt.Sprintf("%d 个日志文件", count)
	})

	// Check for errors in logs
	runTest("日志检查", func() (bool, string) {
		logDir := "/tshock/logs"
		entries, _ := os.ReadDir(logDir)
		var latest string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
				latest = e.Name()
			}
		}
		if latest == "" { return true, "无日志文件" }
		
		data, err := os.ReadFile(filepath.Join(logDir, latest))
		if err != nil { return false, "无法读取日志" }
		
		lines := strings.Split(string(data), "\n")
		errorCount := 0
		for _, line := range lines {
			// Ignore TShock update check errors
			if strings.Contains(line, "ERROR") && 
			   !strings.Contains(line, "UpdateManager") && 
			   !strings.Contains(line, "CheckForUpdates") &&
			   !strings.Contains(line, "Retrying in") {
				errorCount++
			}
		}
		
		if errorCount > 0 {
			return false, fmt.Sprintf("%d 个错误", errorCount)
		}
		return true, "无异常错误"
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"passed":  passed,
		"failed":  failed,
		"total":   passed + failed,
		"results": results,
	})
}

// Boss/Mob operations
func spawnBossHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Boss string `json:"boss"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Boss == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	result, err := rawcmdProxy(fmt.Sprintf("/spawnboss %s", body.Boss), s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func spawnMobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Mob string `json:"mob"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Mob == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	result, err := rawcmdProxy(fmt.Sprintf("/spawnmob %s", body.Mob), s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Time operations
func timeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	var body struct { Time string `json:"time"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Time == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	result, err := rawcmdProxy(fmt.Sprintf("/time %s", body.Time), s)
	if err != nil { http.Error(w, `{"error":"request failed"}`, 502); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	logDir := "/tshock/logs"
	entries, err := os.ReadDir(logDir)
	if err != nil { http.Error(w, `{"error":"cannot read log dir"}`, 500); return }
	var logFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			logFiles = append(logFiles, e.Name())
		}
	}
	if len(logFiles) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"logs":[]}`))
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(logFiles)))
	latest := filepath.Join(logDir, logFiles[0])
	data, err := os.ReadFile(latest)
	if err != nil { http.Error(w, `{"error":"cannot read log"}`, 500); return }
	lines := strings.Split(string(data), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": lines})
}

func logsStreamHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	flusher, ok := w.(http.Flusher)
	if !ok { http.Error(w, `{"error":"streaming not supported"}`, 500); return }
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	logDir := "/tshock/logs"
	var lastSize int64
	// Get latest log file
	getLatest := func() string {
		entries, _ := os.ReadDir(logDir)
		var logFiles []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
				logFiles = append(logFiles, e.Name())
			}
		}
		if len(logFiles) == 0 { return "" }
		sort.Sort(sort.Reverse(sort.StringSlice(logFiles)))
		return filepath.Join(logDir, logFiles[0])
	}
	// Send initial content
	latest := getLatest()
	if latest != "" {
		data, _ := os.ReadFile(latest)
		lines := strings.Split(string(data), "\n")
		if len(lines) > 50 { lines = lines[len(lines)-50:] }
		for _, l := range lines {
			if l != "" {
				fmt.Fprintf(w, "data: %s\n\n", l)
			}
		}
		info, _ := os.Stat(latest)
		if info != nil { lastSize = info.Size() }
	}
	flusher.Flush()
	// Poll for new content
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			latest = getLatest()
			if latest == "" { continue }
			info, err := os.Stat(latest)
			if err != nil { continue }
			if info.Size() > lastSize {
				data, _ := os.ReadFile(latest)
				content := string(data[lastSize:])
				lastSize = info.Size()
				for _, line := range strings.Split(content, "\n") {
					if line != "" {
						fmt.Fprintf(w, "data: %s\n\n", line)
					}
				}
				flusher.Flush()
			} else if info.Size() < lastSize {
				// Log file rotated
				lastSize = 0
			}
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	// Get session from cookie
	cookie, err := r.Cookie("sid")
	if err != nil { http.Error(w, "unauthorized", 401); return }
	mu.RLock()
	s, ok := sessions[cookie.Value]
	mu.RUnlock()
	if !ok { http.Error(w, "unauthorized", 401); return }

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil { log.Println("WebSocket upgrade failed:", err); return }
	defer conn.Close()

	var writeMu sync.Mutex
	writeJSON := func(msg WSMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(msg)
	}

	// Start log streaming goroutine
	logDone := make(chan struct{})
	go streamLogs(conn, s, logDone, writeJSON)

	// Start status update goroutine
	statusDone := make(chan struct{})
	go streamStatus(conn, s, statusDone, writeJSON)

	// Read messages from client
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var wsMsg WSMessage
		if err := json.Unmarshal(msg, &wsMsg); err != nil {
			writeJSON(WSMessage{Type: "error", Payload: "invalid message format"})
			continue
		}

		switch wsMsg.Type {
		case "cmd":
			handleWSCommand(conn, s, wsMsg.Payload, writeJSON)
		case "status":
			handleWSStatus(conn, s, writeJSON)
		case "players":
			handleWSPlayers(conn, s, writeJSON)
		case "player_info":
			handleWSPlayerInfo(s, wsMsg.Payload, writeJSON)
		case "player_tp":
			handleWSPlayerTp(s, wsMsg.Payload, writeJSON)
		default:
			writeJSON(WSMessage{Type: "error", Payload: "unknown message type"})
		}
	}

	close(logDone)
	close(statusDone)
}

func handleWSCommand(conn *websocket.Conn, s *Session, payload interface{}, writeJSON func(WSMessage) error) {
	cmd, ok := payload.(string)
	if !ok {
		writeJSON(WSMessage{Type: "error", Payload: "invalid command"})
		return
	}

	resp, err := http.Get(fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, s.Token, url.QueryEscape(cmd)))
	if err != nil {
		writeJSON(WSMessage{Type: "error", Payload: "request failed"})
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	json.Unmarshal(b, &result)
	writeJSON(WSMessage{Type: "cmd_result", Payload: result})
}

func handleWSStatus(conn *websocket.Conn, s *Session, writeJSON func(WSMessage) error) {
	resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
	if err != nil { return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	writeJSON(WSMessage{Type: "status", Payload: result})
}

func handleWSPlayers(conn *websocket.Conn, s *Session, writeJSON func(WSMessage) error) {
	resp, err := http.Get(fmt.Sprintf("%s/v2/players/list?token=%s", apiURL, s.Token))
	if err != nil { return }
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	writeJSON(WSMessage{Type: "players", Payload: result})
}

func handleWSPlayerInfo(s *Session, payload interface{}, writeJSON func(WSMessage) error) {
	name, _ := payload.(string)
	if name == "" {
		m, ok := payload.(map[string]interface{})
		if ok { name, _ = m["player"].(string) }
	}
	if name == "" { return }
	
	resp, err := http.Get(fmt.Sprintf("%s/v3/players/read?token=%s&player=%s", apiURL, s.Token, url.QueryEscape(name)))
	if err != nil { return }
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var pInfo map[string]interface{}
	json.Unmarshal(b, &pInfo)

	count := trailMaxPerPlayer
	trailMu.RLock()
	td, ok := playerTrails[name]
	var trail []TrailPoint
	if ok {
		trail = td.points
		if len(trail) > count {
			trail = trail[len(trail)-count:]
		}
	}
	trailMu.RUnlock()

	if trail == nil { trail = []TrailPoint{} }

	writeJSON(WSMessage{Type: "player_info", Payload: map[string]interface{}{
		"player": name,
		"info":   pInfo,
		"trail":  trail,
	}})
}

func handleWSPlayerTp(s *Session, payload interface{}, writeJSON func(WSMessage) error) {
	m, ok := payload.(map[string]interface{})
	if !ok {
		writeJSON(WSMessage{Type: "player_tp", Payload: map[string]interface{}{"error": "invalid payload"}})
		return
	}

	from, _ := m["from"].(string)
	to, _ := m["to"].(string)
	if to == "" {
		writeJSON(WSMessage{Type: "player_tp", Payload: map[string]interface{}{"from": from, "to": to, "error": "target player required"}})
		return
	}

	var cmd string
	if from == "" {
		cmd = fmt.Sprintf("/tprest %s", url.QueryEscape(to))
	} else {
		cmd = fmt.Sprintf("/tprest %s %s", url.QueryEscape(from), url.QueryEscape(to))
	}

	resp, err := http.Get(fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, s.Token, url.QueryEscape(cmd)))
	if err != nil {
		writeJSON(WSMessage{Type: "player_tp", Payload: map[string]interface{}{"from": from, "to": to, "error": "request failed"}})
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	json.Unmarshal(b, &result)

	writeJSON(WSMessage{Type: "player_tp", Payload: map[string]interface{}{
		"from":   from,
		"to":     to,
		"result": result,
	}})
}

func streamLogs(conn *websocket.Conn, s *Session, done chan struct{}, writeJSON func(WSMessage) error) {
	logDir := "/tshock/logs"
	var lastSize int64

	getLatest := func() string {
		entries, _ := os.ReadDir(logDir)
		var logFiles []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
				logFiles = append(logFiles, e.Name())
			}
		}
		if len(logFiles) == 0 { return "" }
		sort.Sort(sort.Reverse(sort.StringSlice(logFiles)))
		return filepath.Join(logDir, logFiles[0])
	}

	parsePlayerEvent := func(line string) (string, string) {
		if !strings.Contains(line, "Broadcast:") {
			return "", ""
		}
		bcIdx := strings.Index(line, "Broadcast:")
		afterBC := line[bcIdx+len("Broadcast:"):]
		afterBC = strings.TrimSpace(afterBC)
		if strings.Contains(afterBC, " has joined.") {
			idx := strings.Index(afterBC, " has joined.")
			name := strings.TrimSpace(afterBC[:idx])
			if name != "" {
				parenIdx := strings.Index(name, " (")
				if parenIdx > 0 {
					name = name[:parenIdx]
				}
				if name != "" {
					return name, "join"
				}
			}
		}
		if strings.Contains(afterBC, " has left.") {
			idx := strings.Index(afterBC, " has left.")
			name := strings.TrimSpace(afterBC[:idx])
			if name != "" {
				return name, "leave"
			}
		}
		return "", ""
	}

	processLine := func(line string) {
		if line == "" { return }
		writeJSON(WSMessage{Type: "log", Payload: line})
		name, action := parsePlayerEvent(line)
		if name == "" { return }
		if action == "join" {
			joinMu.Lock()
			if _, exists := joinTimes[name]; !exists {
				joinTimes[name] = time.Now()
			}
			joinMu.Unlock()
			writeJSON(WSMessage{Type: "player_join", Payload: map[string]interface{}{"name": name}})
		} else if action == "leave" {
			joinMu.Lock()
			delete(joinTimes, name)
			joinMu.Unlock()
			writeJSON(WSMessage{Type: "player_leave", Payload: map[string]interface{}{"name": name}})
		}
	}

	// Send initial logs
	latest := getLatest()
	if latest != "" {
		data, _ := os.ReadFile(latest)
		lines := strings.Split(string(data), "\n")
		if len(lines) > 50 { lines = lines[len(lines)-50:] }
		for _, l := range lines {
			processLine(l)
		}
		info, _ := os.Stat(latest)
		if info != nil { lastSize = info.Size() }
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			latest = getLatest()
			if latest == "" { continue }
			info, err := os.Stat(latest)
			if err != nil { continue }
				if info.Size() > lastSize {
					data, _ := os.ReadFile(latest)
					content := string(data[lastSize:])
					lastSize = info.Size()
					for _, line := range strings.Split(content, "\n") {
						processLine(line)
					}
				} else if info.Size() < lastSize {
				lastSize = 0
			}
		}
	}
}

func streamStatus(conn *websocket.Conn, s *Session, done chan struct{}, writeJSON func(WSMessage) error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Get server status
			resp, err := http.Get(fmt.Sprintf("%s/status?token=%s", apiURL, s.Token))
			if err != nil { log.Printf("streamStatus status error: %v", err); continue }
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var status map[string]interface{}
			json.Unmarshal(b, &status)

			// Get world read
			resp2, err := http.Get(fmt.Sprintf("%s/world/read?token=%s", apiURL, s.Token))
			if err != nil { log.Printf("streamStatus world/read error: %v", err); continue }
			b2, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()
			var world map[string]interface{}
			json.Unmarshal(b2, &world)

			// Get world info for mode
			resp3, err := http.Get(fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, s.Token, "/worldinfo"))
			if err == nil {
				b3, _ := io.ReadAll(resp3.Body)
				resp3.Body.Close()
				var worldInfo map[string]interface{}
				json.Unmarshal(b3, &worldInfo)
				if resp, ok := worldInfo["response"].([]interface{}); ok {
					for _, line := range resp {
						if str, ok := line.(string); ok {
							if strings.Contains(str, "Mode:") {
								world["mode"] = strings.TrimSpace(strings.Split(str, ":")[1])
							}
						}
					}
				}
			}

			// Get player list
			var players []map[string]interface{}
			resp4, err := http.Get(fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, s.Token, url.QueryEscape("/playing")))
			if err == nil {
				b4, _ := io.ReadAll(resp4.Body)
				resp4.Body.Close()
				var pcmd map[string]interface{}
				json.Unmarshal(b4, &pcmd)
				if resp, ok := pcmd["response"].([]interface{}); ok {
					for _, line := range resp {
						if str, ok := line.(string); ok {
							if strings.Contains(str, "Online Players") || strings.Contains(str, "no players") { continue }
							for _, name := range strings.Split(str, ",") {
								name = strings.TrimSpace(name)
								if name == "" { continue }
								players = append(players, map[string]interface{}{"nickname": name, "active": true})
							}
						}
					}
				}
			}

			joinMu.Lock()
			for _, pm := range players {
				nickname, _ := pm["nickname"].(string)
				if nickname == "" { continue }
				if _, exists := joinTimes[nickname]; !exists {
					joinTimes[nickname] = time.Now()
				}
			}
			knownPlayers := make(map[string]bool)
			for _, pm := range players {
				nickname, _ := pm["nickname"].(string)
				if nickname != "" { knownPlayers[nickname] = true }
			}
			for name := range joinTimes {
				if !knownPlayers[name] { delete(joinTimes, name) }
			}
			joinMu.Unlock()

			joinMu.RLock()
			joinData := make(map[string]float64)
			for name, t := range joinTimes {
				joinData[name] = time.Since(t).Seconds()
			}
			joinMu.RUnlock()
			if err := writeJSON(WSMessage{Type: "status_update", Payload: map[string]interface{}{
				"server":    status,
				"world":     world,
				"players":   players,
				"joinTimes": joinData,
			}}); err != nil {
				log.Printf("streamStatus WriteJSON error: %v", err)
			}
		}
	}
}

func playerJoinTimesHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	joinMu.RLock()
	data := make(map[string]float64)
	for name, t := range joinTimes {
		data[name] = time.Since(t).Seconds()
	}
	joinMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "joinTimes": data})
}

func playerTrailHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	name := r.URL.Query().Get("player")
	if name == "" {
		http.Error(w, `{"error":"missing player parameter"}`, 400)
		return
	}
	count := trailMaxPerPlayer
	if c := r.URL.Query().Get("count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil {
			if n <= 0 {
				count = trailMaxPerPlayer
			} else if n <= trailMaxPerPlayer {
				count = n
			}
		}
	}
	trailMu.RLock()
	td, ok := playerTrails[name]
	if !ok {
		trailMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "trail": []TrailPoint{}})
		return
	}
	trail := td.points
	start := 0
	if len(trail) > count {
		start = len(trail) - count
	}
	result := make([]TrailPoint, len(trail)-start)
	copy(result, trail[start:])
	trailMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "trail": result})
}

func playerTrailClearHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	var body struct {
		Player string `json:"player"`
		Scope  string `json:"scope"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	trailMu.Lock()
	if body.Player != "" {
		if body.Scope == "offline" {
			if td, ok := playerTrails[body.Player]; ok && !td.online {
				delete(playerTrails, body.Player)
			}
		} else {
			delete(playerTrails, body.Player)
		}
	} else {
		if body.Scope == "offline" {
			for name, td := range playerTrails {
				if !td.online { delete(playerTrails, name) }
			}
		} else {
			playerTrails = make(map[string]*trailData)
		}
	}
	trailMu.Unlock()
	go saveTrails()
	go trailCleanupFiles()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "message": "cleared"})
}

type Preset struct {
	Name      string   `json:"name"`
	Items     []string `json:"items"`     // List of item IDs or names
	CreatedAt string   `json:"createdAt"`
}

func getPresetDir() string {
	return "/presets"
}

func presetsListHandler(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	presetDir := getPresetDir()
	entries, err := os.ReadDir(presetDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "presets": []Preset{}})
		return
	}
	
	var presets []Preset
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(presetDir, e.Name()))
			if err != nil { continue }
			var p Preset
			if json.Unmarshal(data, &p) == nil {
				presets = append(presets, p)
			}
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "presets": presets})
}

func presetCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body Preset
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	presetDir := getPresetDir()
	os.MkdirAll(presetDir, 0755)
	
	body.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	data, _ := json.MarshalIndent(body, "", "  ")
	
	filename := filepath.Join(presetDir, body.Name+".json")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		http.Error(w, `{"error":"cannot save preset"}`, 500); return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "message": "预设已保存"})
}

func presetUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct {
		Preset
		OriginalName string `json:"originalName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	presetDir := getPresetDir()
	filename := filepath.Join(presetDir, body.Name+".json")
	
	lookupName := body.Name
	if body.OriginalName != "" {
		lookupName = body.OriginalName
	}
	var existing Preset
	data, _ := os.ReadFile(filepath.Join(presetDir, lookupName+".json"))
	json.Unmarshal(data, &existing)
	if existing.CreatedAt != "" {
		body.CreatedAt = existing.CreatedAt
	} else {
		body.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	
	data, _ = json.MarshalIndent(body.Preset, "", "  ")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		http.Error(w, `{"error":"cannot save preset"}`, 500); return
	}
	
	if body.OriginalName != "" && body.OriginalName != body.Name {
		os.Remove(filepath.Join(presetDir, body.OriginalName+".json"))
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "message": "预设已更新"})
}

func presetDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct { Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	presetDir := getPresetDir()
	filename := filepath.Join(presetDir, body.Name+".json")
	if err := os.Remove(filename); err != nil {
		http.Error(w, `{"error":"cannot delete preset"}`, 500); return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "200", "message": "预设已删除"})
}

func presetApplyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, `{"error":"method not allowed"}`, 405); return }
	s := getSession(r)
	if s == nil { http.Error(w, `{"error":"unauthorized"}`, 401); return }
	
	var body struct {
		PresetName string `json:"presetName"`
		Player     string `json:"player"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PresetName == "" || body.Player == "" {
		http.Error(w, `{"error":"bad request"}`, 400); return
	}
	
	// Load preset
	presetDir := getPresetDir()
	filename := filepath.Join(presetDir, body.PresetName+".json")
	data, err := os.ReadFile(filename)
	if err != nil {
		http.Error(w, `{"error":"preset not found"}`, 404); return
	}
	
	var preset Preset
	if err := json.Unmarshal(data, &preset); err != nil {
		http.Error(w, `{"error":"invalid preset"}`, 500); return
	}
	
	// Give each item to the player
	var results []string
	for _, item := range preset.Items {
		parts := strings.SplitN(item, ":", 3)
		itemID := parts[0]
		prefix := "0"
		count := "1"
		if len(parts) >= 2 { prefix = parts[1] }
		if len(parts) >= 3 { count = parts[2] }
		cmd := fmt.Sprintf("/give %s \"%s\" %s %s", itemID, body.Player, count, prefix)
		result, err := rawcmdProxy(cmd, s)
		if err != nil {
			results = append(results, fmt.Sprintf("%s: error", item))
		} else {
			if resp, ok := result["response"].([]interface{}); ok && len(resp) > 0 {
				results = append(results, fmt.Sprintf("%s: %v", item, resp[0]))
			} else {
				results = append(results, fmt.Sprintf("%s: ok", item))
			}
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "200",
		"message": fmt.Sprintf("已应用预设到 %s", body.Player),
		"results": results,
	})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/check", func(w http.ResponseWriter, r *http.Request) {
		if getSession(r) != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]bool{"ok": false})
		}
	})
	mux.HandleFunc("/api/login", loginHandler)
	mux.HandleFunc("/api/rawcmd", rawcmdHandler)
	mux.HandleFunc("/api/status", statusHandler)
	mux.HandleFunc("/api/users", usersHandler)
	mux.HandleFunc("/api/bans", bansHandler)
	mux.HandleFunc("/api/playerinfo", playerInfoHandler)
	mux.HandleFunc("/api/player/jointimes", playerJoinTimesHandler)
	mux.HandleFunc("/api/player/trail", playerTrailHandler)
	mux.HandleFunc("/api/worldread", worldReadHandler)
	mux.HandleFunc("/api/usercreate", userCreateHandler)
	mux.HandleFunc("/api/userdelete", userDeleteHandler)
	mux.HandleFunc("/api/userupdate", userUpdateHandler)
	mux.HandleFunc("/api/grouplist", groupListHandler)
	mux.HandleFunc("/api/groupcreate", groupCreateHandler)
	mux.HandleFunc("/api/groupdelete", groupDeleteHandler)
	mux.HandleFunc("/api/groupread", groupReadHandler)
	mux.HandleFunc("/api/autosave", autosaveHandler)
	mux.HandleFunc("/api/motd", motdHandler)
	mux.HandleFunc("/api/rules", rulesHandler)
	mux.HandleFunc("/api/bloodmoon", bloodmoonHandler)
	mux.HandleFunc("/api/broadcast", broadcastHandler)
	mux.HandleFunc("/api/logs", logsHandler)
	mux.HandleFunc("/api/logs/stream", logsStreamHandler)
	mux.HandleFunc("/api/ws", wsHandler)
	// RESTful API endpoints
	mux.HandleFunc("/api/world/hardmode", worldHardmodeHandler)
	mux.HandleFunc("/api/world/mode", worldModeHandler)
	mux.HandleFunc("/api/world/spawnrate", worldSpawnrateHandler)
	mux.HandleFunc("/api/world/maxspawns", worldMaxspawnsHandler)
	mux.HandleFunc("/api/world/setspawn", worldSetspawnHandler)
	mux.HandleFunc("/api/world/protectspawn", worldProtectspawnHandler)
	mux.HandleFunc("/api/world/antibuild", worldAntibuildHandler)
	mux.HandleFunc("/api/world/butcher", worldButcherHandler)
	mux.HandleFunc("/api/worlds", worldsListHandler)
	mux.HandleFunc("/api/world/switch", worldSwitchHandler)
	mux.HandleFunc("/api/world/delete", worldDeleteHandler)
	mux.HandleFunc("/api/world/rename", worldRenameHandler)
	mux.HandleFunc("/api/world/create", worldCreateHandler)
	mux.HandleFunc("/api/world/backups", worldBackupsListHandler)
	mux.HandleFunc("/api/world/backup", worldBackupHandler)
	mux.HandleFunc("/api/world/restore", worldRestoreHandler)
	mux.HandleFunc("/api/world/backup/label", worldBackupLabelHandler)
	mux.HandleFunc("/api/event", eventHandler)
	mux.HandleFunc("/api/player/heal", playerHealHandler)
	mux.HandleFunc("/api/player/kill", playerKillHandler)
	mux.HandleFunc("/api/player/kick", playerKickHandler)
	mux.HandleFunc("/api/player/ban", playerBanHandler)
	mux.HandleFunc("/api/player/mute", playerMuteHandler)
	mux.HandleFunc("/api/player/give", playerGiveHandler)
	mux.HandleFunc("/api/player/tp", playerTpHandler)
	mux.HandleFunc("/api/player/trail/clear", playerTrailClearHandler)
	mux.HandleFunc("/api/spawn/boss", spawnBossHandler)
	mux.HandleFunc("/api/spawn/mob", spawnMobHandler)
	mux.HandleFunc("/api/time", timeHandler)
	mux.HandleFunc("/api/test", testHandler)
	// Preset endpoints
	mux.HandleFunc("/api/presets", presetsListHandler)
	mux.HandleFunc("/api/preset/create", presetCreateHandler)
	mux.HandleFunc("/api/preset/update", presetUpdateHandler)
	mux.HandleFunc("/api/preset/delete", presetDeleteHandler)
	mux.HandleFunc("/api/preset/apply", presetApplyHandler)

	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		s := getSession(r)
		if s == nil { w.WriteHeader(204); return }
		http.Get(fmt.Sprintf("%s/token/destroy/%s", apiURL, s.Token))
		mu.Lock()
		delete(sessions, r.Header.Get("Cookie"))
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", Path: "/", MaxAge: -1})
		w.WriteHeader(204)
	})
	mux.Handle("/", http.FileServer(http.Dir("static")))
	log.Printf("Panel listening on :%s", port)
	loadTrails()
	go trackPlayerJoinTimes()
	go trackPlayerTrails()
	go trailSaveLoop()
	srv := &http.Server{Addr: ":" + port, Handler: mux}
	go srv.ListenAndServe()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	fmt.Fprintf(os.Stderr, "Shutting down, saving trails...\n")
	saveTrails()
	fmt.Fprintf(os.Stderr, "Trails saved, closing server...\n")
	srv.Close()
}

func trackPlayerJoinTimes() {
	for {
		time.Sleep(5 * time.Second)
		resp, err := http.Get(fmt.Sprintf("%s/server/rawcmd?token=%s&cmd=%s", apiURL, appToken, url.QueryEscape("/playing")))
		if err != nil { continue }
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var pcmd map[string]interface{}
		json.Unmarshal(b, &pcmd)
		respList, _ := pcmd["response"].([]interface{})
		joinMu.Lock()
		knownPlayers := make(map[string]bool)
		for _, line := range respList {
			str, _ := line.(string)
			if strings.Contains(str, "Online Players") || strings.Contains(str, "no players") { continue }
			for _, name := range strings.Split(str, ",") {
				name = strings.TrimSpace(name)
				if name == "" { continue }
				knownPlayers[name] = true
				if _, exists := joinTimes[name]; !exists {
					joinTimes[name] = time.Now()
				}
			}
		}
		for name := range joinTimes {
			if !knownPlayers[name] { delete(joinTimes, name) }
		}
		joinMu.Unlock()
	}
}

func trackPlayerTrails() {
	log.Printf("trackPlayerTrails started, apiURL=%s appToken=%s", apiURL, appToken)
	for {
		time.Sleep(5 * time.Second)
		resp, err := httpClient.Get(fmt.Sprintf("%s/v2/players/list?token=%s", apiURL, appToken))
		if err != nil { log.Printf("trackPlayerTrails: v2/players/list error: %v", err); continue }
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var plist map[string]interface{}
		json.Unmarshal(b, &plist)
		players, _ := plist["players"].([]interface{})
		log.Printf("trackPlayerTrails: found %d players", len(players))
		onlineNames := make(map[string]bool)
		for _, p := range players {
			pm, _ := p.(map[string]interface{})
			nickname, _ := pm["nickname"].(string)
			if nickname == "" { continue }
			onlineNames[nickname] = true
			respP, errP := httpClient.Get(fmt.Sprintf("%s/v3/players/read?token=%s&player=%s", apiURL, appToken, url.QueryEscape(nickname)))
			if errP != nil { log.Printf("trackPlayerTrails: v3/players/read error for %s: %v", nickname, errP); continue }
			bP, _ := io.ReadAll(respP.Body)
			respP.Body.Close()
			var pInfo map[string]interface{}
			json.Unmarshal(bP, &pInfo)
			pos, _ := pInfo["position"].(string)
			if pos == "" { continue }
			parts := strings.Split(pos, ",")
			if len(parts) != 2 { continue }
			px, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			py, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 != nil || err2 != nil { continue }
			now := time.Now()
			trailMu.Lock()
			td, ok := playerTrails[nickname]
			if !ok {
				td = &trailData{}
				playerTrails[nickname] = td
			}
			td.points = append(td.points, TrailPoint{X: px, Y: py, Time: now.Unix()})
			if len(td.points) > trailMaxPerPlayer {
				td.points = td.points[len(td.points)-trailMaxPerPlayer:]
			}
			td.online = true
			td.lastAdd = now
			trailMu.Unlock()
			log.Printf("trackPlayerTrails: recorded %s at %.0f,%.0f (%d points)", nickname, px, py, len(td.points))
		}
		trailMu.Lock()
		for name, td := range playerTrails {
			if !onlineNames[name] {
				td.online = false
			}
		}
		trailMu.Unlock()
	}
}

type trailDataJSON struct {
	Points  []TrailPoint `json:"points"`
	Online  bool         `json:"online"`
	LastAdd int64        `json:"lastAdd"`
}

func saveTrails() {
	trailMu.RLock()
	data := make(map[string]trailDataJSON, len(playerTrails))
	for name, td := range playerTrails {
		pts := make([]TrailPoint, len(td.points))
		copy(pts, td.points)
		data[name] = trailDataJSON{Points: pts, Online: td.online, LastAdd: td.lastAdd.Unix()}
	}
	trailMu.RUnlock()
	if len(data) == 0 { return }
	if err := os.MkdirAll(trailDir, 0755); err != nil {
		log.Printf("saveTrails mkdir error: %v", err)
		return
	}
	for name, d := range data {
		b, err := json.Marshal(d)
		if err != nil {
			log.Printf("saveTrails marshal error for %s: %v", name, err)
			continue
		}
		safe := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, name)
		path := filepath.Join(trailDir, safe+".json")
		if err := os.WriteFile(path, b, 0644); err != nil {
			log.Printf("saveTrails write error for %s: %v", path, err)
		}
	}
}

func loadTrails() {
	entries, err := os.ReadDir(trailDir)
	if err != nil { return }
	trailMu.Lock()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") { continue }
		b, err := os.ReadFile(filepath.Join(trailDir, e.Name()))
		if err != nil { continue }
		var d trailDataJSON
		if json.Unmarshal(b, &d) != nil { continue }
		if len(d.Points) == 0 { continue }
		name := strings.TrimSuffix(e.Name(), ".json")
		td := &trailData{
			points:  d.Points,
			online:  d.Online,
			lastAdd: time.Unix(d.LastAdd, 0),
		}
		if len(td.points) > trailMaxPerPlayer {
			td.points = td.points[len(td.points)-trailMaxPerPlayer:]
		}
		playerTrails[name] = td
	}
	trailMu.Unlock()
}

func trailSaveLoop() {
	for {
		time.Sleep(10 * time.Second)
		saveTrails()
		trailMu.RLock()
		n := len(playerTrails)
		trailMu.RUnlock()
		if n > 0 { log.Printf("trailSaveLoop: saved %d players", n) }
	}
}

func trailCleanupFiles() {
	trailMu.RLock()
	active := make(map[string]bool, len(playerTrails))
	for name := range playerTrails {
		active[name] = true
	}
	trailMu.RUnlock()
	entries, err := os.ReadDir(trailDir)
	if err != nil { return }
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") { continue }
		name := strings.TrimSuffix(e.Name(), ".json")
		if !active[name] {
			os.Remove(filepath.Join(trailDir, e.Name()))
		}
	}
}