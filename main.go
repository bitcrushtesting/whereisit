package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"gopkg.in/ini.v1"
)

var devices struct {
	sync.Mutex
	d []Device
}

type Device struct {
	ExternalAddress string            `json:"-"`
	InternalAddress string            `json:"address"`
	Identifier      string            `json:"id"` // e.g. serial number
	Name            string            `json:"name"`
	Tags            map[string]string `json:"tags,omitempty"` // optional
	Added           time.Time         `json:"added"`
}

type Config struct {
	BasicAuthEnabled  bool
	Username          string
	Password          string
	ApiKeyAuthEnabled bool
	APIKey            string
}

func LoadConfiguration(primaryPath, fallbackPath string) (*Config, error) {
	// Check if the primary file exists
	if _, err := os.Stat(primaryPath); os.IsNotExist(err) {
		log.Printf("Primary file %s not found, trying fallback file %s\n", primaryPath, fallbackPath)
		// If primary does not exist, check if fallback exists
		if _, err := os.Stat(fallbackPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("neither %s nor %s were found", primaryPath, fallbackPath)
		}
		primaryPath = fallbackPath // Switch to fallback
	}

	cfg, err := ini.Load(primaryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load ini file: %w", err)
	}

	var config Config

	config.BasicAuthEnabled, _ = cfg.Section("basic_auth").Key("enabled").Bool()
	if config.BasicAuthEnabled {
		config.Username = cfg.Section("basic_auth").Key("username").String()
		config.Password = cfg.Section("basic_auth").Key("password").String()

		if config.Username == "" || config.Password == "" {
			return nil, fmt.Errorf("missing required basic auth credentials in ini file")
		}
	}
	config.ApiKeyAuthEnabled, _ = cfg.Section("api").Key("api_key_enabled").Bool()
	if config.ApiKeyAuthEnabled {
		config.APIKey = cfg.Section("api").Key("api_key").String()
		if config.APIKey == "" {
			return nil, fmt.Errorf("missing required credentials in ini file")
		}
	}
	return &config, nil
}

func main() {

	primaryIniFilePath := "/etc/whereisit.ini"
	fallbackIniFilePath := "./whereisit.ini"

	config, err := LoadConfiguration(primaryIniFilePath, fallbackIniFilePath)
	if err != nil {
		log.Fatalf("Error loading credentials: %v", err)
	}

	publicFolder := flag.String("public", "./public/", "Folder with the public files")
	httpPort := flag.String("http-port", "8180", "Port for the HTTP server")
	l := flag.Int("lifetime", 24, "Device entry lifetime in hours")
	v := flag.Bool("verbose", false, "Enable verbose logging")

	// Parse the command-line flags
	flag.Parse()
	fmt.Println("Listen on port", *httpPort)
	fmt.Println("Using public folder", *publicFolder)
	fmt.Println("Lifetime in hours:", *l)

	// Check if the pubic folder exists
	if _, err := os.Stat(*publicFolder); os.IsNotExist(err) {
		slog.Error("Publich folder does not exist")
		os.Exit(1)
	}

	devices.d = make([]Device, 0)

	r := mux.NewRouter()
	apiRouter := r.PathPrefix("/api").Subrouter()
	if *v {
		fmt.Println("Verbose logging enabled")
		slog.SetLogLoggerLevel(slog.LevelDebug)
		apiRouter.Use(logRequest)
	}
	if config.ApiKeyAuthEnabled {
		apiRouter.Use(KeyAuth(config.APIKey))
	}
	if config.BasicAuthEnabled {
		apiRouter.Use(BasicAuthMiddleware(config.Username, config.Password))
	}
	apiRouter.HandleFunc("/register", RegisterDevice).Methods("POST")
	apiRouter.HandleFunc("/devices", ListDevices).Methods("GET")
	apiRouter.HandleFunc("/alldevices", requireAuth(config, ListAllDevices)).Methods("GET")

	spa := spaHandler{staticPath: *publicFolder, indexPath: "index.html"}
	r.PathPrefix("/").Handler(spa)

	lifetime := time.Duration(*l) * time.Hour
	go cleanup(lifetime)

	srv := &http.Server{
		Handler:      r,
		Addr:         "0.0.0.0:" + *httpPort,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

type spaHandler struct {
	staticPath string
	indexPath  string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Join internally call path.Clean to prevent directory traversal
	path := filepath.Join(h.staticPath, r.URL.Path)

	// check whether a file exists or is a directory at the given path
	fi, err := os.Stat(path)
	if os.IsNotExist(err) || fi.IsDir() {
		// file does not exist or path is a directory, serve index.html
		http.ServeFile(w, r, filepath.Join(h.staticPath, h.indexPath))
		return
	}

	if err != nil {
		// if we got an error (that wasn't that the file doesn't exist) stating the
		// file, return a 500 internal server error and stop
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// otherwise, use http.FileServer to serve the static file
	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ea, _, _ := net.SplitHostPort(r.RemoteAddr)
		slog.Debug("Request", "path", r.URL.Path, "external ip", ea)
		next.ServeHTTP(w, r)
	})
}

func requireAuth(config *Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !config.ApiKeyAuthEnabled && !config.BasicAuthEnabled {
			http.Error(w, "Enable api_key or basic_auth in whereisit.ini to access this endpoint", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func KeyAuth(apiKey string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := r.Header.Get("X-API-Key")
			if a != apiKey {
				http.Error(w, "Invalid or missing API key", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func BasicAuthMiddleware(username, password string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get the Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Check if the Authorization header is in "Basic" format
			if !strings.HasPrefix(authHeader, "Basic ") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Decode the Base64 encoded username and password
			encodedCredentials := strings.TrimPrefix(authHeader, "Basic ")
			decodedCredentials, err := base64.StdEncoding.DecodeString(encodedCredentials)
			if err != nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Split the decoded credentials (format: "username:password")
			credentials := strings.SplitN(string(decodedCredentials), ":", 2)
			if len(credentials) != 2 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Compare the provided credentials with the expected ones
			providedUsername := credentials[0]
			providedPassword := credentials[1]

			if providedUsername != username || providedPassword != password {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// If authentication is successful, proceed to the next handler
			next.ServeHTTP(w, r)
		})
	}
}

func findDeviceByIdentifier(id string, ea string) (int, bool) {

	if isLocalNetwork(ea) {
		ea = "local"
	}

	for i, d := range devices.d {
		if d.Identifier == id && d.ExternalAddress == ea {
			return i, true
		}
	}
	return 0, false
}

func devicesFor(ea string) []Device {

	if isLocalNetwork(ea) {
		ea = "local"
	}
	found := []Device{}
	for _, d := range devices.d {
		if d.ExternalAddress == ea {
			found = append(found, d)
		}
	}
	slog.Debug("Devices for", "external address", ea, "count", len(found))
	return found
}

func addDevice(ea string, ia string, id string, name string, tags map[string]string) {

	slog.Debug("Add device", "external address", ea, "internal address", ia, "name", name)
	if isLocalNetwork(ea) {
		ea = "local"
	}

	devices.d = append(devices.d, Device{
		ExternalAddress: ea,
		InternalAddress: ia,
		Identifier:      id,
		Name:            name,
		Tags:            tags,
		Added:           time.Now(),
	})
}

func RegisterDevice(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		slog.Debug("Content type is not JSON")
		http.Error(w, "Content-Type must be application/json", 400)
		return
	}

	if r.Body == nil {
		slog.Debug("Body did not contain any content")
		http.Error(w, "No content", 400)
		return
	}

	var t struct {
		Name    string            `json:"name"`
		Id      string            `json:"id"`
		Address string            `json:"address"`
		Tags    map[string]string `json:"tags,omitempty"`
	}

	err := json.NewDecoder(r.Body).Decode(&t)
	if err != nil {
		slog.Debug("JSON body could not be decoded", "error", err)
		http.Error(w, err.Error(), 400)
		return
	}

	t.Address = strings.Trim(t.Address, " ")
	if net.ParseIP(t.Address) == nil {
		slog.Debug("Internal address invalid", "address", t.Address)
		http.Error(w, t.Address+" is not a valid IP address", http.StatusBadRequest)
		return
	}

	// Prevent simple loopback mistake
	if t.Address == "127.0.0.1" || t.Address == "::1" {
		slog.Debug("Device loopback is not allowed")
		http.Error(w, `Loopback is not allowed`, http.StatusBadRequest)
		return
	}

	// Get the external address
	ea := getIPAddressFromRequest(r)
	if ea == "" {
		http.Error(w, `Host 127.0.0.1 is not allowed to register devices`, http.StatusBadRequest)
		http.NotFound(w, r)
		return
	}

	devices.Lock()
	defer devices.Unlock()

	if i, ok := findDeviceByIdentifier(t.Id, ea); ok {
		devices.d[i].Name = t.Name
		devices.d[i].InternalAddress = t.Address
		devices.d[i].Tags = t.Tags
		devices.d[i].Added = time.Now()
		slog.Debug("Device updated", "address", t.Address)
	} else {
		addDevice(ea, t.Address, t.Id, t.Name, t.Tags)
		slog.Debug("Device added", "address", t.Address)
	}
	fmt.Fprintf(w, "Successfully added!\n")
}

func ListDevices(w http.ResponseWriter, r *http.Request) {
	// Get the external address
	ea := getIPAddressFromRequest(r)
	if ea == "" {
		http.Error(w, `Host 127.0.0.1 is not allowed to register devices`, http.StatusBadRequest)
		http.NotFound(w, r)
		return
	}

	devices.Lock()
	defer devices.Unlock()

	ds := devicesFor(ea)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ds); err != nil {
		panic(err)
	}
}

func ListAllDevices(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(devices.d); err != nil {
		panic(err)
	}
}

func cleanup(lifetime time.Duration) {
	for {
		time.Sleep(time.Second * 5)
		devices.Lock()
		for i := len(devices.d) - 1; i >= 0; i-- {
			d := devices.d[i]
			if time.Since(d.Added) > lifetime {
				devices.d = append(devices.d[:i], devices.d[i+1:]...)
			}
		}
		devices.Unlock()
	}
}

// Extracts the IP address from RemoteAddr, handling both IPv4 and IPv6
func getIPAddressFromRequest(r *http.Request) string {

	// For IPv6 addresses, RemoteAddr includes brackets around the IP and a zone identifier (e.g., %wlan0)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		slog.Debug("Could not split remote address into IP and port", "remote address", r.RemoteAddr)
		return ""
	}

	// Split out any zone identifier in the IPv6 address (i.e., '%interface' like '%wlan0')
	ip = strings.Split(ip, "%")[0]

	// Check if proxy was configured.
	if ip == "127.0.0.1" || ip == "::1" {
		xrealip := r.Header.Get("x-real-ip")
		if xrealip == "" {
			slog.Debug("127.0.0.1 tried to add an address, this can happen when proxy is not configured correctly.")
			return ""
		}
		ip = xrealip
	}
	return ip
}

func isLocalNetwork(ip string) bool {

	// Parse the IP to ensure it's a valid one
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		slog.Debug("Given IP string is not valid", "ip", ip)
		return false
	}

	privateRanges := []string{
		// Define LAN ranges IPv4
		"10.0.0.0/8",     // Class A private network
		"172.16.0.0/12",  // Class B private network
		"192.168.0.0/16", // Class C private network
		"127.0.0.0/8",    // Loopback range

		// Define private IPv6 address ranges
		"fc00::/7",  // Unique local address range
		"fe80::/10", // Link-local address range
		"::1/128",   // Loopback range
	}

	// Check if the parsed IP falls within any of the private ranges
	for _, cidr := range privateRanges {
		_, privateNet, _ := net.ParseCIDR(cidr)
		if privateNet.Contains(parsedIP) {
			slog.Debug("Url is from a local area network:", "parsedIP", parsedIP)
			return true
		}
	}
	return false
}
