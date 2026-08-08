package internal

import (
	"context"
	"crypto/sha256"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"

	"github.com/yzp0n/ncdn/types"
)

// cacheの識別に使用するためのキーを生成する
func CacheKey(host string, path string, queries string) [32]byte {
	byteArray := make([]byte, 0)

	byteArray = append(byteArray, []byte(host)...)
	byteArray = append(byteArray, []byte(path)...)
	byteArray = append(byteArray, []byte(queries)...)

	hashValue := sha256.Sum256(byteArray)
	return hashValue
}

// dest can be IP address or Hostname
func Fetch(ctx context.Context, dest string, port string, r *http.Request, t *http.Transport) (*http.Response, error) {
	url := "http://" + dest + ":" + port + r.RequestURI
	out, err := http.NewRequestWithContext(ctx, r.Method, url, nil) // GETしか対応しないので、Bodyはセットしない

	if err != nil {
		return nil, err
	}

	out.Header = r.Header.Clone()
  // Hostを引き継ぐ！こうすることで、上流側にどのOriginサーバ向けのリクエストを受信したがっているかを伝える。
	out.Host = r.Host

	return t.RoundTrip(out)
}

type CacheServer struct {
	nodeId    string
	cache     *Cache
	origins   map[string]*types.Upstream // Hostname -> Origin
	shields   []*types.Upstream // Hostname -> Shield
	transport *http.Transport
}

func NewCacheServer(nodeId string, origins []*types.Upstream, shields []*types.Upstream, transport *http.Transport) *CacheServer {
	cache := NewCache(256)
	
	originMap := make(map[string]*types.Upstream)
	for _, origin := range origins {
		originMap[origin.Hostname] = origin
	}

	// shieldMap := make(map[netip.Addr]Shield)
	// addr := netip.MustParseAddr("192.168.88.40")
	// for _, shield := range shields {
	// 	shieldMap[addr] = shield
	// }

	return &CacheServer{
		nodeId: nodeId,
		cache: cache,
		origins: originMap,
		shields: shields,
		transport: transport,
	}
}

// 指定したhostname, portのサーバにHTTPリクエストを送る。その際、ヘッダを引き継ぐ
func (c *CacheServer) fetch(ctx context.Context, r *http.Request, ip4 netip.Addr, port string) (*http.Response, error) {
	resp, err := Fetch(ctx, ip4.String(), port, r, c.transport)
	if err != nil {
		log.Printf("Failed to fetch data from %s:%s\n", ip4, port)
	} else {
		log.Printf("Fetch data from %s:%s successfully\n", ip4, port)
	}
	return resp, err
}

func (c *CacheServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println(r.Host, r.URL.Port(), r.URL.RequestURI())
	key := CacheKey(r.Host, r.URL.Port(), r.URL.RequestURI())

	w.Header().Set("X-NCDN-PoPCache-NodeId", c.nodeId)

	// キャッシュヒット
	// TODO: Fresh Stale Missを返す関数を定義する
	ce, ok := c.cache.Get(key);
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

	// Shieldに取りに行く
	// Shieldを選択 
	// TODO: hashアルゴリズムを実装
	shield := c.shields[0]

	log.Printf("%s: Send request to shield (%s:%s)\n", c.nodeId, shield.Ip4, shield.Port)
	res, err := c.fetch(r.Context(), r, shield.Ip4, shield.Port)

	if err != nil {
		// 別Originにフェールオーバする
		log.Printf("The shield is down. Fetch data from origin server directly\n")
		// ホスト名からOriginサーバのデータを取得
		hostname, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			http.Error(w, "unknown host", http.StatusNotFound)
			return
		}
		log.Printf("Fetch data from %s\n", hostname)

		origin, ok := c.origins[hostname]
		if !ok {
			http.Error(w, "unknown origin", http.StatusNotFound)
			log.Printf("unknown hostname: %s\n", hostname)
			return
		}

		// データをとってくる
		log.Printf("%s: Send request to origin (failover) (%s:%s)\n", c.nodeId, origin.Ip4, origin.Port)
		res, err := c.fetch(r.Context(), r, origin.Ip4, origin.Port)
		if err != nil {
			http.Error(w, "unknown origin", http.StatusNotFound)
			log.Printf("failed to fetch data from %s", hostname)
			return
		}

		// キャッシュに保存する
		h := res.Header.Clone()
		h.Del("X-Cache")
		h.Del("X-NCDN-PoPCache-NodeId")
		h.Del("X-NCDN-Shield-NodeId")
		body, err := io.ReadAll(res.Body)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		c.cache.Put(key, &types.CacheEntry{
			StatusCode: res.StatusCode,
			Header:     h,
			Body:       body,
		})

		// レスポンスに書き込む
		w.Header().Add("X-Cache", "Miss")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
		return
	}
	defer res.Body.Close()

	// TODO: cache-controlをみる
	if res.StatusCode == http.StatusOK {
		// キャッシュに保存する
		h := res.Header.Clone()
		h.Del("X-Cache")
		h.Del("X-NCDN-PoPCache-NodeId")
		h.Del("X-NCDN-Shield-NodeId")
		body, err := io.ReadAll(res.Body)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		c.cache.Put(key, &types.CacheEntry{
			StatusCode: res.StatusCode,
			Header:     h,
			Body:       body,
		})

		// レスポンスに書き込む
		w.Header().Add("X-Cache", "Miss")
		w.Header().Add("X-NCDN-Shield-NodeId", res.Header.Get("X-NCDN-Shield-NodeId"))
		w.WriteHeader(http.StatusOK)
		w.Write(body)
		return
	}

	http.Error(w, "internal server error", http.StatusInternalServerError)
}

