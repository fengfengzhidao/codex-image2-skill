package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSaveChecksPermissionsBeforeReplacingConfig(t *testing.T) {
	for _, test := range []struct {
		name, body, model    string
		status               int
		allowed, confirmable bool
	}{
		{"preferred", `{"data":[{"id":"chat-only"},{"id":"gpt-image-2.5"}]}`, preferredModel, 200, true, false},
		{"legacy", `{"data":[{"id":"gpt-image-2"}]}`, defaultModel, 200, true, false},
		{"no-image-access", `{"data":[{"id":"chat-only"}]}`, "", 200, false, true},
		{"empty", `{"data":[]}`, "", 200, false, true},
		{"hidden-list", "", "", 404, false, true},
		{"incompatible-list", `<html>not a model list</html>`, "", 200, false, true},
		{"invalid-key", `{"error":"candidate-secret-key"}`, "", 401, false, false},
		{"forbidden", `{"error":"candidate-secret-key"}`, "", 403, false, false},
		{"rate-limited", "", "", 429, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testStore(t)
			if err := s.Save("https://old.example", "old-valid-key"); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(s.path)
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Error("save attempted a paid image request")
				}
				if r.Header.Get("Authorization") != "Bearer candidate-secret-key" {
					t.Error("wrong key used for save validation")
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer provider.Close()
			ui := &configurationServer{store: s, host: "127.0.0.1:34567", token: randomID(), shutdown: func() {}}
			body := map[string]any{"api_url": provider.URL, "api_key": "candidate-secret-key"}
			data, _ := json.Marshal(body)
			w := apiRequest(ui, "POST", "/api/save", string(data), ui.token, "http://"+ui.host)
			var result connectionResult
			if json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatal("invalid save response")
			}
			if result.Saved != test.allowed || result.RequiresConfirmation != test.confirmable || result.Model != test.model {
				t.Fatalf("wrong save decision: %+v", result)
			}
			if calls != 1 || strings.Contains(w.Body.String(), "candidate-secret-key") {
				t.Fatal("unsafe validation response")
			}
			if !test.allowed {
				after, _ := os.ReadFile(s.path)
				if !bytes.Equal(after, original) {
					t.Fatal("invalid or unconfirmed save replaced existing configuration")
				}
				body["confirm_unverified"] = true
				data, _ = json.Marshal(body)
				w = apiRequest(ui, "POST", "/api/save", string(data), ui.token, "http://"+ui.host)
				_ = json.Unmarshal(w.Body.Bytes(), &result)
				if result.Saved != test.confirmable {
					t.Fatal("warning confirmation bypassed a credential error or could not save an unverified service")
				}
				if !test.confirmable && w.Code != 422 {
					t.Fatal("authentication failure was not rejected")
				}
			}
			value, err := s.Resolve(true)
			if err != nil {
				t.Fatal(err)
			}
			wantKey := "old-valid-key"
			if test.allowed || test.confirmable {
				wantKey = "candidate-secret-key"
			}
			if value.APIKey != wantKey {
				t.Fatal("wrong stored credential after validation")
			}
		})
	}
}

func TestConnectionReturnsCurrentKeysModelList(t *testing.T) {
	key := "synthetic-list-secret-key"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("models not scoped to the submitted key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "chat-z"}, {"id": "gpt-image-2"}, {"id": "gpt-image-2.5"}, {"id": "chat-a"}, {"id": "gpt-image-2.5"}, {"id": key}, {"id": "\n"}}})
	}))
	defer provider.Close()
	result := checkConnection(context.Background(), provider.URL+"/v1", key)
	if !result.OK || !result.ModelsChecked || result.Model != preferredModel {
		t.Fatal("invalid model-list result", result)
	}
	want := []string{preferredModel, defaultModel, "chat-a", "chat-z"}
	if !reflect.DeepEqual(result.Models, want) {
		t.Fatal("missing, duplicated or unsafe display model IDs", result.Models)
	}
}

func TestMissingImageModelsAreWarnedWithoutClaimingUnsupported(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"id":"chat-model"}]}`} {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		result := checkConnection(context.Background(), provider.URL+"/v1", "synthetic-key")
		provider.Close()
		if result.OK || result.Level != "warning" || !result.ModelsChecked || result.Model != "" || !strings.Contains(result.Message, "gpt-image-2.5") {
			t.Fatal("missing image permissions were silently accepted", result)
		}
	}
}
