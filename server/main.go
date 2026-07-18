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

func main() {
	roots := flag.String("roots", "", "comma-separated roots to discover albums from (e.g. /sdcard/DCIM,/sdcard/Pictures)")
	webDir := flag.String("web", "", "web UI directory (default: ../web)")
	port := flag.Int("port", 8080, "listen port")
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

	lanIP := *lanIPFlag
	if lanIP == "" {
		lanIP = getLANIP()
	}

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	log.Printf("PIN: %s", pin)
	log.Printf("photosmove server listening on http://%s:%d", lanIP, *port)
	log.Fatal(http.ListenAndServe(addr, handler))
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
	// 最可靠: net.Dial udp 到外部地址, LocalAddr 即本机出口 IP.
	// 不实际发包, 只查路由表; 在 Android/gomobile 上比 net.InterfaceAddrs 枚举鲁棒
	// (后者在部分 Android 版本拿不到 wlan0 地址 → 误报 localhost, PC 连不上).
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP.To4() != nil {
			return addr.IP.String()
		}
	}
	// fallback: 接口枚举
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	// 最后兜底 0.0.0.0 (listen 本就是 0.0.0.0, 至少不误导用户用 localhost 连自己)
	return "0.0.0.0"
}
