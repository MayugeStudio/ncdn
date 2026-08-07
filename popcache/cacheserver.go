package main

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"net/http"
	"io"
	"log"

	lru "github.com/hashicorp/golang-lru/v2"
)

type Origin struct {
	Ip4      netip.Addr
	Hostname string
	Port     string
	Url      *url.URL
}

type Shield struct {
	Ip4      netip.Addr // TODO: 多分IPアドレスはここで持たない方がいい DNSに頼るべき
	Hostname string
	Port     string
	Url      *url.URL
}

type CacheServer struct {
	cache *lru.Cache[[32]byte, *cacheEntry]
	origins map[string]Origin // Hostname -> Origin
	shields []Shield // Hostname -> Shield
	transport *http.Transport
}

func NewCacheServer(origins []Origin, shields []Shield, transport *http.Transport) *CacheServer {
	cache, err := lru.New[[32]byte, *cacheEntry](256)
	if err != nil {
		log.Fatalf("Failed to create lru.Cache: %v", err)
	}
	
	originMap := make(map[string]Origin)
	for _, origin := range origins {
		originMap[origin.Hostname] = origin
	}

	// shieldMap := make(map[netip.Addr]Shield)
	// addr := netip.MustParseAddr("192.168.88.40")
	// for _, shield := range shields {
	// 	shieldMap[addr] = shield
	// }

	return &CacheServer{
		cache: cache,
		origins: originMap,
		shields: shields,
		transport: transport,
	}
}

// 指定したhostname, portのサーバにHTTPリクエストを送る。その際、ヘッダを引き継ぐ
func (c *CacheServer) fetch(ctx context.Context, r *http.Request, hostname string, port string) (*http.Response, error) {
	url := "http://" + hostname + ":" + port + r.RequestURI
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
	// TODO: hashアルゴリズムを実装
	shield := c.shields[0]

	// shieldに取りに行く場合もX-CacheはMissとしておく
	w.Header().Add("X-Cache", "Miss")

	res, err := c.fetch(r.Context(), r, shield.Hostname, shield.Port)
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

