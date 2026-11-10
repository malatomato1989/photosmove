package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
)

var version = "dev"

// easyPorts are tried in order when the preferred port is unavailable. They are
// deliberately easy to type on a phone/PC. 6666 is intentionally excluded: Chromium
// (Chrome/Edge) blocks it as an unsafe port, which would make the URL unusable.
var easyPorts = []int{1111, 2222, 3333, 4444, 5555, 6789, 7777, 8888, 9999}

func main() {
	roots := flag.String("roots", "", "comma-separated roots to discover albums from (e.g. /sdcard/DCIM,/sdcard/Pictures)")
	webDir := flag.String("web", "", "web UI directory (default: ../web)")
	port := flag.Int("port", 0, "preferred listen port (0 = auto-select from the built-in easy-port pool)")
	lanIPFlag := flag.String("lan-ip", "", "LAN IP address (auto-detected if empty)")
	mediaDB := flag.String("media-db", "", "MediaStore JSON file for media files")
	mediaPort := flag.Int("media-port", 0, "Java media file server port")
	flag.Parse()

	if *webDir == "" {
		*webDir = "../web"
	}

	if *roots == "" {
		fmt.Fprintln(os.Stderr, "usage: photosmove -roots <root1,root2,...>")
		os.Exit(1)
	}

	pin := generatePIN()
	token := generateToken()

	albumRoots := splitAndTrim(*roots)
	log.Printf("photosmove v%s starting", version)
	log.Printf("Discovering albums...")
	albums := discoverAlbums(albumRoots, *mediaDB)
	log.Printf("Found %d albums", len(albums))

	mux := http.NewServeMux()
	handler := registerHandlers(mux, pin, token, albums, *webDir, *mediaPort)

	// Preferred (last successful port from the previous run) → easy-to-type pool →
	// OS-assigned ephemeral port. The last candidate guarantees startup even when every
	// easy port is occupied.
	candidates := append(append([]int{}, easyPorts...), 0)
	ln, actualPort, err := listenWithFallback(*port, candidates)
	if err != nil {
		// Keep this machine-readable: ServerService parses the line and shows a
		// localized startup-error message instead of a silent "stopped".
		log.Printf("ERROR:PORT_UNAVAILABLE %v", err)
		os.Exit(1)
	}
	defer ln.Close()

	lanIP := *lanIPFlag
	if lanIP == "" {
		lanIP = getLANIP()
	}

	log.Printf("PIN: %s", pin)
	// ServerService parses PORT to build the URL shown on the phone. Emit it before
	// the human-readable listening line so the port is known as early as possible.
	log.Printf("PORT:%d", actualPort)
	log.Printf("photosmove server listening on http://%s:%d", lanIP, actualPort)
	if err := http.Serve(ln, handler); err != nil {
		log.Fatal(err)
	}
}

// listenWithFallback binds the preferred port, then each fallback in order. A
// fallback value of 0 asks the OS for an ephemeral port (used by tests).
func listenWithFallback(preferred int, fallback []int) (net.Listener, int, error) {
	candidates := make([]int, 0, len(fallback)+1)
	seen := make(map[int]bool, len(fallback)+1)
	if preferred > 0 {
		candidates = append(candidates, preferred)
		seen[preferred] = true
	}
	for _, p := range fallback {
		if p < 0 || seen[p] {
			continue
		}
		candidates = append(candidates, p)
		seen[p] = true
	}

	var lastErr error
	for _, p := range candidates {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
		if err == nil {
			return ln, ln.Addr().(*net.TCPAddr).Port, nil
		}
		lastErr = err
		log.Printf("port %d unavailable: %v", p, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate ports")
	}
	return nil, 0, lastErr
}

func splitAndTrim(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func generatePIN() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		log.Fatalf("generate PIN: %v", err)
	}
	return fmt.Sprintf("%04d", n.Int64())
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("generate token: %v", err)
	}
	return fmt.Sprintf("%x", b)
}

func getLANIP() string {
	// Default-route egress IP via a UDP "dial" to an external address: no packet is sent, only
	// the routing table is consulted, and LocalAddr is the local egress IP. This is the most
	// robust option on Android/gomobile (enumerating net.InterfaceAddrs sometimes misses wlan0
	// on certain Android versions → falsely reports localhost, and the PC cannot connect).
	// Caveat: it follows the default route, so an always-on VPN's tun address may be returned.
	// ServerService.getWifiIP filters by TRANSPORT_WIFI and passes -lan-ip, so this runs only
	// when the Java side detects no Wi-Fi address.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		// Require a private/site-local address: on cellular or an always-on VPN the default-route
		// egress is a public or CGNAT (100.64/10) IP that a PC on the Wi-Fi can never reach;
		// surfacing it would mislead the user. Fall through to interface enumeration / 0.0.0.0.
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP.To4() != nil && addr.IP.IsPrivate() {
			return addr.IP.String()
		}
	}
	// Fallback: interface enumeration — prefer a private/site-local IPv4 (10/8, 172.16/12,
	// 192.168/16) and skip loopback/public addresses, so we do not surface a mobile-data or
	// virtual interface address that a PC on the Wi-Fi could never reach.
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				if ipnet.IP.IsPrivate() {
					return ipnet.IP.String()
				}
			}
		}
	}
	// Final fallback 0.0.0.0 (listen is 0.0.0.0 anyway; at least it doesn't
	// mislead the user into connecting to themselves via localhost)
	return "0.0.0.0"
}
