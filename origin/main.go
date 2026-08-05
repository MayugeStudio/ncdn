package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/yzp0n/ncdn/httprps"
)

var nodeId = flag.String("nodeId", "unknown_node", "Name of the node")
var listenAddr = flag.String("listenAddr", ":8888", "Address to listen on")
var siteDir = flag.String("siteDir", ".", "Directory of the site to serve (must contain site.json). Defaults to the legacy single-page origin.")

type requestInfo struct {
	RemoteAddr string
	PopCacheId string
	OriginId   string
}

func dumpRequestInfo(r *http.Request) requestInfo {
	return requestInfo{
		RemoteAddr: r.RemoteAddr,
		PopCacheId: r.Header.Get("X-NCDN-PoPCache-NodeId"),
		OriginId:   *nodeId,
	}
}

func serveIndexHTMLInternal(w http.ResponseWriter, r *http.Request) error {
	tmpl, err := template.New("index.html.gotmpl").ParseFiles("./templates/index.html.gotmpl")
	if err != nil {
		return fmt.Errorf("Failed to parse index.html template: %w", err)
	}

	ri := dumpRequestInfo(r)

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, &ri); err != nil {
		return fmt.Errorf("Failed to execute index.html template: %w", err)
	}

	w.Header().Set("Content-Type", "text/html")
	_, err = w.Write(buf.Bytes())
	if err != nil {
		log.Printf("Failed to write response: %v", err)
		return nil // since it is too late to recover
	}

	return nil
}

func serveIndexHTML(w http.ResponseWriter, r *http.Request) {
	err := serveIndexHTMLInternal(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func serveJsonInternal(w http.ResponseWriter, r *http.Request) error {
	ri := dumpRequestInfo(r)

	bs, err := json.MarshalIndent(ri, "", "  ")
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(bs)
	if err != nil {
		log.Printf("Failed to write response: %v", err)
		return nil // since it is too late to recover
	}

	return nil
}

func serveJson(w http.ResponseWriter, r *http.Request) {
	err := serveJsonInternal(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func registerLegacyRoutes(mux *http.ServeMux) {
	fs := http.FileServer(http.Dir("./static"))

	mux.HandleFunc("/index.html", serveIndexHTML)
	mux.HandleFunc("/json", serveJson)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// redirect to index.html
			http.Redirect(w, r, "/index.html", http.StatusPermanentRedirect)
			return
		}

		fs.ServeHTTP(w, r)
	})
}

func main() {
	flag.Parse()

	mux := http.NewServeMux()

	if _, err := os.Stat(filepath.Join(*siteDir, "site.json")); err == nil {
		s, err := loadSite(*siteDir)
		if err != nil {
			log.Fatalf("サイトの読み込みに失敗しました: %v", err)
		}
		s.logSummary()
		registerSiteRoutes(mux, s)
	} else {
		log.Printf("%s に site.json が無いため、従来の単一ページ origin として起動します", *siteDir)
		registerLegacyRoutes(mux)
	}

	rps := httprps.NewMiddleware(mux)
	http.Handle("/", rps)
	mux.HandleFunc("/rps", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "RPS: %.2f\n", rps.GetRPS())
	})

	log.Printf("Listening on %s...\n", *listenAddr)
	err := http.ListenAndServe(*listenAddr, nil)
	if err != nil {
		log.Fatal(err)
	}
}
