package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CookieUpdateCallback is kept for backwards compatibility
type CookieUpdateCallback func(providerName string, newCookies string, cookieMap map[string]string)

// GeminiWebProvider wraps gemini-web2api via File IPC for zero-auth / cookie-based Gemini Web access
type GeminiWebProvider struct {
	mu             sync.Mutex
	name           string
	defaultModel   string
	models         []string
	cookies        string
	cookieMap      map[string]string
	proxy          string
	pythonBin      string
	runnerPath     string
	ipcDir         string
	onCookieUpdate CookieUpdateCallback
}

// IPCRequest represents the JSON payload passed to scripts/gemini_web_runner.py
type IPCRequest struct {
	Prompt     string `json:"prompt"`
	Model      string `json:"model"`
	ThinkMode  *int   `json:"think_mode,omitempty"`
	Stream     bool   `json:"stream"`
	Proxy      string `json:"proxy,omitempty"`
	CookieFile string `json:"cookie_file,omitempty"`
	CookieStr  string `json:"cookie_str,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// IPCEvent represents a streaming event emitted by scripts/gemini_web_runner.py
type IPCEvent struct {
	Type        string `json:"type"` // "chunk", "done", "error"
	Delta       string `json:"delta,omitempty"`
	Content     string `json:"content,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	TotalTokens int    `json:"total_tokens,omitempty"`
	LatencyMs   int64  `json:"latency_ms,omitempty"`
	Model       string `json:"model,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Default models supported by gemini-web2api
var defaultGeminiWebModels = []string{
	"gemini-3.5-flash-thinking",
	"gemini-3.6-flash",
	"gemini-3.7-flash",
	"gemini-auto",
	"gemini-flash-lite",
	"gemini-3.1-pro",
	"gemini-3.5-flash-thinking-lite",
}

// NewGeminiWebProvider creates a new GeminiWeb2APIProvider instance
func NewGeminiWebProvider(name string, authInput string, defaultModel string, models []string) *GeminiWebProvider {
	if defaultModel == "" {
		defaultModel = "gemini-3.5-flash-thinking"
	}
	if len(models) == 0 {
		models = make([]string, len(defaultGeminiWebModels))
		copy(models, defaultGeminiWebModels)
	}

	ipcDir := filepath.Join("data", "gemini_ipc")
	_ = os.MkdirAll(ipcDir, 0755)

	p := &GeminiWebProvider{
		name:         name,
		defaultModel: defaultModel,
		models:       models,
		cookieMap:    make(map[string]string),
		ipcDir:       ipcDir,
		pythonBin:    findPythonBinary(),
		runnerPath:   findRunnerScript(),
	}

	if authInput != "" {
		_ = p.UpdateAuth(authInput)
	}

	return p
}

func (p *GeminiWebProvider) Name() string         { return p.name }
func (p *GeminiWebProvider) Type() string         { return "gemini_web" }
func (p *GeminiWebProvider) DefaultModel() string { return p.defaultModel }
func (p *GeminiWebProvider) Models() []string     { return p.models }

func (p *GeminiWebProvider) SetHTTPClient(client interface{}) {
	// Not used for File IPC, but preserved to satisfy Provider interface
}

func (p *GeminiWebProvider) SetProxy(proxyURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxy = proxyURL
}

func (p *GeminiWebProvider) SetOnCookieUpdate(cb CookieUpdateCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onCookieUpdate = cb
}

func (p *GeminiWebProvider) HasValidCookies() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.cookies) > 10
}

func (p *GeminiWebProvider) GetCookiesString() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cookies
}

func (p *GeminiWebProvider) UpdateAuth(rawInput string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	parsed, err := ParseGoogleAuthInput(rawInput)
	if err != nil {
		p.cookies = strings.TrimSpace(rawInput)
		return nil
	}

	for k, v := range parsed {
		p.cookieMap[k] = v
	}

	var parts []string
	for k, v := range p.cookieMap {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	p.cookies = strings.Join(parts, "; ")
	return nil
}

// GenerateChat executes standard non-streaming chat request
func (p *GeminiWebProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	return p.GenerateChatStream(ctx, req)
}

// GenerateChatStream executes streaming or non-streaming chat request via gemini-web2api File IPC
func (p *GeminiWebProvider) GenerateChatStream(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	startTime := time.Now()

	// 1. Build prompt from messages
	var promptBuilder strings.Builder
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			promptBuilder.WriteString(fmt.Sprintf("[System Instruction]: %s\n\n", m.Content))
		case RoleUser:
			promptBuilder.WriteString(fmt.Sprintf("%s\n", m.Content))
		case RoleAssistant:
			promptBuilder.WriteString(fmt.Sprintf("[Model Response]: %s\n\n", m.Content))
		}
	}
	fullPrompt := strings.TrimSpace(promptBuilder.String())
	if fullPrompt == "" {
		return nil, errors.New("prompt tidak boleh kosong")
	}

	modelToUse := req.Model
	if modelToUse == "" || strings.EqualFold(modelToUse, "auto") || strings.EqualFold(modelToUse, "gemini_web") || strings.EqualFold(modelToUse, "gemini-web") {
		modelToUse = p.defaultModel
	}

	p.mu.Lock()
	cookieStr := p.cookies
	proxy := p.proxy
	pyBin := p.pythonBin
	runnerPath := p.runnerPath
	ipcDir := p.ipcDir
	p.mu.Unlock()

	if pyBin == "" {
		pyBin = findPythonBinary()
	}
	if runnerPath == "" {
		runnerPath = findRunnerScript()
	}

	if _, err := os.Stat(runnerPath); err != nil {
		return nil, fmt.Errorf("runner script tidak ditemukan di %s: %w", runnerPath, err)
	}

	// 2. Prepare File IPC paths
	_ = os.MkdirAll(ipcDir, 0755)
	reqID := uuid.New().String()
	reqPath := filepath.Join(ipcDir, fmt.Sprintf("req_%s.json", reqID))
	outPath := filepath.Join(ipcDir, fmt.Sprintf("resp_%s.jsonl", reqID))

	defer func() {
		_ = os.Remove(reqPath)
		_ = os.Remove(outPath)
	}()

	ipcPayload := IPCRequest{
		Prompt:     fullPrompt,
		Model:      modelToUse,
		Stream:     req.Stream,
		Proxy:      proxy,
		CookieStr:  cookieStr,
		TimeoutSec: 150,
	}

	reqBytes, err := json.Marshal(ipcPayload)
	if err != nil {
		return nil, fmt.Errorf("gagal menyusun request IPC: %w", err)
	}

	if err := os.WriteFile(reqPath, reqBytes, 0600); err != nil {
		return nil, fmt.Errorf("gagal menulis request IPC: %w", err)
	}

	// 3. Spawn python runner process
	cmd := exec.CommandContext(ctx, pyBin, runnerPath, "--req", reqPath, "--out", outPath)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("gagal menjalankan runner python (%s): %w", pyBin, err)
	}

	// 4. Stream consumer loop: tails outPath incrementally
	var finalContent strings.Builder
	var lastEvent *IPCEvent
	var streamDone bool

	readTimeout := time.After(180 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	var fileOffset int64
	procFinished := make(chan error, 1)
	go func() {
		procFinished <- cmd.Wait()
	}()

	for !streamDone {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		case <-readTimeout:
			_ = cmd.Process.Kill()
			return nil, errors.New("timeout membaca respon dari gemini-web2api File IPC")
		case procErr := <-procFinished:
			// Read remaining events from file
			readRemainingEvents(outPath, &fileOffset, func(ev IPCEvent) {
				lastEvent = &ev
				if ev.Type == "chunk" && ev.Delta != "" {
					finalContent.WriteString(ev.Delta)
					if req.Stream && req.StreamCallback != nil {
						req.StreamCallback(StreamChunk{Content: ev.Delta, Done: false})
					}
				} else if ev.Type == "done" {
					streamDone = true
				}
			})

			if procErr != nil && lastEvent == nil {
				errMsg := strings.TrimSpace(stderrBuf.String())
				if errMsg == "" {
					errMsg = procErr.Error()
				}
				return nil, fmt.Errorf("runner gemini-web2api gagal (%v): %s", procErr, errMsg)
			}
			streamDone = true
		case <-ticker.C:
			// Read incremental new lines from outPath
			readRemainingEvents(outPath, &fileOffset, func(ev IPCEvent) {
				lastEvent = &ev
				if ev.Type == "chunk" && ev.Delta != "" {
					finalContent.WriteString(ev.Delta)
					if req.Stream && req.StreamCallback != nil {
						req.StreamCallback(StreamChunk{Content: ev.Delta, Done: false})
					}
				} else if ev.Type == "done" {
					streamDone = true
				}
			})
		}
	}

	if lastEvent != nil && lastEvent.Type == "error" {
		return nil, fmt.Errorf("gemini-web2api error: %s", lastEvent.Error)
	}

	contentStr := finalContent.String()
	if lastEvent != nil && lastEvent.Content != "" {
		contentStr = lastEvent.Content
	}

	if strings.TrimSpace(contentStr) == "" {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg != "" {
			return nil, fmt.Errorf("tidak ada output yang dihasilkan oleh gemini-web2api: %s", errMsg)
		}
		return nil, errors.New("tidak ada output yang dihasilkan oleh gemini-web2api")
	}

	if req.Stream && req.StreamCallback != nil {
		req.StreamCallback(StreamChunk{Done: true})
	}

	latency := time.Since(startTime)
	approxTokens := (len(fullPrompt) + len(contentStr)) / 4
	if lastEvent != nil && lastEvent.TotalTokens > 0 {
		approxTokens = lastEvent.TotalTokens
	}

	return &ChatResponse{
		Content:          contentStr,
		Thinking:         lastEventThinking(lastEvent),
		PromptTokens:     len(fullPrompt) / 4,
		CompletionTokens: len(contentStr) / 4,
		TotalTokens:      approxTokens,
		CostUSD:          0, // Free Gemini Web
		Latency:          latency,
		Model:            modelToUse,
		ProviderName:     p.name,
	}, nil
}

func lastEventThinking(ev *IPCEvent) string {
	if ev != nil {
		return ev.Thinking
	}
	return ""
}

// readRemainingEvents reads new lines from file starting from offset
func readRemainingEvents(path string, offset *int64, callback func(IPCEvent)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	if *offset > 0 {
		if _, err := f.Seek(*offset, io.SeekStart); err != nil {
			return
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		*offset += int64(len(line) + 1) // +1 for newline delimiter
		lineStr := strings.TrimSpace(string(line))
		if lineStr == "" {
			continue
		}
		var ev IPCEvent
		if err := json.Unmarshal([]byte(lineStr), &ev); err == nil {
			callback(ev)
		}
	}
}

// RunDiagnostic executes runner diagnostic check (--test)
func (p *GeminiWebProvider) RunDiagnostic(ctx context.Context) (map[string]interface{}, error) {
	pyBin := p.pythonBin
	if pyBin == "" {
		pyBin = findPythonBinary()
	}
	runner := p.runnerPath
	if runner == "" {
		runner = findRunnerScript()
	}

	cmd := exec.CommandContext(ctx, pyBin, runner, "--test")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("diagnostic gagal dieksekusi: %w (output: %s)", err, string(out))
	}

	var res map[string]interface{}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("output diagnostic bukan JSON: %s", string(out))
	}
	return res, nil
}

// RunUpdate executes the update script for gemini-web2api
func (p *GeminiWebProvider) RunUpdate(ctx context.Context) (string, error) {
	root := findProjectRoot()
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		batPath := filepath.Join(root, "scripts", "update_gemini_web2api.bat")
		if _, err := os.Stat(batPath); err == nil {
			cmd = exec.CommandContext(ctx, "cmd.exe", "/c", batPath)
		} else {
			cmd = exec.CommandContext(ctx, "python", "-m", "pip", "install", "--upgrade", "git+https://github.com/Sophomoresty/gemini-web2api.git", "httpx>=0.25")
		}
	} else {
		shPath := filepath.Join(root, "scripts", "update_gemini_web2api.sh")
		if _, err := os.Stat(shPath); err == nil {
			cmd = exec.CommandContext(ctx, "sh", shPath)
		} else {
			cmd = exec.CommandContext(ctx, "python3", "-m", "pip", "install", "--upgrade", "--break-system-packages", "git+https://github.com/Sophomoresty/gemini-web2api.git", "httpx>=0.25")
		}
	}

	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	outputStr := string(out)
	if err != nil {
		log.Printf("⚠️ [GeminiWeb] Update script gagal: %v\nOutput:\n%s", err, outputStr)
		return outputStr, fmt.Errorf("update gagal: %w", err)
	}
	return outputStr, nil
}

// ParseGoogleAuthInput parses cookie strings, tokens or JSON exports
func ParseGoogleAuthInput(input string) (map[string]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, errors.New("input autentikasi kosong")
	}

	cookies := make(map[string]string)

	// JSON format
	if strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]") {
		type cJSON struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		var list []cJSON
		if err := json.Unmarshal([]byte(input), &list); err == nil && len(list) > 0 {
			for _, item := range list {
				if item.Name != "" && item.Value != "" {
					cookies[item.Name] = item.Value
				}
			}
			if len(cookies) > 0 {
				return cookies, nil
			}
		}
	}

	// Standard cookie string format (key=value; ...)
	splitRegex := regexp.MustCompile(`[;\n\r]+`)
	tokens := splitRegex.Split(input, -1)
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if eqIdx := strings.IndexAny(tok, "=:"); eqIdx > 0 {
			k := strings.TrimSpace(tok[:eqIdx])
			v := strings.TrimSpace(tok[eqIdx+1:])
			v = strings.Trim(v, `"'`)
			if k != "" && v != "" {
				cookies[k] = v
			}
		} else if strings.HasPrefix(tok, "sidts-") {
			cookies["__Secure-1PSIDTS"] = tok
		} else if strings.HasPrefix(tok, "g.a") {
			cookies["__Secure-1PSID"] = tok
		}
	}

	if len(cookies) == 0 && len(input) > 20 && !strings.Contains(input, " ") {
		if strings.HasPrefix(input, "sidts-") {
			cookies["__Secure-1PSIDTS"] = input
		} else {
			cookies["__Secure-1PSID"] = input
		}
	}

	if len(cookies) == 0 {
		return nil, errors.New("tidak ditemukan cookie atau token valid")
	}

	return cookies, nil
}

func findPythonBinary() string {
	if envPy := os.Getenv("PYTHON_BIN"); envPy != "" {
		return envPy
	}
	// On Alpine / Linux, prefer python3
	if runtime.GOOS != "windows" {
		if path, err := exec.LookPath("python3"); err == nil {
			return path
		}
		if path, err := exec.LookPath("python"); err == nil {
			return path
		}
		return "python3"
	}
	// On Windows, check python then python3
	if path, err := exec.LookPath("python"); err == nil {
		return path
	}
	if path, err := exec.LookPath("python3"); err == nil {
		return path
	}
	return "python"
}

func findRunnerScript() string {
	root := findProjectRoot()
	return filepath.Join(root, "scripts", "gemini_web_runner.py")
}

func findProjectRoot() string {
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "gemini_web_runner.py")); err == nil {
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		for {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "gemini_web_runner.py")); err == nil {
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "."
}
