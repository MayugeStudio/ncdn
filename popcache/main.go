package main

import (
	"bytes"
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
	"net/http/httputil"
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

var originConfigPath = flag.String("originConfigPath", "origin_config.json", "Path to the config file for popcache")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

type PopcacheConfig struct {
	originLookup map[string]types.OriginInfo // Host -> OriginInfo
}

func parseOrigins(configPath string) ([]types.OriginInfo, error) {
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

	out := []types.OriginInfo{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" +data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, types.OriginInfo{
			Ip4: ip4,
			Hostname: hostname,
			Port: data[i].Port,
			Url: u,
		})
	}

	return out, nil
}

type CacheEntry struct {
	StatusCode int
	Header     http.Header
	Body       string
}

type cacheRecorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *cacheRecorder) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *cacheRecorder) Write(b []byte) (int, error) {
	c.buf.Write(b)
	return c.ResponseWriter.Write(b)
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

type originKey struct {}

// キャッシュが存在すれば返却、存在しなければ取りに行くミドルウェア
func withCache(next http.Handler, cfg *PopcacheConfig, cache *lru.Cache[[32]byte, *CacheEntry]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheKey := GenerateCacheKey(r.Host, r.URL.Path, r.URL.Query())

		w.Header().Set("X-NCDN-PoPCache-NodeId", *nodeId)

		// キャッシュヒット
		if ce, ok := cache.Get(cacheKey); ok {
			log.Println("Cache Hit !!!")
			for k, vs := range ce.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.Header().Add("X-Cache", "Hit")
			w.WriteHeader(ce.StatusCode)
			io.WriteString(w, ce.Body)
			return
		}

		// キャッシュミス
		log.Println("Cache Miss !!!")

		// Originを選択
		// Originが選択できなかった場合はX-Cache: Missもつけない
		hostname, _, err := net.SplitHostPort(r.Host)
		log.Println(hostname)
		if err != nil {
			http.Error(w, "unknown host", http.StatusNotFound)
		}
		origin, ok := cfg.originLookup[hostname]
		if !ok {
			http.Error(w, "unkown host", http.StatusNotFound)
			return
		}

		w.Header().Add("X-Cache", "Miss")

		ctx := context.WithValue(r.Context(), originKey{}, origin)
		rec := &cacheRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		if rec.status == http.StatusOK {
			h := w.Header().Clone()
			h.Del("X-Cache")
			h.Del("X-NCDN-PoPCache-NodeId")
			cache.Add(cacheKey, &CacheEntry{
				StatusCode: rec.status,
				Header:     h,
				Body:       rec.buf.String(),
			})
		}
	})
}

func main() {
	flag.Parse()

	origins, err := parseOrigins(*originConfigPath)
	if err != nil {
		log.Fatalf("Failed to parse configurations: %v", err)
	}

	cfg := &PopcacheConfig{
		originLookup: make(map[string]types.OriginInfo),
	}
	for _, origin := range origins {
		cfg.originLookup[origin.Hostname] = origin
	}

	cache, err := lru.New[[32]byte, *CacheEntry](256)
	if err != nil {
		log.Fatalf("Failed to create lru.Cache: %v", err)
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

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetXForwarded()
			pr.Out.Header.Set("X-NCDN-PoPCache-NodeId", *nodeId)
			log.Printf("Got a request from %s to %s", pr.In.RemoteAddr, pr.In.Host)

			origin := pr.In.Context().Value(originKey{}).(types.OriginInfo)
			log.Println(origin.Url.String())
			pr.SetURL(origin.Url)
		},
	}

	mux.Handle("/", withCache(rp, cfg, cache))

	log.Printf("Listening on %s...", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}
}
