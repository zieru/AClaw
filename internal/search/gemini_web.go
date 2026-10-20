package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

// GeminiWebSearchProvider implements search.Provider using Gemini Web native Google search grounding
type GeminiWebSearchProvider struct {
	pythonBin  string
	runnerPath string
	ipcDir     string
	model      string
	proxy      string
	cookieStr  string
}

// NewGeminiWebSearchProvider creates a new Gemini Web search provider
func NewGeminiWebSearchProvider() *GeminiWebSearchProvider {
	return &GeminiWebSearchProvider{
		pythonBin:  findPythonBinary(),
		runnerPath: findRunnerScript(),
		ipcDir:     filepath.Join("data", "gemini_ipc"),
		model:      "gemini-3.5-flash-thinking",
	}
}

func (g *GeminiWebSearchProvider) Name() string {
	return "gemini_web"
}

func (g *GeminiWebSearchProvider) IsAvailable() bool {
	runner := g.runnerPath
	if runner == "" {
		runner = findRunnerScript()
	}
	info, err := os.Stat(runner)
	return err == nil && !info.IsDir()
}

func (g *GeminiWebSearchProvider) SetProxy(proxy string) {
	g.proxy = proxy
}

func (g *GeminiWebSearchProvider) SetCookies(cookies string) {
	g.cookieStr = cookies
}

type geminiIPCReq struct {
	Prompt     string `json:"prompt"`
	Model      string `json:"model"`
	Stream     bool   `json:"stream"`
	Proxy      string `json:"proxy,omitempty"`
	CookieStr  string `json:"cookie_str,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type geminiIPCEvent struct {
	Type        string `json:"type"`
	Delta       string `json:"delta,omitempty"`
	Content     string `json:"content,omitempty"`
	Error       string `json:"error,omitempty"`
	TotalTokens int    `json:"total_tokens,omitempty"`
	LatencyMS   int    `json:"latency_ms,omitempty"`
}

// Search queries Google Gemini Web with search grounding and returns normalized results
func (g *GeminiWebSearchProvider) Search(ctx context.Context, query string, limit int) (*Response, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query cannot be empty")
	}
	if limit <= 0 {
		limit = 5
	}

	pyBin := g.pythonBin
	if pyBin == "" {
		pyBin = findPythonBinary()
	}
	runner := g.runnerPath
	if runner == "" {
		runner = findRunnerScript()
	}

	if _, err := os.Stat(runner); err != nil {
		return nil, fmt.Errorf("gemini_web runner not found at %s: %w", runner, err)
	}

	prompt := fmt.Sprintf(`Search the web for: "%s"

Provide a concise answer summary followed by top %d search results.
Return the search results as a JSON array inside a `+"```json ... ```"+` code block with this format:
[
  {
    "title": "Title of page",
    "url": "https://...",
    "snippet": "Summary of snippet"
  }
]`, query, limit)

	_ = os.MkdirAll(g.ipcDir, 0755)
	reqID := uuid.New().String()
	reqPath := filepath.Join(g.ipcDir, fmt.Sprintf("search_req_%s.json", reqID))
	outPath := filepath.Join(g.ipcDir, fmt.Sprintf("search_resp_%s.jsonl", reqID))

	defer func() {
		_ = os.Remove(reqPath)
		_ = os.Remove(outPath)
	}()

	ipcPayload := geminiIPCReq{
		Prompt:     prompt,
		Model:      g.model,
		Stream:     false,
		Proxy:      g.proxy,
		CookieStr:  g.cookieStr,
		TimeoutSec: 60,
	}

	reqBytes, err := json.Marshal(ipcPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search IPC request: %w", err)
	}

	if err := os.WriteFile(reqPath, reqBytes, 0600); err != nil {
		return nil, fmt.Errorf("failed to write search IPC request: %w", err)
	}

	cmd := exec.CommandContext(ctx, pyBin, runner, "--req", reqPath, "--out", outPath)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		errOutput := strings.TrimSpace(stderrBuf.String())
		if errOutput == "" {
			errOutput = err.Error()
		}
		return nil, fmt.Errorf("gemini_web search execution failed: %s", errOutput)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read search response file: %w", err)
	}

	var fullContent string
	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var ev geminiIPCEvent
		if err := json.Unmarshal([]byte(l), &ev); err == nil {
			if ev.Type == "error" {
				return nil, fmt.Errorf("gemini_web search error: %s", ev.Error)
			}
			if ev.Type == "done" && ev.Content != "" {
				fullContent = ev.Content
			}
		}
	}

	if strings.TrimSpace(fullContent) == "" {
		return nil, errors.New("gemini_web search returned empty content")
	}

	return parseGeminiWebSearchResponse(query, fullContent, limit)
}

// parseGeminiWebSearchResponse extracts instant answer and SearchItems from Gemini's response
func parseGeminiWebSearchResponse(query, content string, limit int) (*Response, error) {
	resp := &Response{
		Query:    query,
		Provider: "gemini_web",
		Results:  make([]SearchItem, 0),
	}

	// 1. Try to find a JSON array block
	jsonBlockRegex := regexp.MustCompile("(?s)```(?:json)?\\s*(\\[.*?\\])\\s*```")
	matches := jsonBlockRegex.FindStringSubmatch(content)

	if len(matches) > 1 {
		jsonText := strings.TrimSpace(matches[1])
		var items []SearchItem
		if err := json.Unmarshal([]byte(jsonText), &items); err == nil && len(items) > 0 {
			// Answer is everything before the code block
			idx := strings.Index(content, "```")
			if idx > 0 {
				resp.Answer = strings.TrimSpace(content[:idx])
			}
			for i, it := range items {
				if i >= limit {
					break
				}
				if it.URL != "" {
					resp.Results = append(resp.Results, it)
				}
			}
			if len(resp.Results) > 0 {
				return resp, nil
			}
		}
	}

	// 2. Fallback: Parse markdown links and bullets
	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\)]+)\)`)
	allLinks := linkRegex.FindAllStringSubmatch(content, -1)

	seenURLs := make(map[string]bool)
	for _, m := range allLinks {
		if len(m) > 2 {
			title := strings.TrimSpace(m[1])
			rawURL := strings.TrimSpace(m[2])
			if rawURL != "" && !seenURLs[rawURL] {
				seenURLs[rawURL] = true
				resp.Results = append(resp.Results, SearchItem{
					Title:   title,
					URL:     rawURL,
					Snippet: title,
				})
				if len(resp.Results) >= limit {
					break
				}
			}
		}
	}

	resp.Answer = strings.TrimSpace(content)
	if len(resp.Results) == 0 && resp.Answer == "" {
		return nil, errors.New("could not extract search results from gemini_web response")
	}

	return resp, nil
}

func findPythonBinary() string {
	if envPy := os.Getenv("PYTHON_BIN"); envPy != "" {
		return envPy
	}
	if runtime.GOOS != "windows" {
		if path, err := exec.LookPath("python3"); err == nil {
			return path
		}
		if path, err := exec.LookPath("python"); err == nil {
			return path
		}
		return "python3"
	}
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
	log.Printf("[search] Falling back to default project root '.'")
	return "."
}
