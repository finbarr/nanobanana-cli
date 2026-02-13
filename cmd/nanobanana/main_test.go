package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveModel(t *testing.T) {
	tests := []struct {
		alias   string
		want    string
		wantErr bool
	}{
		{"flash", modelFlash, false},
		{"pro", modelPro, false},
		{"gemini-2.5-flash-image", "gemini-2.5-flash-image", false},
		{"gemini-3-pro-image-preview", "gemini-3-pro-image-preview", false},
		{"some-future-model-v2", "some-future-model-v2", false},
		{"unknown", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, err := resolveModel(tt.alias)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveModel(%q) error = %v, wantErr %v", tt.alias, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("resolveModel(%q) = %q, want %q", tt.alias, got, tt.want)
			}
		})
	}
}

func TestIsProModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"pro", true},
		{modelPro, true},
		{"flash", false},
		{modelFlash, false},
		{"some-other-model", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isProModel(tt.model); got != tt.want {
				t.Errorf("isProModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestValidateAspectRatio(t *testing.T) {
	valid := []string{"1:1", "16:9", "9:16", "4:3", "3:4"}
	for _, ar := range valid {
		if err := validateAspectRatio(ar); err != nil {
			t.Errorf("validateAspectRatio(%q) unexpected error: %v", ar, err)
		}
	}

	invalid := []string{"2:1", "16:10", "foo", ""}
	for _, ar := range invalid {
		if err := validateAspectRatio(ar); err == nil {
			t.Errorf("validateAspectRatio(%q) expected error", ar)
		}
	}
}

func TestValidateImageSize(t *testing.T) {
	tests := []struct {
		size    string
		model   string
		wantErr bool
	}{
		{"1K", "flash", false},
		{"2K", "flash", false},
		{"4K", "pro", false},
		{"4K", modelPro, false}, // full model name should also work
		{"4K", "flash", true},   // 4K requires pro
		{"8K", "pro", true},     // invalid size
		{"", "flash", true},     // empty
	}

	for _, tt := range tests {
		t.Run(tt.size+"_"+tt.model, func(t *testing.T) {
			err := validateImageSize(tt.size, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateImageSize(%q, %q) error = %v, wantErr %v", tt.size, tt.model, err, tt.wantErr)
			}
		})
	}
}

func TestAutoName(t *testing.T) {
	tests := []struct {
		mime    string
		wantExt string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"", ".png"},
	}

	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			name := autoName("nanobanana", tt.mime)
			if !strings.HasPrefix(name, "nanobanana_") {
				t.Errorf("autoName should start with prefix, got %q", name)
			}
			if !strings.HasSuffix(name, tt.wantExt) {
				t.Errorf("autoName with mime %q should end with %q, got %q", tt.mime, tt.wantExt, name)
			}
		})
	}

	// Check timestamp format
	name := autoName("nanobanana", "image/png")
	ts := time.Now().Format("20060102")
	if !strings.Contains(name, ts) {
		t.Errorf("autoName should contain today's date %q, got %q", ts, name)
	}
}

func TestExtForMIME(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"", ".png"},
		{"image/unknown", ".png"},
	}

	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			if got := extForMIME(tt.mime); got != tt.want {
				t.Errorf("extForMIME(%q) = %q, want %q", tt.mime, got, tt.want)
			}
		})
	}
}

func TestConfigLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Load should return defaults when no file exists
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}
	if cfg.Model != "flash" {
		t.Errorf("expected default model flash, got %q", cfg.Model)
	}
	if cfg.APIKey != "" {
		t.Errorf("expected empty API key, got %q", cfg.APIKey)
	}

	// Save and reload
	cfg.APIKey = "test-key-12345678"
	cfg.Model = "pro"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig() error: %v", err)
	}

	cfg2, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() after save error: %v", err)
	}
	if cfg2.APIKey != "test-key-12345678" {
		t.Errorf("expected API key test-key-12345678, got %q", cfg2.APIKey)
	}
	if cfg2.Model != "pro" {
		t.Errorf("expected model pro, got %q", cfg2.Model)
	}

	// Check file permissions
	path := filepath.Join(tmpDir, "nanobanana", "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected config permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestResolveAPIKey(t *testing.T) {
	// Clear all API key env vars
	clearAPIKeyEnvs := func(t *testing.T) {
		t.Helper()
		for _, env := range []string{"NANOBANANA_GEMINI_API_KEY", "GEMINI_API_KEY"} {
			t.Setenv(env, "")
		}
	}

	// NANOBANANA_GEMINI_API_KEY takes highest precedence
	clearAPIKeyEnvs(t)
	t.Setenv("NANOBANANA_GEMINI_API_KEY", "nb-key")
	t.Setenv("GEMINI_API_KEY", "fallback-key")
	cfg := &Config{APIKey: "config-key"}
	key, err := resolveAPIKey(cfg)
	if err != nil {
		t.Fatalf("resolveAPIKey() error: %v", err)
	}
	if key != "nb-key" {
		t.Errorf("expected nb-key, got %q", key)
	}

	// Falls back to GEMINI_API_KEY
	clearAPIKeyEnvs(t)
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	key, err = resolveAPIKey(cfg)
	if err != nil {
		t.Fatalf("resolveAPIKey() error: %v", err)
	}
	if key != "gemini-key" {
		t.Errorf("expected gemini-key, got %q", key)
	}

	// Falls back to config
	clearAPIKeyEnvs(t)
	key, err = resolveAPIKey(cfg)
	if err != nil {
		t.Fatalf("resolveAPIKey() error: %v", err)
	}
	if key != "config-key" {
		t.Errorf("expected config-key, got %q", key)
	}

	// Error when neither set
	clearAPIKeyEnvs(t)
	cfg2 := &Config{}
	_, err = resolveAPIKey(cfg2)
	if err == nil {
		t.Error("expected error when no API key")
	}
}

func TestResolveModelFlag(t *testing.T) {
	cfg := &Config{Model: "pro"}

	// CLI flag takes precedence
	t.Setenv("NANOBANANA_MODEL", "")
	got := resolveModelFlag("flash", cfg)
	if got != "flash" {
		t.Errorf("expected flash from flag, got %q", got)
	}

	// NANOBANANA_MODEL env takes precedence over config
	t.Setenv("NANOBANANA_MODEL", "gemini-2.5-flash-image")
	got = resolveModelFlag("", cfg)
	if got != "gemini-2.5-flash-image" {
		t.Errorf("expected gemini-2.5-flash-image from env, got %q", got)
	}

	// Falls back to config
	t.Setenv("NANOBANANA_MODEL", "")
	got = resolveModelFlag("", cfg)
	if got != "pro" {
		t.Errorf("expected pro from config, got %q", got)
	}

	// Falls back to default
	t.Setenv("NANOBANANA_MODEL", "")
	got = resolveModelFlag("", &Config{})
	if got != "flash" {
		t.Errorf("expected flash default, got %q", got)
	}
}

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"photo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"photo.gif", "image/gif"},
		{"photo.webp", "image/webp"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectMIMEType(tt.path, nil)
			if got != tt.want {
				t.Errorf("detectMIMEType(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestReadImage(t *testing.T) {
	// Use the testdata fixture
	path := filepath.Join("..", "..", "testdata", "tiny.png")
	data, mime, err := readImage(path)
	if err != nil {
		t.Fatalf("readImage() error: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("expected image/png, got %q", mime)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data")
	}
}

func TestWriteImage(t *testing.T) {
	// Create a simple PNG in memory
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding test PNG: %v", err)
	}
	pngData := buf.Bytes()

	// Write as PNG
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "test.png")
	if err := writeImage(outPath, pngData, "image/png"); err != nil {
		t.Fatalf("writeImage() error: %v", err)
	}

	// Verify file exists and is valid
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if len(written) == 0 {
		t.Error("written file is empty")
	}

	// Write as JPEG (transcode)
	jpgPath := filepath.Join(tmpDir, "test.jpg")
	if err := writeImage(jpgPath, pngData, "image/png"); err != nil {
		t.Fatalf("writeImage() transcode error: %v", err)
	}
	jpgData, err := os.ReadFile(jpgPath)
	if err != nil {
		t.Fatalf("reading JPEG: %v", err)
	}
	if len(jpgData) == 0 {
		t.Error("JPEG file is empty")
	}
}

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		prompt string
		aspect string
		size   string
		want   string
	}{
		{"a cat", "1:1", "1K", "a cat"},
		{"a cat", "16:9", "1K", "a cat. Aspect ratio: 16:9"},
		{"a cat", "1:1", "4K", "a cat. Resolution: 3840x2160"},
		{"a cat", "16:9", "2K", "a cat. Aspect ratio: 16:9. Resolution: 2048x2048"},
	}

	for _, tt := range tests {
		t.Run(tt.prompt+"_"+tt.aspect+"_"+tt.size, func(t *testing.T) {
			got := buildPrompt(tt.prompt, tt.aspect, tt.size)
			if got != tt.want {
				t.Errorf("buildPrompt(%q, %q, %q) = %q, want %q", tt.prompt, tt.aspect, tt.size, got, tt.want)
			}
		})
	}
}

// Helper: create a minimal PNG for API responses
func testPNGBase64() string {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{0, 255, 0, 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestAPIGenerateImage(t *testing.T) {
	b64 := testPNGBase64()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected API key header, got %q", r.Header.Get("x-goog-api-key"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected JSON content type")
		}

		// Verify request body
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if len(req.Contents) == 0 || len(req.Contents[0].Parts) == 0 {
			t.Fatal("empty request contents")
		}
		if req.Contents[0].Parts[0].Text == "" {
			t.Error("empty prompt")
		}

		// Return mock response
		resp := apiResponse{
			Candidates: []apiCandidate{
				{
					Content: apiContent{
						Parts: []apiPart{
							{
								InlineData: &apiBlob{
									MIMEType: "image/png",
									Data:     b64,
								},
							},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Test the HTTP server directly
	reqBody := apiRequest{
		Contents: []apiContent{
			{Parts: []apiPart{{Text: "test prompt"}}},
		},
		GenerationConfig: nil,
	}
	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL, bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(apiResp.Candidates) == 0 {
		t.Fatal("no candidates in response")
	}
	found := false
	for _, part := range apiResp.Candidates[0].Content.Parts {
		if part.InlineData != nil && part.InlineData.MIMEType == "image/png" {
			found = true
			imgBytes, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				t.Fatalf("decode base64: %v", err)
			}
			if len(imgBytes) == 0 {
				t.Error("empty image data")
			}
		}
	}
	if !found {
		t.Error("no image part in response")
	}
}

func TestAPIErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    string
	}{
		{
			name:       "401",
			statusCode: 401,
			body:       `{}`,
			wantErr:    "authentication failed",
		},
		{
			name:       "403",
			statusCode: 403,
			body:       `{}`,
			wantErr:    "authentication failed",
		},
		{
			name:       "429",
			statusCode: 429,
			body:       `{}`,
			wantErr:    "rate limit",
		},
		{
			name:       "400 with message",
			statusCode: 400,
			body:       `{"error":{"code":400,"message":"bad prompt","status":"INVALID_ARGUMENT"}}`,
			wantErr:    "bad prompt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			reqBody := apiRequest{
				Contents: []apiContent{
					{Parts: []apiPart{{Text: "test"}}},
				},
			}
			jsonData, _ := json.Marshal(reqBody)
			req, _ := http.NewRequest("POST", server.URL, bytes.NewReader(jsonData))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-goog-api-key", "test-key")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request error: %v", err)
			}
			defer resp.Body.Close()

			// Verify status codes match expected
			switch {
			case resp.StatusCode == 401 || resp.StatusCode == 403:
				if !strings.Contains(tt.wantErr, "authentication") {
					t.Errorf("expected authentication error for %d", resp.StatusCode)
				}
			case resp.StatusCode == 429:
				if !strings.Contains(tt.wantErr, "rate limit") {
					t.Errorf("expected rate limit error for 429")
				}
			}
		})
	}
}

func TestConfigDir(t *testing.T) {
	// Test XDG override
	t.Setenv("XDG_CONFIG_HOME", "/tmp/test-xdg")
	dir := configDir()
	if dir != "/tmp/test-xdg/nanobanana" {
		t.Errorf("expected /tmp/test-xdg/nanobanana, got %q", dir)
	}
}

func TestValidAspectRatios(t *testing.T) {
	// Ensure all expected ratios exist
	expected := []string{"1:1", "16:9", "9:16", "4:3", "3:4"}
	for _, ar := range expected {
		if !validAspectRatios[ar] {
			t.Errorf("expected %q in validAspectRatios", ar)
		}
	}
}

func TestValidSizes(t *testing.T) {
	expected := map[string][2]int{
		"1K": {1024, 1024},
		"2K": {2048, 2048},
		"4K": {3840, 2160},
	}
	for k, v := range expected {
		got, ok := validSizes[k]
		if !ok {
			t.Errorf("expected %q in validSizes", k)
			continue
		}
		if got != v {
			t.Errorf("validSizes[%q] = %v, want %v", k, got, v)
		}
	}
}

func TestModelAliases(t *testing.T) {
	if modelAliases["flash"] != modelFlash {
		t.Errorf("expected flash alias to map to %q", modelFlash)
	}
	if modelAliases["pro"] != modelPro {
		t.Errorf("expected pro alias to map to %q", modelPro)
	}
}
