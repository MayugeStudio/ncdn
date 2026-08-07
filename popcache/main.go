package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/yzp0n/ncdn/httprps"
	"github.com/yzp0n/ncdn/types"
)

var originConfigPath = flag.String("originConfigPath", "origin_config.json", "Path to the config file for pocsache")
var shieldConfigPath = flag.String("shieldConfigPath", "shield_config.json", "Path to the config file for origin shield")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

func parseOrigins(configPath string) ([]Origin, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configs from %s", configPath)
	}
	defer f.Close()

	data := []struct{
		Ip4      string `json:"ip4"`
		Hostname string `json:"hostname"`
		Port     string `json:"openPort"`
	}{}

	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse configs %s: %w", configPath, err)
	}

	out := []Origin{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" +data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, Origin{
			Ip4: ip4,
			Hostname: hostname,
			Port: data[i].Port,
			Url: u,
		})
	}

	return out, nil
}

func parseShields(configPath string) ([]Shield, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configs from %s", configPath)
	}
	defer f.Close()

	data := []struct{
		Ip4      string `json:"ip4"`
		Hostname string `json:"hostname"`
		Port     string `json:"openPort"`
	}{}

	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse configs %s: %w", configPath, err)
	}

	out := []Shield{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" +data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, Shield{
			Ip4: ip4,
			Hostname: hostname,
			Port: data[i].Port,
			Url: u,
		})
	}

	return out, nil
}

type cacheEntry struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// cacheの識別に使用するためのキーを生成する
func GenerateCacheKey(host string, path string, queries url.Values) [32]byte {
	byteArray := make([]byte, 0)
	for _, k := range slices.Sorted(maps.Keys(queries)) {
		byteArray = append(byteArray, []byte(k)...)
		values := queries[k][:]
		slices.Sort(values)
		byteArray = append(byteArray, []byte(strings.Join(values, ""))...)
	}

	byteArray = append(byteArray, []byte(path)...)
	byteArray = append(byteArray, []byte(host)...)

	hashValue := sha256.Sum256(byteArray)
	return hashValue
}

func main() {
	flag.Parse()

	origins, err := parseOrigins(*originConfigPath)
	if err != nil {
		log.Fatalf("Failed to parse configurations: %v", err)
	}
	shields, err := parseShields(*shieldConfigPath)
	if err != nil {
		log.Fatalf("Failed to parse configurations: %v", err)
	}

	start := time.Now()

	mux := http.NewServeMux()
	rps := httprps.NewMiddleware(mux)
	http.Handle("/", rps)

	mux.HandleFunc("/statusz", func(w http.ResponseWriter, r *http.Request) {
		s := types.PoPStatus{
			Id:     *nodeId,
			Uptime: time.Since(start).Seconds(),
			Load:   rps.GetRPS(),
		}
		bs, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			log.Printf("Failed to marshal PoP status: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write(bs)
	})
	mux.HandleFunc("/latencyz", func(w http.ResponseWriter, r *http.Request) {
		// return 204
		w.WriteHeader(http.StatusNoContent)
	})

	sharedTransport := &http.Transport{
		MaxIdleConns: 1024,
		MaxIdleConnsPerHost: 256,
	}
	cs := NewCacheServer(origins, shields, sharedTransport)

	mux.Handle("/", cs)

	log.Printf("Listening on %s...", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}

}
