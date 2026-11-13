package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewDefaultAddress(t *testing.T) {
	s := testStore(t)
	value, err := s.Resolve(false)
	if err != nil || value.APIURL != "https://apinebula.ai/v1" {
		t.Fatal("wrong first-run default", value.APIURL, err)
	}
	if endpoint(defaultAPIURL, "generations") != "https://apinebula.ai/v1/images/generations" {
		t.Fatal("duplicated API version")
	}
}

func TestSavedKeyRevealRequiresLocalExplicitAction(t *testing.T) {
	s := testStore(t)
	if err := s.Save("https://example.com", "synthetic-reveal-test-key", ""); err != nil {
		t.Fatal(err)
	}
	ui := &configurationServer{store: s, host: "127.0.0.1:23456", token: randomID(), shutdown: func() {}}
	origin := "http://" + ui.host
	for _, request := range []struct{ method, token, origin string }{
		{"GET", ui.token, origin}, {"POST", "wrong", origin}, {"POST", ui.token, "https://other.example"}, {"POST", ui.token, ""},
	} {
		w := apiRequest(ui, request.method, "/api/reveal-key", `{}`, request.token, request.origin)
		if w.Code < 400 || strings.Contains(w.Body.String(), "synthetic-reveal-test-key") {
			t.Fatal("unsafe key reveal", w.Code)
		}
	}
	for _, path := range []string{"/", "/api/status"} {
		w := apiRequest(ui, "GET", path, "", ui.token, origin)
		if strings.Contains(w.Body.String(), "synthetic-reveal-test-key") {
			t.Fatal("key exposed before explicit reveal")
		}
	}
	w := apiRequest(ui, "POST", "/api/reveal-key", `{}`, ui.token, origin)
	var value map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value["api_key"] != "synthetic-reveal-test-key" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("explicit local reveal failed")
	}
	status := apiRequest(ui, "GET", "/api/status", "", ui.token, origin)
	if strings.Contains(status.Body.String(), "synthetic-reveal-test-key") {
		t.Fatal("revealing changed status redaction")
	}
}

func TestAutomaticImageModelSelection(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"both", `{"data":[{"id":"gpt-image-2"},{"id":"gpt-image-2.5"}]}`, preferredModel, 200},
		{"preferred-only", `{"data":[{"id":"gpt-image-2.5"}]}`, preferredModel, 200},
		{"flare-with-legacy", `{"data":[{"id":"gpt-image-2"},{"id":"gpt-image-2.5-flare"}]}`, "gpt-image-2.5-flare", 200},
		{"sunburst-with-legacy", `{"data":[{"id":"gpt-image-2"},{"id":"gpt-image-2.5-sunburst"}]}`, "gpt-image-2.5-sunburst", 200},
		{"flare-and-sunburst", `{"data":[{"id":"gpt-image-2.5-sunburst"},{"id":"gpt-image-2.5-flare"},{"id":"gpt-image-2"}]}`, "gpt-image-2.5-flare", 200},
		{"exact-preferred-with-flare", `{"data":[{"id":"gpt-image-2.5-flare"},{"id":"gpt-image-2.5"}]}`, preferredModel, 200},
		{"legacy-only", `{"data":[{"id":"gpt-image-2"}]}`, defaultModel, 200},
		{"new-version-only", `{"data":[{"id":"gpt-image-2.6"}]}`, "gpt-image-2.6", 200},
		{"older-version-only", `{"data":[{"id":"gpt-image-1"}]}`, "gpt-image-1", 200},
		{"variant-only", `{"data":[{"id":"gpt-image-1-mini"}]}`, "gpt-image-1-mini", 200},
		{"family-name", `{"data":[{"id":"gpt-image"}]}`, "gpt-image", 200},
		{"preferred-with-new-version", `{"data":[{"id":"gpt-image-2.6"},{"id":"gpt-image-2.5"}]}`, preferredModel, 200},
		{"legacy-with-older-version", `{"data":[{"id":"gpt-image-1"},{"id":"gpt-image-2"}]}`, defaultModel, 200},
		{"other-models-ordered", `{"data":[{"id":"gpt-image-2.6"},{"id":"gpt-image-1-mini"},{"id":"chat-model"}]}`, "gpt-image-1-mini", 200},
		{"other-models-reversed", `{"data":[{"id":"gpt-image-1-mini"},{"id":"gpt-image-2.6"}]}`, "gpt-image-1-mini", 200},
		{"unrelated-prefix", `{"data":[{"id":"gpt-imagery"}]}`, defaultModel, 200},
		{"hidden", `{"data":[]}`, defaultModel, 200},
		{"unsupported", "", defaultModel, 404},
		{"unauthorized", "", defaultModel, 401},
		{"bad-json", `<html>not an API</html>`, defaultModel, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Error("discovery called a paid image endpoint")
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer provider.Close()
			selector := &imageModelSelector{}
			config := settings{APIURL: provider.URL + "/v1", APIKey: "synthetic-model-key"}
			for range 2 {
				if model := selector.Select(context.Background(), config, "auto", false, time.Second); model != test.want {
					t.Fatal("wrong automatic model", model)
				}
			}
			if calls != 1 {
				t.Fatal("selector did not reuse discovery", calls)
			}
			if model := selector.Select(context.Background(), config, "gpt-image-2", false, time.Second); model != defaultModel || calls != 1 {
				t.Fatal("explicit override ignored")
			}
			config.APIKey = "different-synthetic-key"
			_ = selector.Select(context.Background(), config, "auto", false, time.Second)
			if calls != 2 {
				t.Fatal("discovery reused across different credentials")
			}
		})
	}
}

func TestConfiguredDefaultModelSkipsDiscovery(t *testing.T) {
	for _, configured := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
		t.Run(configured, func(t *testing.T) {
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = io.WriteString(w, `{"data":[{"id":"gpt-image-2"}]}`)
			}))
			defer provider.Close()
			config := settings{APIURL: provider.URL + "/v1", APIKey: "synthetic-model-key", DefaultModel: configured}
			selector := &imageModelSelector{}
			if model := selector.Select(context.Background(), config, "auto", false, time.Second); model != configured || calls != 0 {
				t.Fatal("configured default was not used directly", model, calls)
			}
			if model := selector.Select(context.Background(), config, "exact-request-model", false, time.Second); model != "exact-request-model" || calls != 0 {
				t.Fatal("explicit model did not override configured model", model)
			}
		})
	}
}

func TestModelDryRunAndCanceledDiscovery(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer provider.Close()
	config := settings{APIURL: provider.URL + "/v1", APIKey: "synthetic-key"}
	selector := &imageModelSelector{}
	if selector.Select(context.Background(), config, "auto", true, time.Second) != defaultModel || calls != 0 {
		t.Fatal("dry-run made network requests")
	}
	config.DefaultModel = "gpt-image-2.5-flare"
	if selector.Select(context.Background(), config, "auto", true, time.Second) != config.DefaultModel || calls != 0 {
		t.Fatal("dry-run ignored configured default or made network requests")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if (&imageModelSelector{}).Select(ctx, settings{APIURL: config.APIURL, APIKey: config.APIKey}, "auto", false, time.Second) != defaultModel {
		t.Fatal("unavailable discovery blocked default fallback")
	}
	if (&imageModelSelector{}).Select(ctx, config, "auto", false, time.Second) != config.DefaultModel {
		t.Fatal("configured default was not used when discovery was unavailable")
	}
}

func TestListedModelReachesGenerationEditAndBatch(t *testing.T) {
	for _, wantModel := range []string{preferredModel, "gpt-image-2.6"} {
		t.Run(wantModel, func(t *testing.T) {
			// Isolate the runtime configuration lookup on every supported platform.
			t.Setenv("APPDATA", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if runtime.GOOS == "windows" {
				t.Setenv("USERPROFILE", t.TempDir())
			}
			var probes, images atomic.Int32
			image := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/v1/models" {
					probes.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": wantModel}}})
					return
				}
				images.Add(1)
				model := ""
				if r.URL.Path == "/v1/images/edits" {
					if err := r.ParseMultipartForm(1024 * 1024); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					model = r.FormValue("model")
				} else if r.URL.Path == "/v1/images/generations" {
					var payload map[string]any
					_ = json.NewDecoder(r.Body).Decode(&payload)
					model, _ = payload["model"].(string)
				} else {
					t.Error("unexpected image endpoint", r.URL.Path)
				}
				if model != wantModel {
					t.Error("listed model did not reach image endpoint", model)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": image}}})
			}))
			defer provider.Close()
			t.Setenv("CODEX_API_URL", provider.URL)
			t.Setenv("CODEX_API_KEY", "synthetic-generation-key")
			args := commonArgs{model: "auto", size: defaultSize, quality: defaultQuality, n: 1, outDir: t.TempDir(), maxAttempts: 1, timeout: time.Second}
			result, err := generate("model-test", "generation.png", args)
			if err != nil || result["model"] != wantModel {
				t.Fatal("wrong reported generation model", err)
			}
			input := filepath.Join(t.TempDir(), "input.png")
			_ = os.WriteFile(input, []byte("test-input"), 0600)
			result, err = edit("model-test", []string{input}, "", "edit.png", args)
			if err != nil || result["model"] != wantModel {
				t.Fatal("wrong reported edit model", err)
			}
			before := probes.Load()
			jobs := filepath.Join(t.TempDir(), "jobs.jsonl")
			_ = os.WriteFile(jobs, []byte("{\"prompt\":\"one\",\"out\":\"one.png\"}\n{\"prompt\":\"two\",\"out\":\"two.png\"}\n"), 0600)
			if err := runBatch([]string{"--input", jobs, "--out-dir", t.TempDir(), "--concurrency", "2"}); err != nil {
				t.Fatal(err)
			}
			if probes.Load()-before != 1 || images.Load() != 4 {
				t.Fatal("batch should share model discovery", probes.Load()-before, images.Load())
			}
			args.dryRun = true
			result, err = generate("model-test", "dry.png", args)
			if err != nil || result["model_selection"] != "auto" || images.Load() != 4 || probes.Load() != before+1 {
				t.Fatal("dry-run contacted the service or omitted auto-selection state")
			}
		})
	}
}
