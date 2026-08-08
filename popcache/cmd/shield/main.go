package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/yzp0n/ncdn/types"
	"github.com/yzp0n/ncdn/httprps"
	"github.com/yzp0n/ncdn/popcache/internal"
)

var g singleflight.Group


type Shield struct {
	nodeId    string
	cache     *lru.Cache[[32]byte, *types.CacheEntry]
	origins   map[string]*types.Upstream
	transport *http.Transport
}

func NewShield(nodeId string, origins []*types.Upstream, transport *http.Transport) *Shield {
	cache, err := lru.New[[32]byte, *types.CacheEntry](256)
	if err != nil {
		log.Fatalf("Failed to create lru.Cache: %v", err)
	}

	originMap := make(map[string]*types.Upstream)
	for _, origin := range origins {
		originMap[origin.Hostname] = origin
	}

	return &Shield{
		cache: cache,
		origins: originMap,
		transport: transport,
	}
}

func (s *Shield) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := internal.CacheKey(r.Host, r.URL.Port(), r.URL.RequestURI())
	ce, ok := s.cache.Get(key);
	if ok /* キャッシュヒット */ {
		log.Println("Cache Hit !!!")
		for k, vs := range ce.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Add("X-Cache", "Hit")
		w.WriteHeader(ce.StatusCode)
		w.Write(ce.Body)
	} else /* キャッシュミス */ {
		log.Println("Cache Miss !!!")

		hostname, _, err := net.SplitHostPort(r.Host) // FIXME: ポート番号なしの場合を考慮する
		log.Printf("Host: %s\n", hostname)
		if err != nil {
			http.Error(w, "unknown host", http.StatusNotFound)
			return
		}

		// Originを選択する
		origin, ok := s.origins[hostname]
		if !ok {
			http.Error(w, "unknown host", http.StatusNotFound)
		}

		w.Header().Add("X-NCDN-Shield-NodeId", s.nodeId)
		val, err, _ := g.Do(string(key[:]), func() (any, error) {
			res, err := internal.Fetch(r.Context(), origin.Ip4.String(), origin.Port, r, s.transport)

			if err != nil {
				return nil, err
			}
			defer res.Body.Close()

			// キャッシュに保存する
			// TODO: cache-controlをみる
			h := res.Header.Clone()
			h.Del("X-Cache")
			h.Del("X-NCDN-Popcache-NodeId")
			h.Del("X-NCDN-Shield-NodeId")
			body, err := io.ReadAll(res.Body)
			if err != nil {
				return nil, err
			}
			ce := &types.CacheEntry{
				StatusCode: res.StatusCode,
				Header:     h,
				Body:       body,
			}
			s.cache.Add(key, ce)
			// レスポンスに書き込む
			w.Header().Add("X-Cache", "Miss")
			w.WriteHeader(res.StatusCode)
			w.Write(body)
			log.Printf("Successfully fetching data from %s:%s\n", origin.Ip4.String(), origin.Port)
			return ce, nil
		})

		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		ce := val.(*types.CacheEntry)

		for k, vs := range ce.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}

		w.Header().Add("X-Cache", "Hit")
		w.WriteHeader(ce.StatusCode)
		w.Write(ce.Body)
	}
}

var originConfigPath = flag.String("originConfigPath", "origin_config.json", "Path to the config file for popcache")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

func main() {
	flag.Parse()

	origins, err := internal.ParseUpstreams(*originConfigPath)
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

	ss := NewShield(*nodeId, origins, sharedTransport)

	mux.Handle("/", ss)

	log.Printf("Listening on %s...", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}
}
