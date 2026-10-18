package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type memoryVault struct {
	keys map[string]string
	fail bool
}

func (v *memoryVault) Seal(key string) (storedSecret, error) {
	if v.fail {
		return storedSecret{}, errors.New("vault unavailable")
	}
	ref := randomID()
	v.keys[ref] = key
	return storedSecret{Kind: "test-vault", Data: ref}, nil
}
func (v *memoryVault) Open(secret storedSecret) (string, error) {
	key, ok := v.keys[secret.Data]
	if !ok {
		return "", errors.New("missing")
	}
	return key, nil
}
func (v *memoryVault) Delete(secret storedSecret) { delete(v.keys, secret.Data) }
func testStore(t *testing.T) *configStore {
	t.Helper()
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("CODEX_API_URL", "")
	return &configStore{path: filepath.Join(t.TempDir(), "settings", "config.json"), vault: &memoryVault{keys: map[string]string{}}}
}

func TestNormalizeAPIAddress(t *testing.T) {
	for input, want := range map[string]string{
		" https://example.com/ ":                    "https://example.com/v1",
		"example.com":                               "https://example.com/v1",
		"https://example.com/v1/":                   "https://example.com/v1",
		"https://example.com/v1/images/generations": "https://example.com/v1",
		"https://example.com/api/v1/images/edits":   "https://example.com/api/v1",
		"https://example.com/v1/models":             "https://example.com/v1",
		"http://127.0.0.1:8080":                     "http://127.0.0.1:8080/v1",
		"http://[::1]:8080/":                        "http://[::1]:8080/v1",
	} {
		got, err := normalizeAPIBase(input)
		if err != nil || got != want {
			t.Errorf("normalize %q = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "http://example.com", "ftp://example.com", "https://user:password@example.com", "https://example.com?key=secret", "https://example.com/#secret", "https://a b.com", "https://example.com:bad"} {
		if _, err := normalizeAPIBase(input); err == nil {
			t.Errorf("accepted invalid address %q", input)
		}
	}
}

func TestConfigPriorityAndFreshRead(t *testing.T) {
	s := testStore(t)
	if _, err := s.Resolve(true); !errors.Is(err, errNotConfigured) {
		t.Fatalf("missing config: %v", err)
	}
	t.Setenv("CODEX_API_URL", "https://old.example")
	t.Setenv("CODEX_API_KEY", "legacy-key")
	value, err := s.Resolve(true)
	if err != nil || value.Source != "environment" {
		t.Fatal("legacy config unavailable")
	}
	if err := s.Save("https://new.example", "new-key"); err != nil {
		t.Fatal(err)
	}
	value, err = s.Resolve(true)
	if err != nil || value.APIKey != "new-key" || value.APIURL != "https://new.example/v1" || value.Source != "local" {
		t.Fatalf("local config did not override environment: %+v %v", value, err)
	}
	if err := s.Save("https://next.example", "next-key"); err != nil {
		t.Fatal(err)
	}
	value, err = s.Resolve(true)
	if err != nil || value.APIKey != "next-key" {
		t.Fatal("configuration was cached instead of reloaded")
	}
	data, _ := os.ReadFile(s.path)
	if bytes.Contains(data, []byte("next-key")) {
		t.Fatal("plaintext key written to config")
	}
	status, _ := json.Marshal(s.Status())
	if bytes.Contains(status, []byte("next-key")) {
		t.Fatal("status leaked key")
	}
	if err := os.WriteFile(s.path, []byte(`{"version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(true); err == nil {
		t.Fatal("broken local config silently fell back to environment")
	}
}

func TestFailedSavePreservesExistingConfiguration(t *testing.T) {
	s := testStore(t)
	if err := s.Save("https://example.com", "original-key"); err != nil {
		t.Fatal(err)
	}
	s.vault.(*memoryVault).fail = true
	if s.Save("https://other.example", "replacement-key") == nil {
		t.Fatal("save should fail")
	}
	value, err := s.Resolve(true)
	if err != nil || value.APIKey != "original-key" {
		t.Fatal("failed save destroyed configuration")
	}
	if err := s.Save("https://example.com", "key\nheader"); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestWindowsEncryptionRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI integration")
	}
	s := testStore(t)
	s.vault = platformVault{}
	if err := s.Save("https://example.com", "fake-key-for-dpapi-test"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.path)
	if bytes.Contains(data, []byte("fake-key-for-dpapi-test")) {
		t.Fatal("DPAPI stored plaintext")
	}
	value, err := s.Resolve(true)
	if err != nil || value.APIKey != "fake-key-for-dpapi-test" {
		t.Fatalf("DPAPI roundtrip failed: %v", err)
	}
	if err := s.Save("https://example.com", "replacement-dpapi-key"); err != nil {
		t.Fatal("atomic replacement failed", err)
	}
	value, err = s.Resolve(true)
	if err != nil || value.APIKey != "replacement-dpapi-key" {
		t.Fatal("replacement did not persist")
	}
}

func apiRequest(ui *configurationServer, method, path, body, token, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+ui.host+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	return w
}

func TestConfigurationHTTPBoundaryAndSave(t *testing.T) {
	s := testStore(t)
	closed := false
	ui := &configurationServer{store: s, host: "127.0.0.1:12345", token: randomID(), shutdown: func() { closed = true }}
	origin := "http://" + ui.host
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-image-2"}]}`)
	}))
	defer provider.Close()
	bodyData, _ := json.Marshal(map[string]string{"api_url": provider.URL, "api_key": "only-user-knows-this"})
	body := string(bodyData)
	for _, bad := range []struct{ token, origin string }{{"wrong", origin}, {ui.token, "https://attacker.example"}, {ui.token, ""}} {
		w := apiRequest(ui, "POST", "/api/save", body, bad.token, bad.origin)
		if w.Code < 400 {
			t.Fatal("cross-origin or unauthorized request accepted")
		}
	}
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatal("rejected request wrote configuration")
	}
	page := apiRequest(ui, "GET", "/", "", "", "")
	if page.Code != 200 || !strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || strings.Contains(page.Body.String(), "{{NONCE}}") {
		t.Fatal("page security headers or nonce missing")
	}
	req := httptest.NewRequest("GET", "http://attacker.example/", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
	w = apiRequest(ui, "POST", "/api/save", body, ui.token, origin)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(ui, "GET", "/api/status", "", ui.token, origin)
	if strings.Contains(w.Body.String(), "only-user-knows-this") || !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatal("unsafe or invalid status", w.Body.String())
	}
	keepData, _ := json.Marshal(map[string]string{"api_url": provider.URL, "api_key": ""})
	w = apiRequest(ui, "POST", "/api/save", string(keepData), ui.token, origin)
	if w.Code != 200 {
		t.Fatal("cannot preserve saved key")
	}
	w = apiRequest(ui, "POST", "/api/save", `{"api_url":"https://different.example","api_key":""}`, ui.token, origin)
	if w.Code != 400 {
		t.Fatal("old key silently moved to another service")
	}
	w = apiRequest(ui, "POST", "/api/save", body+body, ui.token, origin)
	if w.Code != 400 {
		t.Fatal("multiple JSON values accepted")
	}
	w = apiRequest(ui, "POST", "/api/test-image", body, ui.token, origin)
	if w.Code != 400 {
		t.Fatal("paid generation accepted without confirmation")
	}
	w = apiRequest(ui, "POST", "/api/close", `{}`, ui.token, origin)
	if w.Code != 200 || !closed {
		t.Fatal("close did not stop server")
	}
}

func TestDiagnosticsDoNotGenerateOrLeakCredentials(t *testing.T) {
	key := "secret-reflected-by-provider"
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Error("connection check attempted image generation")
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing authorization")
		}
		w.WriteHeader(401)
		_, _ = io.WriteString(w, key)
	}))
	defer provider.Close()
	result := checkConnection(context.Background(), provider.URL+"/v1", key)
	if calls != 1 || result.OK || strings.Contains(result.Message, key) {
		t.Fatal("invalid or unsafe diagnostic", result)
	}
	for _, code := range []int{404, 405} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		result := checkConnection(context.Background(), server.URL+"/v1", key)
		server.Close()
		if result.OK || result.Level != "warning" {
			t.Fatal("unsupported model-list endpoint treated as invalid credentials")
		}
	}
}

func TestDiagnosticsRedirectAndModelVisibility(t *testing.T) {
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer origin.Close()
	result := checkConnection(context.Background(), origin.URL+"/v1", "test-key")
	if redirected || result.OK {
		t.Fatal("credentialed diagnostic followed redirect")
	}
	for _, body := range []string{`{"data":[{"id":"gpt-image-2"}]}`, `{"data":[{"id":"gpt-image-2.5"}]}`} {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		result := checkConnection(context.Background(), provider.URL+"/v1", "test-key")
		provider.Close()
		if !result.OK || !strings.Contains(result.Message, "实际生图") {
			t.Fatal("model list misrepresents image support", result)
		}
	}
}

func TestPaidImageDiagnosticOneRequest(t *testing.T) {
	calls := 0
	image := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-image-2.5"}]}`)
			return
		}
		calls++
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if r.Method != "POST" || r.URL.Path != "/v1/images/generations" || payload["n"] != float64(1) || payload["quality"] != "low" || payload["model"] != preferredModel {
			t.Error("unexpected paid test request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": image}}})
	}))
	defer provider.Close()
	result := checkImage(context.Background(), provider.URL+"/v1", "test-key")
	if !result.OK || calls != 1 || result.Model != preferredModel || !strings.HasPrefix(result.Image, "data:image/png;base64,") {
		t.Fatal("image test failed", result)
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(404)
			return
		}
		calls++
		w.WriteHeader(524)
	}))
	defer failed.Close()
	result = checkImage(context.Background(), failed.URL+"/v1", "test-key")
	if result.OK || calls != 2 {
		t.Fatal("paid diagnostic unexpectedly retried")
	}
}

func TestGenerationEditAndBatchUseSavedConfiguration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native credential integration is tested on Windows")
	}
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("CODEX_API_URL", "https://obsolete.example")
	t.Setenv("CODEX_API_KEY", "obsolete-environment-key")
	calls := 0
	image := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-image-2.5"}]}`)
			return
		}
		calls++
		if r.Header.Get("Authorization") != "Bearer saved-test-key" {
			t.Error("generation used environment key instead of saved key")
		}
		if r.URL.Path == "/v1/images/edits" {
			if err := r.ParseMultipartForm(1024 * 1024); err != nil {
				t.Error(err)
			}
			if r.FormValue("prompt") != "edit-test" {
				t.Error("edit prompt changed")
			}
			defer r.MultipartForm.RemoveAll()
		} else if r.URL.Path != "/v1/images/generations" {
			t.Error("invalid endpoint", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": image}}})
	}))
	defer provider.Close()
	s, err := defaultConfigStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(provider.URL, "saved-test-key"); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	args := commonArgs{model: defaultModel, size: defaultSize, quality: defaultQuality, n: 1, outDir: outDir, maxAttempts: 1, timeout: 5 * time.Second}
	if _, err := generate("test", "generated.png", args); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "input.png")
	_ = os.WriteFile(input, []byte("\x89PNG\r\n\x1a\n"), 0600)
	if _, err := edit("edit-test", []string{input}, "", "edited.png", args); err != nil {
		t.Fatal(err)
	}
	jobs := filepath.Join(t.TempDir(), "jobs.jsonl")
	_ = os.WriteFile(jobs, []byte("{\"prompt\":\"batch-test\",\"out\":\"batch.png\"}\n"), 0600)
	if err := runBatch([]string{"--input", jobs, "--out-dir", outDir, "--concurrency", "1"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"generated.png", "edited.png", "batch.png"} {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil || string(data) != "\x89PNG\r\n\x1a\n" {
			t.Fatal("missing or changed image", name, err)
		}
	}
	if calls != 3 {
		t.Fatal("unexpected generation calls", calls)
	}
	args.dryRun = true
	if _, err := generate("dry-run", "dry.png", args); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("dry-run contacted provider")
	}
}
