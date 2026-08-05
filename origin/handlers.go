package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"path/filepath"
	"strconv"
	"time"
)

//go:embed cachemeter.js
var cacheMeterJS []byte

//go:embed base.css
var baseCSS []byte

type pageStats struct {
	ImageCount int
	AssetCount int
	UniqueURLs int
	TotalBytes int
	PageCount  int
}

type pageData struct {
	Site       siteConfig
	Node       requestInfo
	Heading    string
	Images     []imageInfo
	Variants   []variantConfig
	Categories []category
	Page       int
	PageCount  int
	ActiveCat  string
	MeterURLs  string
	Stats      pageStats
}

func (s *site) newPageData(r *http.Request, heading string) pageData {
	return pageData{
		Site:       s.cfg,
		Node:       dumpRequestInfo(r),
		Heading:    heading,
		Variants:   s.variantList,
		Categories: s.categories,
		Page:       1,
		PageCount:  s.pageCount,
		MeterURLs:  s.meterURLsJSON,
		Stats: pageStats{
			ImageCount: len(s.images),
			AssetCount: len(s.assets),
			UniqueURLs: s.uniqueURLs,
			TotalBytes: s.totalBytes,
			PageCount:  s.pageCount,
		},
	}
}

func (s *site) render(w http.ResponseWriter, name string, data pageData) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, &data); err != nil {
		log.Printf("テンプレート %s の実行に失敗しました: %v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=10")
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Printf("レスポンスの書き込みに失敗しました: %v", err)
	}
}

func servePNG(w http.ResponseWriter, body []byte, maxAge int) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
	if _, err := w.Write(body); err != nil {
		log.Printf("画像の書き込みに失敗しました: %v", err)
	}
}

func serveJSON(w http.ResponseWriter, v any, cacheControl string) {
	bs, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheControl)
	if _, err := w.Write(bs); err != nil {
		log.Printf("レスポンスの書き込みに失敗しました: %v", err)
	}
}

func registerSiteRoutes(mux *http.ServeMux, s *site) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "ok %s\n", s.cfg.Id)
	})

	mux.HandleFunc("GET /json", serveJson)

	mux.HandleFunc("GET /ncdn-cachemeter.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(cacheMeterJS)
	})

	mux.HandleFunc("GET /ncdn-base.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(baseCSS)
	})

	assetFS := http.FileServer(http.Dir(filepath.Join(s.dir, "static")))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", cacheControl("public, max-age=300", assetFS)))

	mux.HandleFunc("GET /api/manifest", func(w http.ResponseWriter, r *http.Request) {
		serveJSON(w, map[string]any{
			"site":       s.cfg.Id,
			"host":       s.cfg.Host,
			"profile":    s.cfg.Profile,
			"variants":   s.variantList,
			"pageCount":  s.pageCount,
			"uniqueURLs": s.uniqueURLs,
			"images":     s.images,
		}, "public, max-age=60")
	})

	switch s.cfg.Profile {
	case profileGallery:
		registerGalleryRoutes(mux, s)
	case profileCatalog:
		registerCatalogRoutes(mux, s)
	case profileDynamic:
		registerDynamicRoutes(mux, s)
	default:
		log.Fatalf("未知の profile です: %q", s.cfg.Profile)
	}
}

func registerGalleryRoutes(mux *http.ServeMux, s *site) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		d := s.newPageData(r, s.cfg.Name)
		d.Images = s.images
		s.render(w, "index", d)
	})

	mux.HandleFunc("GET /img/{file}", func(w http.ResponseWriter, r *http.Request) {
		body, ok := s.variantBytes("full", r.PathValue("file"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		servePNG(w, body, 60)
	})
}

func registerCatalogRoutes(mux *http.ServeMux, s *site) {
	renderPage := func(w http.ResponseWriter, r *http.Request, page int) {
		if page < 1 || page > s.pageCount {
			http.NotFound(w, r)
			return
		}

		d := s.newPageData(r, fmt.Sprintf("%s — %d / %d ページ", s.cfg.Name, page, s.pageCount))
		d.Images = s.imagesForPage(page)
		d.Page = page
		s.render(w, "index", d)
	}

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, 1)
	})

	mux.HandleFunc("GET /p/{page}", func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.PathValue("page"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		renderPage(w, r, page)
	})

	mux.HandleFunc("GET /c/{slug}", func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		images := s.imagesForCategory(slug)
		if len(images) == 0 {
			http.NotFound(w, r)
			return
		}

		name := slug
		for _, c := range s.categories {
			if c.Slug == slug {
				name = c.Name
			}
		}

		d := s.newPageData(r, fmt.Sprintf("%s — %s", s.cfg.Name, name))
		d.Images = images
		d.ActiveCat = slug
		s.render(w, "index", d)
	})

	mux.HandleFunc("GET /img/{variant}/{file}", func(w http.ResponseWriter, r *http.Request) {
		body, ok := s.variantBytes(r.PathValue("variant"), r.PathValue("file"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		servePNG(w, body, 300)
	})
}

func registerDynamicRoutes(mux *http.ServeMux, s *site) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		d := s.newPageData(r, s.cfg.Name)
		d.Images = s.images
		s.render(w, "index", d)
	})

	mux.HandleFunc("GET /img/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")

		q := r.URL.Query().Get("w")
		if q == "" {
			body, ok := s.variantBytes("full", file)
			if !ok {
				http.NotFound(w, r)
				return
			}
			servePNG(w, body, 60)
			return
		}

		width, err := strconv.Atoi(q)
		if err != nil || !s.widthAllowed(width) {
			http.Error(w, fmt.Sprintf("w= には %v のいずれかを指定してください", s.cfg.AllowedWidths), http.StatusBadRequest)
			return
		}

		body, err := s.derivedBytes(file, width)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		servePNG(w, body, 60)
	})

	mux.HandleFunc("GET /api/random", func(w http.ResponseWriter, r *http.Request) {
		n := 6
		picks := make([]imageInfo, 0, n)
		for i := 0; i < n; i++ {
			picks = append(picks, s.images[rand.Intn(len(s.images))])
		}

		serveJSON(w, map[string]any{
			"servedAt": time.Now().Format(time.RFC3339Nano),
			"originId": *nodeId,
			"picks":    picks,
		}, "no-store")
	})

	mux.HandleFunc("GET /live/now", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s origin=%s\n", time.Now().Format(time.RFC3339Nano), *nodeId)
	})
}

func cacheControl(v string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", v)
		next.ServeHTTP(w, r)
	})
}
