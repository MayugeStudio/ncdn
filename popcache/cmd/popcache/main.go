package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/yzp0n/ncdn/httprps"
	"github.com/yzp0n/ncdn/types"
	"github.com/yzp0n/ncdn/popcache/internal"
)

var originConfigPath = flag.String("originConfigPath", "origin_config.json", "Path to the config file for pocsache")
var shieldConfigPath = flag.String("shieldConfigPath", "shield_config.json", "Path to the config file for origin shield")
var listenAddr = flag.String("listenAddr", ":8889", "Address to listen on")
var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")

func main() {
	flag.Parse()

	origins, err := internal.ParseBackends(*originConfigPath)
	if err != nil {
		log.Fatalf("Failed to parse configurations: %v", err)
	}
	shields, err := internal.ParseBackends(*shieldConfigPath)
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
	cs := internal.NewCacheServer(*nodeId, origins, shields, sharedTransport)

	mux.Handle("/", cs)

	log.Printf("Listening on %s...", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}

}
