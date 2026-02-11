package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type cacheEntry struct {
	LastEdited string `json:"last_edited"`
	OutputPath string `json:"output_path"`
}

type runCache struct {
	Pages map[string]cacheEntry `json:"pages"`
}

func defaultCache() runCache {
	return runCache{
		Pages: make(map[string]cacheEntry),
	}
}

func loadCache(path string) (runCache, error) {
	if path == "" {
		return defaultCache(), nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultCache(), nil
		}
		return runCache{}, err
	}

	var cache runCache
	if err := json.Unmarshal(content, &cache); err != nil {
		return runCache{}, err
	}
	if cache.Pages == nil {
		cache.Pages = make(map[string]cacheEntry)
	}
	return cache, nil
}

func saveCache(path string, cache runCache) error {
	if path == "" {
		return nil
	}
	content, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content, 0644)
}

func cacheTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
