package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/yzp0n/ncdn/httprps"
	"github.com/yzp0n/ncdn/popcache/internal"
	"github.com/yzp0n/ncdn/types"
	"golang.org/x/sync/singleflight"
)

var g singleflight.Group

var shieldConfigPath = flag.String("shieldConfigPath", "config/shield.yaml", "Path to the config file for shields")
var originConfigPath = flag.String("originConfigPath", "config/origin.yaml", "Path to the config file for origins")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

func main() {
	flag.Parse()

	sharedTransport := &http.Transport{
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
	}

	origins, err := internal.ParseBackends(*originConfigPath, sharedTransport)
	if err != nil {
		log.Fatalf("Failed to parse configs: %v", err)
	}
	shields, err := internal.ParseBackends(*shieldConfigPath, sharedTransport)
	if err != nil {
		log.Fatalf("Failed to parse configs: %v", err)
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

	noUpstreamAvailable := internal.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("no upstream available")
	})

	originLookup := make(map[string]*internal.Backend)
	for _, origin := range origins {
		originLookup[origin.Hostname] = origin
	}

	forwardToOrigin := internal.Forward(noUpstreamAvailable, internal.ByHost(originLookup), *nodeId)
	shieldShard := internal.Shard(forwardToOrigin, shields, *nodeId)

	cs := internal.NewCacheServer(*nodeId, originLookup, shieldShard)

	mux.Handle("/", cs)

	log.Printf("%s starting...\n", *nodeId)
	log.Printf("Listening on %s...\n", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}
}
