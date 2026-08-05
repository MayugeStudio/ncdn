package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

const (
	profileGallery = "gallery"

	profileCatalog = "catalog"

	profileDynamic = "dynamic"
)

type variantConfig struct {
	Name  string `json:"name"`
	Width int    `json:"width"` // 0 は原寸
	Label string `json:"label"`
}

type siteConfig struct {
	Id      string `json:"id"`
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
	Host    string `json:"host"`
	Profile string `json:"profile"`

	Accent string `json:"accent"`
	Ink    string `json:"ink"`
	Paper  string `json:"paper"`

	Variants []variantConfig `json:"variants"`
	PerPage  int             `json:"perPage"`

	AllowedWidths []int `json:"allowedWidths"`
}

type imageInfo struct {
	Id           string `json:"id"`
	Title        string `json:"title"`
	File         string `json:"file"`
	Bytes        int    `json:"bytes"`
	Category     string `json:"category"`
	CategorySlug string `json:"categorySlug"`
}

type category struct {
	Slug  string
	Name  string
	Count int
}

type site struct {
	cfg siteConfig
	dir string

	images     []imageInfo
	categories []category

	tmpl *template.Template

	assets map[string][]byte

	derivedMu sync.RWMutex
	derived   map[string][]byte

	totalBytes  int
	uniqueURLs  int
	buildTime   time.Duration
	startedAt   time.Time
	pageCount   int
	variantList []variantConfig

	meterURLsJSON string
}

func loadSite(dir string) (*site, error) {
	var cfg siteConfig
	if err := readJSON(filepath.Join(dir, "site.json"), &cfg); err != nil {
		return nil, err
	}

	var images []imageInfo
	if err := readJSON(filepath.Join(dir, "images.json"), &images); err != nil {
		return nil, fmt.Errorf("%w (先に ./fetch-assets.sh を実行して素材を取得してください)", err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("%s に画像が1件もありません", dir)
	}

	if len(cfg.Variants) == 0 {
		cfg.Variants = []variantConfig{{Name: "full", Width: 0, Label: "原寸"}}
	}

	s := &site{
		cfg:         cfg,
		dir:         dir,
		images:      images,
		assets:      make(map[string][]byte),
		derived:     make(map[string][]byte),
		startedAt:   time.Now(),
		variantList: cfg.Variants,
	}

	start := time.Now()
	if err := s.buildAssets(); err != nil {
		return nil, err
	}
	s.buildTime = time.Since(start)

	s.buildCategories()

	if cfg.PerPage > 0 {
		s.pageCount = (len(images) + cfg.PerPage - 1) / cfg.PerPage
	} else {
		s.pageCount = 1
	}

	urls := s.buildMeterURLs()
	bs, err := json.Marshal(urls)
	if err != nil {
		return nil, err
	}
	s.meterURLsJSON = string(bs)

	s.uniqueURLs = len(urls) + s.pageCount + len(s.categories) + 3

	tmpl, err := template.New("").Funcs(templateFuncs()).ParseGlob(filepath.Join(dir, "templates", "*.gotmpl"))
	if err != nil {
		return nil, fmt.Errorf("テンプレートの読み込みに失敗しました: %w", err)
	}
	s.tmpl = tmpl

	return s, nil
}

func (s *site) buildAssets() error {
	imgDir := filepath.Join(s.dir, "static", "img")

	originals := make(map[string][]byte, len(s.images))

	for i := range s.images {
		info := &s.images[i]

		orig, err := os.ReadFile(filepath.Join(imgDir, info.File))
		if err != nil {
			return fmt.Errorf("画像の読み込みに失敗しました: %w", err)
		}
		info.Bytes = len(orig)

		originals[info.File] = orig
		s.assets["full/"+info.File] = orig
		s.totalBytes += len(orig)
	}

	var (
		mu      sync.Mutex
		firstEr error
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, runtime.NumCPU())

	for _, v := range s.cfg.Variants {
		if v.Width == 0 {
			continue // 原寸は読み込み時に登録済み
		}

		for _, info := range s.images {
			wg.Add(1)
			sem <- struct{}{}

			go func(v variantConfig, info imageInfo) {
				defer wg.Done()
				defer func() { <-sem }()

				resized, err := resizePNG(originals[info.File], v.Width)

				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					if firstEr == nil {
						firstEr = fmt.Errorf("%s の縮小に失敗しました: %w", info.File, err)
					}
					return
				}
				s.assets[v.Name+"/"+info.File] = resized
				s.totalBytes += len(resized)
			}(v, info)
		}
	}

	wg.Wait()
	return firstEr
}

func (s *site) buildMeterURLs() []string {
	var urls []string

	switch s.cfg.Profile {
	case profileCatalog:
		for _, v := range s.cfg.Variants {
			for _, img := range s.images {
				urls = append(urls, "/img/"+v.Name+"/"+img.File)
			}
		}

	case profileDynamic:
		for _, img := range s.images {
			urls = append(urls, "/img/"+img.File)
			for _, w := range s.cfg.AllowedWidths {
				urls = append(urls, fmt.Sprintf("/img/%s?w=%d", img.File, w))
			}
		}

	default:
		for _, img := range s.images {
			urls = append(urls, "/img/"+img.File)
		}
	}

	return urls
}

func (s *site) buildCategories() {
	counts := map[string]*category{}
	var order []string

	for _, img := range s.images {
		if img.CategorySlug == "" {
			continue
		}
		c, ok := counts[img.CategorySlug]
		if !ok {
			c = &category{Slug: img.CategorySlug, Name: img.Category}
			counts[img.CategorySlug] = c
			order = append(order, img.CategorySlug)
		}
		c.Count++
	}

	sort.Strings(order)
	for _, slug := range order {
		s.categories = append(s.categories, *counts[slug])
	}
}

func (s *site) variantBytes(variant, file string) ([]byte, bool) {
	b, ok := s.assets[variant+"/"+file]
	return b, ok
}

func (s *site) derivedBytes(file string, width int) ([]byte, error) {
	key := fmt.Sprintf("%d/%s", width, file)

	s.derivedMu.RLock()
	if b, ok := s.derived[key]; ok {
		s.derivedMu.RUnlock()
		return b, nil
	}
	s.derivedMu.RUnlock()

	orig, ok := s.variantBytes("full", file)
	if !ok {
		return nil, os.ErrNotExist
	}

	resized, err := resizePNG(orig, width)
	if err != nil {
		return nil, err
	}

	s.derivedMu.Lock()
	s.derived[key] = resized
	s.derivedMu.Unlock()

	return resized, nil
}

func (s *site) widthAllowed(w int) bool {
	for _, allowed := range s.cfg.AllowedWidths {
		if allowed == w {
			return true
		}
	}
	return false
}

func (s *site) imagesForPage(page int) []imageInfo {
	if s.cfg.PerPage <= 0 {
		return s.images
	}

	start := (page - 1) * s.cfg.PerPage
	if start >= len(s.images) {
		return nil
	}
	end := min(start+s.cfg.PerPage, len(s.images))

	return s.images[start:end]
}

func (s *site) imagesForCategory(slug string) []imageInfo {
	var out []imageInfo
	for _, img := range s.images {
		if img.CategorySlug == slug {
			out = append(out, img)
		}
	}
	return out
}

func readJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s を開けませんでした: %w", path, err)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(dst); err != nil {
		return fmt.Errorf("%s の解析に失敗しました: %w", path, err)
	}
	return nil
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},
		"kb": func(b int) string {
			return fmt.Sprintf("%.1f KB", float64(b)/1024)
		},
	}
}

func (s *site) logSummary() {
	log.Printf("site %q (profile=%s host=%s): 実画像 %d 枚 / 生成アセット %d 件 / 概算ユニークURL %d / メモリ %.1f MB / 生成 %v",
		s.cfg.Id, s.cfg.Profile, s.cfg.Host,
		len(s.images), len(s.assets), s.uniqueURLs,
		float64(s.totalBytes)/1024/1024, s.buildTime.Round(time.Millisecond))
}
