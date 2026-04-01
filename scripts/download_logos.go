package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const defaultLogoBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8HwQACfsD/QMX52MAAAAASUVORK5CYII="

var defaultLogoData []byte

func init() {
	var err error
	defaultLogoData, err = base64.StdEncoding.DecodeString(defaultLogoBase64)
	if err != nil {
		log.Fatalf("failed to decode embedded default logo: %v", err)
	}
}

func main() {
	tokenFlag := flag.String("token", "", "Logo.dev token (can also be provided through LOGO_DEV_TOKEN)")
	dataDir := flag.String("data-dir", "data", "directory containing company folders")
	outputDir := flag.String("output-dir", "logos", "where downloaded logos are saved")
	concurrency := flag.Int("concurrency", 5, "maximum number of simultaneous downloads")
	flag.Parse()

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("LOGO_DEV_TOKEN")
	}
	if token == "" {
		log.Fatal("logo token is required; set LOGO_DEV_TOKEN or pass -token")
	}

	if *concurrency < 1 {
		*concurrency = 1
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("create output directory %s: %v", *outputDir, err)
	}

	if err := os.WriteFile(filepath.Join(*outputDir, "default.png"), defaultLogoData, 0o644); err != nil {
		log.Fatalf("write default logo: %v", err)
	}

	entries, err := os.ReadDir(*dataDir)
	if err != nil {
		log.Fatalf("read data directory %s: %v", *dataDir, err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	var wg sync.WaitGroup
	sem := make(chan struct{}, *concurrency)
	var mu sync.Mutex
	downloaded := 0
	fallbackCount := 0
	failures := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		company := entry.Name()

		sem <- struct{}{}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()

			result := fetchLogo(name, *outputDir, token, client)

			mu.Lock()
			defer mu.Unlock()

			if result.Err != nil {
				failures++
				log.Printf("❌ %s: %v", name, result.Err)
				return
			}

			downloaded++

			if result.UsedDefault {
				fallbackCount++
				msg := fmt.Sprintf("🟡 %s: saved default logo", name)
				if result.RemoteError != nil {
					msg += fmt.Sprintf(" (logo.dev error: %v)", result.RemoteError)
				}
				log.Println(msg)
				return
			}

			log.Printf("✅ %s: saved %s.png (logo.dev candidate %s)", name, sanitizeFileName(name), result.Candidate)
		}(company)
	}

	wg.Wait()
	log.Printf("completed %d company logos (%d default fallbacks, %d failures)", downloaded, fallbackCount, failures)
}

type fetchResult struct {
	Candidate   string
	UsedDefault bool
	RemoteError error
	Err         error
}

func fetchLogo(company, outputDir, token string, client *http.Client) fetchResult {
	dest := filepath.Join(outputDir, fmt.Sprintf("%s.png", sanitizeFileName(company)))
	candidates := buildCandidates(company)
	var lastErr error

	for _, candidate := range candidates {
		url := fmt.Sprintf("https://img.logo.dev/%s?token=%s&format=png", candidate, token)
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("logo.dev returned %s for %s", resp.Status, candidate)
			resp.Body.Close()
			continue
		}

		if err := writeResponse(resp.Body, dest); err != nil {
			resp.Body.Close()
			return fetchResult{Err: fmt.Errorf("write %s: %w", dest, err)}
		}

		resp.Body.Close()
		return fetchResult{Candidate: candidate}
	}

	if err := os.WriteFile(dest, defaultLogoData, 0o644); err != nil {
		return fetchResult{Err: fmt.Errorf("write default %s: %w", dest, err)}
	}

	return fetchResult{UsedDefault: true, RemoteError: lastErr}
}

func writeResponse(src io.Reader, dest string) error {
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, src)
	return err
}

func buildCandidates(name string) []string {
	slug := slugify(name)
	fallback := strings.ToLower(strings.ReplaceAll(name, " ", ""))
	if slug == "" {
		slug = fallback
	}

	tlds := []string{"com", "net", "io", "ai", "co", "org", "co.in"}
	seen := make(map[string]struct{})
	candidates := make([]string, 0, len(tlds)*2+2)

	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}

	for _, tld := range tlds {
		if slug != "" {
			add(fmt.Sprintf("%s.%s", slug, tld))
		}
		if fallback != "" {
			add(fmt.Sprintf("%s.%s", fallback, tld))
		}
	}

	add(slug)
	add(fallback)

	return candidates
}

func slugify(name string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "logo"
	}

	var builder strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			builder.WriteRune('_')
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
