package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/term"
)

var Version = "dev"

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// Model aliases
const (
	modelFlash  = "gemini-2.5-flash-image"
	modelPro    = "gemini-3-pro-image-preview"
	apiBaseURL  = "https://generativelanguage.googleapis.com/v1beta/models"
	httpTimeout = 120 * time.Second
)

// Model alias map
var modelAliases = map[string]string{
	"flash": modelFlash,
	"pro":   modelPro,
}

// Valid aspect ratios
var validAspectRatios = map[string]bool{
	"1:1":  true,
	"16:9": true,
	"9:16": true,
	"4:3":  true,
	"3:4":  true,
}

// Valid image sizes and their dimensions
var validSizes = map[string][2]int{
	"1K": {1024, 1024},
	"2K": {2048, 2048},
	"4K": {3840, 2160},
}

// quiet suppresses info/spinner output when true
var quiet bool

// --- Config ---

type Config struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

func configDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "nanobanana")
	}
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "nanobanana")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nanobanana")
}

func configPath() string {
	return filepath.Join(configDir(), "config.toml")
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		Model: "flash",
	}
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

func saveConfig(cfg *Config) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(configPath(), buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

func resolveAPIKey(cfg *Config) (string, error) {
	// Match the official nanobanana Gemini extension env var precedence
	for _, env := range []string{"NANOBANANA_GEMINI_API_KEY", "GEMINI_API_KEY"} {
		if key := os.Getenv(env); key != "" {
			return key, nil
		}
	}
	if cfg.APIKey != "" {
		return cfg.APIKey, nil
	}
	return "", fmt.Errorf("no API key found. Set NANOBANANA_GEMINI_API_KEY or run: nanobanana setup")
}

// resolveModelFlag returns the model flag value, applying precedence:
// CLI flag > NANOBANANA_MODEL env > config file > default
func resolveModelFlag(flagVal string, cfg *Config) string {
	if flagVal != "" {
		return flagVal
	}
	if envModel := os.Getenv("NANOBANANA_MODEL"); envModel != "" {
		return envModel
	}
	if cfg.Model != "" {
		return cfg.Model
	}
	return "flash"
}

// --- API types ---

type apiContent struct {
	Parts []apiPart `json:"parts"`
	Role  string    `json:"role,omitempty"`
}

type apiPart struct {
	Text       string   `json:"text,omitempty"`
	InlineData *apiBlob `json:"inlineData,omitempty"`
}

type apiBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type apiGenerationConfig struct {
	ResponseMIMEType   string   `json:"responseMimeType,omitempty"`
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

type apiRequest struct {
	Contents         []apiContent         `json:"contents"`
	GenerationConfig *apiGenerationConfig `json:"generationConfig,omitempty"`
}

type apiResponse struct {
	Candidates []apiCandidate `json:"candidates"`
	Error      *apiError      `json:"error,omitempty"`
}

type apiCandidate struct {
	Content apiContent `json:"content"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// --- API client ---

func buildPrompt(prompt, aspect, size string) string {
	parts := []string{prompt}
	if aspect != "1:1" {
		parts = append(parts, fmt.Sprintf("Aspect ratio: %s", aspect))
	}
	dims, ok := validSizes[size]
	if ok && size != "1K" {
		parts = append(parts, fmt.Sprintf("Resolution: %dx%d", dims[0], dims[1]))
	}
	return strings.Join(parts, ". ")
}

func generateImage(apiKey, model, prompt, aspect, size string) ([]byte, string, error) {
	fullPrompt := buildPrompt(prompt, aspect, size)

	reqBody := apiRequest{
		Contents: []apiContent{
			{
				Parts: []apiPart{
					{Text: fullPrompt},
				},
			},
		},
		GenerationConfig: nil,
	}

	return doAPICall(apiKey, model, reqBody)
}

func editImage(apiKey, model, prompt string, imgData []byte, mimeType, aspect, size string) ([]byte, string, error) {
	fullPrompt := buildPrompt(prompt, aspect, size)
	b64 := base64.StdEncoding.EncodeToString(imgData)

	reqBody := apiRequest{
		Contents: []apiContent{
			{
				Parts: []apiPart{
					{Text: fullPrompt},
					{
						InlineData: &apiBlob{
							MIMEType: mimeType,
							Data:     b64,
						},
					},
				},
			},
		},
		GenerationConfig: nil,
	}

	return doAPICall(apiKey, model, reqBody)
}

func doAPICall(apiKey, model string, reqBody apiRequest) ([]byte, string, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent", apiBaseURL, model)
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("could not reach API. Check your internet connection")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading response: %w", err)
	}

	// Handle HTTP error codes
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, "", fmt.Errorf("authentication failed. Check your API key: nanobanana setup")
	case resp.StatusCode == 429:
		return nil, "", fmt.Errorf("rate limit exceeded. Wait and try again")
	case resp.StatusCode == 400:
		var apiResp apiResponse
		if err := json.Unmarshal(body, &apiResp); err == nil && apiResp.Error != nil {
			return nil, "", fmt.Errorf("API error: %s", apiResp.Error.Message)
		}
		return nil, "", fmt.Errorf("bad request (400)")
	case resp.StatusCode != 200:
		var apiResp apiResponse
		if err := json.Unmarshal(body, &apiResp); err == nil && apiResp.Error != nil {
			return nil, "", fmt.Errorf("API error (%d): %s", resp.StatusCode, apiResp.Error.Message)
		}
		return nil, "", fmt.Errorf("API error (%d)", resp.StatusCode)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, "", fmt.Errorf("parsing response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, "", fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	// Extract image from response (matches official extension logic)
	for _, candidate := range apiResp.Candidates {
		for _, part := range candidate.Content.Parts {
			// Primary: image in inlineData
			if part.InlineData != nil && part.InlineData.Data != "" {
				imgBytes, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					return nil, "", fmt.Errorf("decoding image: %w", err)
				}
				mime := part.InlineData.MIMEType
				if mime == "" {
					mime = "image/png"
				}
				return imgBytes, mime, nil
			}
			// Fallback: base64 image data in text field
			if part.Text != "" && len(part.Text) >= 1000 && isBase64Image(part.Text) {
				imgBytes, err := base64.StdEncoding.DecodeString(part.Text)
				if err != nil {
					continue
				}
				return imgBytes, "image/png", nil
			}
		}
	}

	return nil, "", fmt.Errorf("no image in API response")
}

var base64Re = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

func isBase64Image(s string) bool {
	return base64Re.MatchString(s)
}

// --- Image I/O ---

func readImage(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading image: %w", err)
	}

	mimeType := detectMIMEType(path, data)
	return data, mimeType, nil
}

func detectMIMEType(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	// Fallback to content detection
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return "image/png"
}

func writeImage(path string, data []byte, sourceMIME string) error {
	ext := strings.ToLower(filepath.Ext(path))

	// If the output extension matches the source MIME, write raw bytes
	if (ext == ".png" && sourceMIME == "image/png") ||
		(ext == ".jpg" && sourceMIME == "image/jpeg") ||
		(ext == ".jpeg" && sourceMIME == "image/jpeg") {
		return os.WriteFile(path, data, 0644)
	}

	// Need to transcode
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// If we can't decode, just write raw bytes
		return os.WriteFile(path, data, 0644)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	switch ext {
	case ".jpg", ".jpeg":
		return jpeg.Encode(f, img, &jpeg.Options{Quality: 95})
	case ".png":
		return png.Encode(f, img)
	default:
		// Default to PNG
		return png.Encode(f, img)
	}
}

func extForMIME(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func autoName(prefix, mime string) string {
	ts := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s%s", prefix, ts, extForMIME(mime))
}

// --- Output helpers ---

func success(format string, args ...any) {
	if !quiet {
		fmt.Fprintf(os.Stderr, colorGreen+"✓ "+colorReset+format+"\n", args...)
	}
}

func info(format string, args ...any) {
	if !quiet {
		fmt.Fprintf(os.Stderr, colorBlue+"→ "+colorReset+format+"\n", args...)
	}
}

func warn(format string, args ...any) {
	if !quiet {
		fmt.Fprintf(os.Stderr, colorYellow+"⚠ "+colorReset+format+"\n", args...)
	}
}

func errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, colorRed+"✗ "+colorReset+format+"\n", args...)
}

// --- Spinner ---

func startSpinner(msg string) func() {
	if quiet || !term.IsTerminal(int(os.Stderr.Fd())) {
		if !quiet {
			fmt.Fprintf(os.Stderr, "%s...\n", msg)
		}
		return func() {}
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	var mu sync.Mutex
	done := false

	go func() {
		i := 0
		for {
			mu.Lock()
			if done {
				mu.Unlock()
				return
			}
			mu.Unlock()

			fmt.Fprintf(os.Stderr, "\r%s%s%s %s", colorCyan, frames[i%len(frames)], colorReset, msg)
			i++
			time.Sleep(80 * time.Millisecond)
		}
	}()

	return func() {
		mu.Lock()
		done = true
		mu.Unlock()
		fmt.Fprintf(os.Stderr, "\r\033[K") // Clear line
	}
}

// --- Validation ---

func validateAspectRatio(ar string) error {
	if !validAspectRatios[ar] {
		valid := make([]string, 0, len(validAspectRatios))
		for k := range validAspectRatios {
			valid = append(valid, k)
		}
		return fmt.Errorf("invalid aspect ratio %q (valid: %s)", ar, strings.Join(valid, ", "))
	}
	return nil
}

func validateImageSize(size, model string) error {
	if _, ok := validSizes[size]; !ok {
		valid := make([]string, 0, len(validSizes))
		for k := range validSizes {
			valid = append(valid, k)
		}
		return fmt.Errorf("invalid size %q (valid: %s)", size, strings.Join(valid, ", "))
	}
	if size == "4K" && !isProModel(model) {
		return fmt.Errorf("4K size requires --model pro")
	}
	return nil
}

// isProModel returns true if the model string refers to the pro model,
// whether by alias or full model name.
func isProModel(model string) bool {
	return model == "pro" || model == modelPro
}

// resolveModel maps an alias to a full model name, or passes through
// a full model name directly.
func resolveModel(alias string) (string, error) {
	if full, ok := modelAliases[alias]; ok {
		return full, nil
	}
	// Accept full model names (anything containing a hyphen)
	if strings.Contains(alias, "-") {
		return alias, nil
	}
	return "", fmt.Errorf("unknown model %q (valid: flash, pro, or a full model name)", alias)
}

// --- Commands ---

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		return 0
	}

	switch args[0] {
	case "generate", "gen":
		return runGenerate(args[1:])
	case "edit":
		return runEdit(args[1:])
	case "setup":
		return runSetup()
	case "config":
		return runConfig()
	case "version":
		printVersion()
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		errorf("unknown command: %s (try 'nanobanana help')", args[0])
		return 1
	}
}

func runGenerate(args []string) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		modelFlag  string
		outputFlag string
		aspectFlag string
		sizeFlag   string
		quietFlag  bool
	)

	fs.StringVar(&modelFlag, "model", "", "model: flash, pro, or full model name")
	fs.StringVar(&modelFlag, "m", "", "model (shorthand)")
	fs.StringVar(&outputFlag, "output", "", "output file path")
	fs.StringVar(&outputFlag, "o", "", "output file path (shorthand)")
	fs.StringVar(&aspectFlag, "aspect", "1:1", "aspect ratio")
	fs.StringVar(&aspectFlag, "a", "1:1", "aspect ratio (shorthand)")
	fs.StringVar(&sizeFlag, "size", "1K", "image size: 1K, 2K, 4K")
	fs.StringVar(&sizeFlag, "s", "1K", "image size (shorthand)")
	fs.BoolVar(&quietFlag, "quiet", false, "suppress output, print only file path")
	fs.BoolVar(&quietFlag, "q", false, "suppress output (shorthand)")

	if err := fs.Parse(args); err != nil {
		errorf("invalid flags: %v", err)
		return 1
	}
	quiet = quietFlag

	remaining := fs.Args()
	if len(remaining) == 0 {
		errorf("usage: nanobanana generate \"prompt\" [flags]")
		return 1
	}
	prompt := strings.Join(remaining, " ")

	cfg, err := loadConfig()
	if err != nil {
		errorf("%v", err)
		return 1
	}

	modelFlag = resolveModelFlag(modelFlag, cfg)

	// Validate
	if err := validateAspectRatio(aspectFlag); err != nil {
		errorf("%v", err)
		return 1
	}
	if err := validateImageSize(sizeFlag, modelFlag); err != nil {
		errorf("%v", err)
		return 1
	}

	modelName, err := resolveModel(modelFlag)
	if err != nil {
		errorf("%v", err)
		return 1
	}

	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		errorf("%v", err)
		return 1
	}

	info("Generating with %s (%s, %s, %s)", modelFlag, aspectFlag, sizeFlag, prompt)
	stop := startSpinner("Generating image...")

	imgData, mimeType, err := generateImage(apiKey, modelName, prompt, aspectFlag, sizeFlag)
	stop()
	if err != nil {
		errorf("%v", err)
		return 1
	}

	// Determine output path
	outPath := outputFlag
	if outPath == "" {
		outPath = autoName("nanobanana", mimeType)
	}

	if err := writeImage(outPath, imgData, mimeType); err != nil {
		errorf("writing image: %v", err)
		return 1
	}

	if quiet {
		fmt.Println(outPath)
	} else {
		success("Saved to %s (%d bytes)", outPath, len(imgData))
	}
	return 0
}

func runEdit(args []string) int {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		modelFlag  string
		outputFlag string
		aspectFlag string
		sizeFlag   string
		quietFlag  bool
	)

	fs.StringVar(&modelFlag, "model", "", "model: flash, pro, or full model name")
	fs.StringVar(&modelFlag, "m", "", "model (shorthand)")
	fs.StringVar(&outputFlag, "output", "", "output file path")
	fs.StringVar(&outputFlag, "o", "", "output file path (shorthand)")
	fs.StringVar(&aspectFlag, "aspect", "1:1", "aspect ratio")
	fs.StringVar(&aspectFlag, "a", "1:1", "aspect ratio (shorthand)")
	fs.StringVar(&sizeFlag, "size", "1K", "image size: 1K, 2K, 4K")
	fs.StringVar(&sizeFlag, "s", "1K", "image size (shorthand)")
	fs.BoolVar(&quietFlag, "quiet", false, "suppress output, print only file path")
	fs.BoolVar(&quietFlag, "q", false, "suppress output (shorthand)")

	if err := fs.Parse(args); err != nil {
		errorf("invalid flags: %v", err)
		return 1
	}
	quiet = quietFlag

	remaining := fs.Args()
	if len(remaining) < 2 {
		errorf("usage: nanobanana edit <image> \"prompt\" [flags]")
		return 1
	}
	imagePath := remaining[0]
	prompt := strings.Join(remaining[1:], " ")

	cfg, err := loadConfig()
	if err != nil {
		errorf("%v", err)
		return 1
	}

	modelFlag = resolveModelFlag(modelFlag, cfg)

	// Validate
	if err := validateAspectRatio(aspectFlag); err != nil {
		errorf("%v", err)
		return 1
	}
	if err := validateImageSize(sizeFlag, modelFlag); err != nil {
		errorf("%v", err)
		return 1
	}

	modelName, err := resolveModel(modelFlag)
	if err != nil {
		errorf("%v", err)
		return 1
	}

	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		errorf("%v", err)
		return 1
	}

	// Read input image
	imgData, mimeType, err := readImage(imagePath)
	if err != nil {
		errorf("%v", err)
		return 1
	}

	info("Editing %s with %s (%s)", imagePath, modelFlag, prompt)
	stop := startSpinner("Editing image...")

	resultData, resultMIME, err := editImage(apiKey, modelName, prompt, imgData, mimeType, aspectFlag, sizeFlag)
	stop()
	if err != nil {
		errorf("%v", err)
		return 1
	}

	// Determine output path
	outPath := outputFlag
	if outPath == "" {
		ext := filepath.Ext(imagePath)
		base := strings.TrimSuffix(filepath.Base(imagePath), ext)
		outPath = base + "_edited" + ext
	}

	if err := writeImage(outPath, resultData, resultMIME); err != nil {
		errorf("writing image: %v", err)
		return 1
	}

	if quiet {
		fmt.Println(outPath)
	} else {
		success("Saved to %s (%d bytes)", outPath, len(resultData))
	}
	return 0
}

func runSetup() int {
	cfg, err := loadConfig()
	if err != nil {
		errorf("%v", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "\n%snanobanana setup%s\n\n", colorBold, colorReset)

	// API key
	fmt.Fprintf(os.Stderr, "Enter your Gemini API key")
	if cfg.APIKey != "" {
		masked := cfg.APIKey[:4] + "..." + cfg.APIKey[len(cfg.APIKey)-4:]
		fmt.Fprintf(os.Stderr, " (current: %s)", masked)
	}
	fmt.Fprintf(os.Stderr, ": ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		key := strings.TrimSpace(scanner.Text())
		if key != "" {
			cfg.APIKey = key
		}
	}

	if cfg.APIKey == "" {
		errorf("API key is required")
		return 1
	}

	// Default model
	fmt.Fprintf(os.Stderr, "Default model [flash/pro] (current: %s): ", cfg.Model)
	if scanner.Scan() {
		model := strings.TrimSpace(scanner.Text())
		if model != "" {
			if model != "flash" && model != "pro" {
				errorf("invalid model: %s (must be flash or pro)", model)
				return 1
			}
			cfg.Model = model
		}
	}

	if err := saveConfig(cfg); err != nil {
		errorf("saving config: %v", err)
		return 1
	}

	success("Config saved to %s", configPath())
	return 0
}

func runConfig() int {
	cfg, err := loadConfig()
	if err != nil {
		errorf("%v", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "\n%snanobanana config%s\n\n", colorBold, colorReset)
	fmt.Fprintf(os.Stderr, "  %sConfig file:%s  %s\n", colorBold, colorReset, configPath())

	if cfg.APIKey != "" {
		masked := cfg.APIKey[:4] + "..." + cfg.APIKey[len(cfg.APIKey)-4:]
		fmt.Fprintf(os.Stderr, "  %sAPI key:%s      %s\n", colorBold, colorReset, masked)
	} else {
		fmt.Fprintf(os.Stderr, "  %sAPI key:%s      %s(not set)%s\n", colorBold, colorReset, colorYellow, colorReset)
	}

	fmt.Fprintf(os.Stderr, "  %sModel:%s        %s\n", colorBold, colorReset, cfg.Model)

	// Show env var overrides
	for _, env := range []string{"NANOBANANA_GEMINI_API_KEY", "GEMINI_API_KEY"} {
		if os.Getenv(env) != "" {
			fmt.Fprintf(os.Stderr, "\n  %s%s:%s set (overrides config)%s\n", colorYellow, env, colorReset, colorReset)
			break
		}
	}
	if envModel := os.Getenv("NANOBANANA_MODEL"); envModel != "" {
		fmt.Fprintf(os.Stderr, "  %sNANOBANANA_MODEL:%s %s (overrides config)%s\n", colorYellow, colorReset, envModel, colorReset)
	}

	fmt.Fprintln(os.Stderr)
	return 0
}

func printVersion() {
	fmt.Printf("%snanobanana%s %s%s%s (%s/%s)\n", colorBold, colorReset, colorCyan, Version, colorReset, runtime.GOOS, runtime.GOARCH)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "\n  %snanobanana%s — generate and edit images with Gemini\n\n", colorBold, colorReset)
	fmt.Fprintf(os.Stderr, "  %sVersion:%s %s\n\n", colorBold, colorReset, Version)
	fmt.Fprintf(os.Stderr, "%sUSAGE:%s\n", colorBold, colorReset)
	fmt.Fprintln(os.Stderr, "  nanobanana generate \"prompt\"      Generate an image from text (alias: gen)")
	fmt.Fprintln(os.Stderr, "  nanobanana edit <image> \"prompt\"   Edit an existing image")
	fmt.Fprintln(os.Stderr, "  nanobanana setup                  Configure API key")
	fmt.Fprintln(os.Stderr, "  nanobanana config                 Show current configuration")
	fmt.Fprintln(os.Stderr, "  nanobanana version                Show version info")
	fmt.Fprintln(os.Stderr, "  nanobanana help                   Show this help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "%sFLAGS:%s\n", colorBold, colorReset)
	fmt.Fprintln(os.Stderr, "  -m, --model <name>    Model: flash, pro, or a full model name")
	fmt.Fprintln(os.Stderr, "  -o, --output <path>   Output file path (default: auto-generated)")
	fmt.Fprintln(os.Stderr, "  -a, --aspect <ratio>  Aspect ratio: 1:1, 16:9, 9:16, 4:3, 3:4 (default: 1:1)")
	fmt.Fprintln(os.Stderr, "  -s, --size <size>     Image size: 1K, 2K, 4K (default: 1K; 4K requires pro)")
	fmt.Fprintln(os.Stderr, "  -q, --quiet           Suppress output, print only file path to stdout")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "%sMODELS:%s\n", colorBold, colorReset)
	fmt.Fprintln(os.Stderr, "  flash                 gemini-2.5-flash-image (fast, ~$0.04/img)")
	fmt.Fprintln(os.Stderr, "  pro                   gemini-3-pro-image-preview (quality, ~$0.13/img)")
	fmt.Fprintln(os.Stderr, "  <full-name>           Any Gemini model name (e.g., gemini-2.5-flash-image)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "%sCONFIG:%s\n", colorBold, colorReset)
	fmt.Fprintf(os.Stderr, "  File: %s\n", configPath())
	fmt.Fprintln(os.Stderr, "  Env:  NANOBANANA_GEMINI_API_KEY (or GEMINI_API_KEY)")
	fmt.Fprintln(os.Stderr, "  Env:  NANOBANANA_MODEL (overrides config default model)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "%sEXAMPLES:%s\n", colorBold, colorReset)
	fmt.Fprintln(os.Stderr, "  nanobanana generate \"a cat in space\"")
	fmt.Fprintln(os.Stderr, "  nanobanana gen \"sunset\" --aspect 16:9 --output sunset.png")
	fmt.Fprintln(os.Stderr, "  nanobanana generate \"4K wallpaper\" --model pro --size 4K")
	fmt.Fprintln(os.Stderr, "  nanobanana edit photo.jpg \"make it cartoon\"")
	fmt.Fprintln(os.Stderr, "  nanobanana edit photo.jpg \"watercolor style\" -o result.png")
	fmt.Fprintln(os.Stderr, "  nanobanana gen \"logo\" -q | xargs open   # generate and open")
	fmt.Fprintln(os.Stderr, "")
}
