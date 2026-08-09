package internal

import (
	"log"
	"net/http"
)

type CacheServer struct {
	nodeId  string
	cache   *Cache
	originLookup map[string]*Backend

	roundTripper http.RoundTripper
}

func NewCacheServer(nodeId string, originLookup map[string]*Backend, roundTripper http.RoundTripper) *CacheServer {
	cache := NewCache(256)

	return &CacheServer{
		nodeId:       nodeId,
		cache:        cache,
		originLookup: originLookup,
		roundTripper: roundTripper,
	}
}

func (c *CacheServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-NCDN-PoPCache-NodeId", c.nodeId)

	// r.Hostからポート番号を取り除く
	hostname := RemovePortFromHost(r.Host)
	r.Host = hostname

	// 登録されていないオリジン宛のリクエストを弾く
	if _, ok := c.originLookup[r.Host]; !ok {
		log.Printf("Unknown host: %s", r.Host)
		http.Error(w, "unknown host", http.StatusNotFound)
		return
	}
	

	obj, err := CacheAndFetch(c.roundTripper, r, c.cache)
	if err != nil {
		log.Printf("CacheAndFetch failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// ヘッダに書き込み
	for k, vs := range obj.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(obj.StatusCode)
	w.Write(obj.Body)
	return
}
