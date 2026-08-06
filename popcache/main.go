package main

import (
	"crypto/sha256"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/url"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/yzp0n/ncdn/httprps"
	"github.com/yzp0n/ncdn/types"
)

var originConfigPath = flag.String("originConfigPath", "origin_config.json", "Path to the config file for pocsache")
var shieldConfigPath = flag.String("shieldConfigPath", "shield_config.json", "Path to the config file for origin shield")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

func parseOrigins(configPath string) ([]types.Origin, error) {
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

	out := []types.Origin{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" +data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, types.Origin{
			Ip4: ip4,
			Hostname: hostname,
			Port: data[i].Port,
			Url: u,
		})
	}

	return out, nil
}

func parseShields(configPath string) ([]types.Shield, error) {
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

	out := []types.Shield{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" +data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, types.Shield{
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

type CacheServer struct {
	cache *lru.Cache[[32]byte, *cacheEntry]
	origins map[string]types.Origin // Hostname -> Origin
	shields map[string]types.Shield // Hostname -> Shield
	transport *http.Transport
}

func NewCacheServer(origins []types.Origin, shields []types.Shield, transport *http.Transport) *CacheServer {
	cache, err := lru.New[[32]byte, *cacheEntry](256)
	if err != nil {
		log.Fatalf("Failed to create lru.Cache: %v", err)
	}
	
	originMap := make(map[string]types.Origin)
	for _, origin := range origins {
		originMap[origin.Hostname] = origin
	}

	shieldMap := make(map[string]types.Shield)
	for _, shield := range shields {
		shieldMap[shield.Hostname] = shield
	}

	return &CacheServer{
		cache: cache,
		origins: originMap,
		shields: shieldMap,
		transport: transport,
	}
}

// 指定したhostname, portのサーバにHTTPリクエストを送る。その際、ヘッダを引き継ぐ
func (c *CacheServer) fetch(ctx context.Context, r *http.Request, ip4 netip.Addr, port string) (*http.Response, error) {
	url := "http://" + ip4.String() + ":" + port + r.RequestURI
	out, err := http.NewRequestWithContext(ctx, r.Method, url, nil) // GETしか対応しないので、Bodyはセットしない

	if err != nil {
		return nil, err
	}

	out.Header = r.Header.Clone()
  // Hostを引き継ぐ！こうすることで、上流側にどのOriginサーバ向けのリクエストを受信したがっているかを伝える。
	out.Host = r.Host

	return c.transport.RoundTrip(out)

}

func (c *CacheServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cacheKey := GenerateCacheKey(r.Host, r.URL.Path, r.URL.Query())

	w.Header().Set("X-NCDN-PoPCache-NodeId", *nodeId)

	// キャッシュヒット
	// TODO: Fresh Stale Missを返す関数を定義する
	ce, ok := c.cache.Get(cacheKey);
	if ok {
		log.Println("Cache Hit !!!")
		for k, vs := range ce.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Add("X-Cache", "Hit")
		w.WriteHeader(ce.StatusCode)
		w.Write(ce.Body)
		return
	}

	// キャッシュミス
	log.Println("Cache Miss !!!")

	// shieldに取りに行く
	hostname, _, err := net.SplitHostPort(r.Host)
	log.Println(hostname)
	if err != nil {
		http.Error(w, "unknown host", http.StatusNotFound)
		return
	}

	// Shieldを選択
	shield, ok := c.shields[hostname]
	if !ok {
		http.Error(w, "unkown host", http.StatusNotFound)
		return
	}

	// shieldに取りに行く場合もX-CacheはMissとしておく
	w.Header().Add("X-Cache", "Miss")

	res, err := c.fetch(r.Context(), r, shield.Ip4, shield.Port)
	if err != nil {
		// shieldにフェッチできなかった時の対策を考える
		// 1. originにフェールオーバ
		// 2. 別shieldに行ってからoriginにフェールオーバ
		http.Error(w, "something wrong", http.StatusInternalServerError)
		return
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusOK {
		// キャッシュに保存する
		h := res.Header.Clone()
		h.Del("X-Cache")
		h.Del("X-NCDN-PoPCache-NodeId")
		h.Del("X-NCDN-Shield-NodeId")
		body, err := io.ReadAll(res.Body)
		if err != nil {
			http.Error(w, "something went wrong", http.StatusInternalServerError)
		}
		c.cache.Add(cacheKey, &cacheEntry{
			StatusCode: res.StatusCode,
			Header:     h,
			Body:       body,
		})

		// レスポンスに書き込む
		w.Header().Add("X-Cache", res.Header.Get("X-Cache"))
		w.Header().Add("X-NCDN-Shield-NodeId", res.Header.Get("X-NCDN-Shield-NodeId"))
		w.Write(body)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "something wrong", http.StatusInternalServerError)
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
