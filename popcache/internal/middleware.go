package internal

import (
	"crypto/sha256"
	"io"
	"log"
	"net/http"
)

// cacheの識別に使用するためのキーを生成する
func CacheKey(r *http.Request) [32]byte {
	host := r.Host
	port := r.URL.Port()
	uri := r.URL.RequestURI()
	byteArray := make([]byte, 0)

	byteArray = append(byteArray, []byte(host)...)
	byteArray = append(byteArray, []byte(port)...)
	byteArray = append(byteArray, []byte(uri)...)

	hashValue := sha256.Sum256(byteArray)
	return hashValue
}

// TODO: Fresh Stale Missを返す関数を定義する
func CacheAndFetch(next http.RoundTripper, r *http.Request, cache *Cache) (*Object, error) {
	key := CacheKey(r)
	obj, ok := cache.Get(key)
	if ok /* キャッシュヒット */ {
		log.Printf("Cache HIT: (%s:%s%s)\n", r.Host, r.URL.Port(), r.URL.RequestURI())
		obj.Header.Set("X-Cache", "HIT")
		return obj, nil
	}

	/* キャッシュミス */
	log.Printf("Cache MISS: (%s:%s%s)\n", r.Host, r.URL.Port(), r.URL.RequestURI())
	resp, err := next.RoundTrip(r)
	if err != nil {
		return nil, err
	}

	cacheHeader := resp.Header.Clone()
	cacheHeader.Del("X-Cache")
	cacheHeader.Del("X-NCDN-PoPCache-NodeId")
	cacheHeader.Del("X-NCDN-Shield-NodeId")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	newObject := &Object{
		StatusCode: resp.StatusCode,
		Header:     cacheHeader,
		Body:       body,
	}
	cache.Put(key, newObject)

	newObject.Header.Add("X-Cache", "Miss")
	return newObject, nil
}

type RoundTripperFunc func(r *http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Shard selects backends from peers. peers must be the same tier server of the caller.
// It expects the caller to verify that it does not have cache entry for `r`.
func Shard(next http.RoundTripper, peers []*Backend, nodeId string) RoundTripperFunc {
	return func(r *http.Request) (*http.Response, error) {
		// X-Shardがある場合、同じTierのサーバから飛んできているので上流に流す
		if r.Header.Get("X-Shard") != "" {
			r.Header.Del("X-Shard")
			log.Printf("The request is from shard\n")
			return next.RoundTrip(r)
		}
		peer := RendezvousSelect(r, peers)
		// 自分が持つべきデータだった場合は上流に流す
		if peer.NodeId == nodeId {
			log.Printf("The request is for me\n")
			return next.RoundTrip(r)
		}
		r.Header.Add("X-Shard", nodeId)

		resp, err := peer.Fetch(r.Context(), r)
		if err != nil {
			log.Printf("Peer(%s) is down\n", peer.NodeId)
			return next.RoundTrip(r)
		}
		return resp, nil
	}
}

type BackendSelector func(*http.Request) *Backend

func Rendezvous(backends []*Backend) BackendSelector {
	return func(r *http.Request) *Backend {
		return RendezvousSelect(r, backends)
	}
}

func ByHost(lookupTable map[string]*Backend) BackendSelector {
	return func(r *http.Request) *Backend {
		return lookupTable[r.Host]
	}
}

// Forward selects backends chosen by sel.
// It expects the caller to verify taht it does not have cache entry for `r`.
func Forward(next http.RoundTripper, sel BackendSelector, nodeId string) RoundTripperFunc {
	return func(r *http.Request) (*http.Response, error) {
		upstream := sel(r)
		resp, err := upstream.Fetch(r.Context(), r)
		// upstreamがエラーを返した場合、nextに流す
		if err != nil {
			// TODO: Healthを更新？
			log.Printf("Upstream(%s) is down\n", upstream.NodeId)
			return next.RoundTrip(r)
		}
		return resp, nil
	}
}
