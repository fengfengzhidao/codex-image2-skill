package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// One selector belongs to one CLI invocation. Batch workers share it, so they
// probe once per service/credential pair without persisting credentials/cache.
type imageModelSelector struct {
	mu    sync.Mutex
	cache map[[32]byte]string
}

func listedImageModel(ids []string) string {
	for _, id := range ids {
		if id == preferredModel {
			return preferredModel
		}
	}
	return defaultModel
}

func hasImageModel(ids []string) bool {
	for _, id := range ids {
		if id == preferredModel || id == defaultModel {
			return true
		}
	}
	return false
}

// Model IDs are provider-controlled text. Only return compact, printable IDs;
// never reflect a credential masquerading as a model in an error response.
func displayModelIDs(ids []string, key string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 256 || strings.IndexFunc(id, unicode.IsControl) >= 0 || (key != "" && (id == key || (len(key) >= 8 && strings.Contains(id, key)))) || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	priority := func(id string) int {
		if id == preferredModel {
			return 0
		}
		if id == defaultModel {
			return 1
		}
		return 2
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := priority(result[i]), priority(result[j])
		if a != b {
			return a < b
		}
		return result[i] < result[j]
	})
	return result
}

func fetchModelList(ctx context.Context, base, key string, timeout time.Duration) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := diagnosticClient(timeout).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, nil
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&models) != nil || models.Data == nil {
		return nil, resp.StatusCode, errors.New("invalid model list")
	}
	ids := make([]string, 0, len(models.Data))
	for _, model := range models.Data {
		ids = append(ids, model.ID)
	}
	return ids, resp.StatusCode, nil
}

func (s *imageModelSelector) Select(ctx context.Context, config settings, requested string, dryRun bool, timeout time.Duration) string {
	if requested != "" && requested != "auto" {
		return requested
	}
	// A dry-run never performs discovery or claims that availability was checked.
	if dryRun {
		return defaultModel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cacheKey := sha256.Sum256([]byte(config.APIURL + "\x00" + config.APIKey))
	if model, ok := s.cache[cacheKey]; ok {
		return model
	}
	if timeout <= 0 || timeout > 8*time.Second {
		timeout = 8 * time.Second
	}
	ids, status, err := fetchModelList(ctx, config.APIURL, config.APIKey, timeout)
	model := defaultModel
	if err == nil && status >= 200 && status < 300 {
		model = listedImageModel(ids)
	}
	// Model lists may be hidden or unsupported by image gateways. Discovery
	// failures cannot block the original direct-call fallback.
	if s.cache == nil {
		s.cache = make(map[[32]byte]string)
	}
	s.cache[cacheKey] = model
	return model
}

func selectedImageModel(config settings, args commonArgs) string {
	selector := args.modelSelector
	if selector == nil {
		selector = &imageModelSelector{}
	}
	return selector.Select(context.Background(), config, args.model, args.dryRun, args.timeout)
}
