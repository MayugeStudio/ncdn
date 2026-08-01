package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/yzp0n/ncdn/httprps"
	"github.com/yzp0n/ncdn/types"
)

var originURLStr = flag.String("originURL", "http://localhost:8888", "Origin server URL")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

type cacheEntry struct {
	Body string
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

// キャッシュをチェックして、存在していればそれをボディに書き込んで返却するミドルウェア
func checkCache(next http.Handler, cache *lru.Cache[[32]byte, *cacheEntry]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheKey := GenerateCacheKey(r.Host, r.URL.Path, r.URL.Query())

		if cacheEntry, ok := cache.Get(cacheKey); ok {
			io.WriteString(w, cacheEntry.Body)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	flag.Parse()

	originURL, err := url.Parse(*originURLStr)
	if err != nil {
		log.Fatalf("Failed to parse origin URL %q: %v", *originURLStr, err)
	}

	cache, err := lru.New[[32]byte, *cacheEntry](256)
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
		// FIXME: actually cache stuff...
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetXForwarded()
			r.Out.Header.Set("X-NCDN-PoPCache-NodeId", *nodeId)
			r.SetURL(originURL)
			log.Printf("%s -> %s\n", r.In.Host, r.Out.Host)
		},
		ModifyResponse: func(r *http.Response) error {
			return nil
		},
	}

	mux.Handle("/", checkCache(rp, cache))

	log.Printf("Listening on %s...", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}
}
