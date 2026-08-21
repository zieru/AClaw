package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"goassistant/internal/config"
)

// GeminiWebProvider scrapes/interacts with Gemini Web (gemini.google.com) using Google session cookies
type GeminiWebProvider struct {
	mu             sync.Mutex
	name           string
	defaultModel   string
	models         []string
	cookies        string            // Full formatted cookie string
	cookieMap      map[string]string // Parsed cookie map
	snlm0e         string            // CSRF session token from gemini.google.com/app
	snlm0eFetched  time.Time
	conversationID string
	responseID     string
	choiceID       string
	client         *http.Client
}

var (
	snlm0eRegex = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
)

// NewGeminiWebProvider creates a new Gemini Web Scrape provider instance
func NewGeminiWebProvider(name string, authInput string, defaultModel string, models []string) *GeminiWebProvider {
	if defaultModel == "" {
		defaultModel = "gemini-web-pro"
	}
	if len(models) == 0 {
		models = []string{"gemini-web-pro", "gemini-web-flash", "gemini-web-ultra"}
	}

	p := &GeminiWebProvider{
		name:         name,
		defaultModel: defaultModel,
		models:       models,
		cookieMap:    make(map[string]string),
		client:       &http.Client{Timeout: time.Duration(config.Get().Timeouts.APICallSeconds) * time.Second},
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
	if c, ok := client.(*http.Client); ok && c != nil {
		p.client = c
	}
}

// UpdateAuth parses and stores authentication input (URL redirect, cookie string, or individual cookie)
func (p *GeminiWebProvider) UpdateAuth(rawInput string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	parsedCookies, err := ParseGoogleAuthInput(rawInput)
	if err != nil {
		return err
	}

	for k, v := range parsedCookies {
		p.cookieMap[k] = v
	}

	// Rebuild cookie string
	var parts []string
	for k, v := range p.cookieMap {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	p.cookies = strings.Join(parts, "; ")

	// Reset cached SNlM0e to force re-fetch
	p.snlm0e = ""
	p.snlm0eFetched = time.Time{}

	return nil
}

// HasValidCookies checks if minimum required Google cookies are present
func (p *GeminiWebProvider) HasValidCookies() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cookies == "" {
		return false
	}
	// Minimal Google session cookie
	if _, ok := p.cookieMap["__Secure-1PSID"]; ok {
		return true
	}
	if _, ok := p.cookieMap["SID"]; ok {
		return true
	}
	return len(p.cookies) > 20
}

// GetCookiesString returns current cookie string
func (p *GeminiWebProvider) GetCookiesString() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cookies
}

// ParseGoogleAuthInput extracts cookies from a pasted URL or raw cookie header string
func ParseGoogleAuthInput(input string) (map[string]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, errors.New("input autentikasi kosong")
	}

	cookies := make(map[string]string)

	// Case 1: JSON formatted cookies (e.g. from Cookie-Editor extension export)
	if strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]") {
		type cookieJSON struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		var list []cookieJSON
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

	// Case 2: URL with query parameters or fragments (e.g. redirect URL after login)
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		if u, err := url.Parse(input); err == nil {
			// Check query params
			for k, vals := range u.Query() {
				if len(vals) > 0 {
					cookies[k] = vals[0]
				}
			}
			// Check fragment (hash) params
			if u.Fragment != "" {
				if fragValues, err := url.ParseQuery(u.Fragment); err == nil {
					for k, vals := range fragValues {
						if len(vals) > 0 {
							cookies[k] = vals[0]
						}
					}
				}
			}
		}
	}

	// Case 3: Standard Cookie header string: "key1=val1; key2=val2" or key=val per line
	splitRegex := regexp.MustCompile(`[;\n\r]+`)
	tokens := splitRegex.Split(input, -1)
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if eqIdx := strings.Index(tok, "="); eqIdx > 0 {
			k := strings.TrimSpace(tok[:eqIdx])
			v := strings.TrimSpace(tok[eqIdx+1:])
			// Strip surrounding quotes if present
			v = strings.Trim(v, `"'`)
			if k != "" && v != "" {
				cookies[k] = v
			}
		}
	}

	// Case 4: Single raw value assumed to be __Secure-1PSID if no '=' was present
	if len(cookies) == 0 && len(input) > 20 && !strings.Contains(input, " ") {
		cookies["__Secure-1PSID"] = input
	}

	if len(cookies) == 0 {
		return nil, errors.New("tidak ditemukan cookie atau token valid dalam input yang diberikan")
	}

	return cookies, nil
}

// FetchSNlM0e fetches the CSRF session token (SNlM0e) from gemini.google.com/app
func (p *GeminiWebProvider) FetchSNlM0e(ctx context.Context) (string, error) {
	p.mu.Lock()
	if p.snlm0e != "" && time.Since(p.snlm0eFetched) < 4*time.Hour {
		token := p.snlm0e
		p.mu.Unlock()
		return token, nil
	}
	cookieStr := p.cookies
	client := p.client
	p.mu.Unlock()

	if cookieStr == "" {
		return "", errors.New("cookie Google belum diatur. Silakan jalankan /gemini_login")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://gemini.google.com/app", nil)
	if err != nil {
		return "", fmt.Errorf("gagal membuat request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Cookie", cookieStr)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,id;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi gemini.google.com: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", errors.New("sesi Google login kedaluwarsa atau tidak valid (HTTP 401/403). Silakan perbarui cookie via /gemini_login")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gagal membaca response Gemini Web: %w", err)
	}

	bodyStr := string(body)
	matches := snlm0eRegex.FindStringSubmatch(bodyStr)
	if len(matches) < 2 {
		// Try alternative pattern for WIZ_global_data SNlM0e
		altRe := regexp.MustCompile(`"SNlM0e",null,"([^"]+)"`)
		altMatches := altRe.FindStringSubmatch(bodyStr)
		if len(altMatches) >= 2 {
			p.mu.Lock()
			p.snlm0e = altMatches[1]
			p.snlm0eFetched = time.Now()
			p.mu.Unlock()
			return altMatches[1], nil
		}
		return "", errors.New("gagal mengekstrak session token SNlM0e dari Gemini Web. Pastikan cookie __Secure-1PSID sudah benar dan akun sudah login")
	}

	snlm0e := matches[1]
	p.mu.Lock()
	p.snlm0e = snlm0e
	p.snlm0eFetched = time.Now()
	p.mu.Unlock()

	return snlm0e, nil
}

// GenerateChat executes standard non-streaming chat request
func (p *GeminiWebProvider) GenerateChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return p.GenerateChatStream(ctx, req)
}

// GenerateChatStream executes streaming or non-streaming chat request via Gemini Web backend
func (p *GeminiWebProvider) GenerateChatStream(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	startTime := time.Now()

	snlm0e, err := p.FetchSNlM0e(ctx)
	if err != nil {
		return nil, err
	}

	// Build prompt from messages
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

	p.mu.Lock()
	convID := p.conversationID
	respID := p.responseID
	choiceID := p.choiceID
	cookieStr := p.cookies
	client := p.client
	p.mu.Unlock()

	// Format RPC payload according to Bard/Gemini Web stream protocol
	reqArray := []interface{}{
		nil,
		fmt.Sprintf(`[[%s,0,null,null,null,null,0],["id"],[%s,%s,%s,null,null,[]],null,null,null,[1]]`,
			quoteJSONString(fullPrompt),
			quoteJSONString(convID),
			quoteJSONString(respID),
			quoteJSONString(choiceID),
		),
	}

	reqArrayJSON, err := json.Marshal(reqArray)
	if err != nil {
		return nil, fmt.Errorf("gagal menyusun request payload: %w", err)
	}

	formData := url.Values{}
	formData.Set("f.req", string(reqArrayJSON))
	formData.Set("at", snlm0e)

	randReqID := rand.Intn(900000) + 100000
	apiURL := fmt.Sprintf("https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?bl=boq_assistant-bard-web-server_20240519.16_p0&_reqid=%d&rt=c", randReqID)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request POST Gemini: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	httpReq.Header.Set("Cookie", cookieStr)
	httpReq.Header.Set("Origin", "https://gemini.google.com")
	httpReq.Header.Set("Referer", "https://gemini.google.com/")
	httpReq.Header.Set("X-Same-Domain", "1")
	httpReq.Header.Set("Accept", "*/*")

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal mengirim request ke Gemini Web: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		bodySample, _ := io.ReadAll(io.LimitReader(httpResp.Body, 512))
		return nil, fmt.Errorf("Gemini Web mengembalikan status HTTP %d: %s", httpResp.StatusCode, string(bodySample))
	}

	// Parse stream response
	reader := bufio.NewReader(httpResp.Body)
	var finalContent string
	var finalThinking string
	var newConvID, newRespID, newChoiceID string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Look for wrb.fr response blocks
		if strings.HasPrefix(line, `[["wrb.fr"`) {
			content, thought, cID, rID, chID, parseErr := parseGeminiWebChunk(line)
			if parseErr == nil && content != "" {
				delta := content
				if strings.HasPrefix(content, finalContent) {
					delta = strings.TrimPrefix(content, finalContent)
				}
				finalContent = content
				if thought != "" {
					finalThinking = thought
				}
				if cID != "" {
					newConvID = cID
				}
				if rID != "" {
					newRespID = rID
				}
				if chID != "" {
					newChoiceID = chID
				}

				if req.Stream && req.StreamCallback != nil && delta != "" {
					req.StreamCallback(StreamChunk{
						Content:  delta,
						Thinking: finalThinking,
						Done:     false,
					})
				}
			}
		}
	}

	// Update conversation identifiers
	p.mu.Lock()
	if newConvID != "" {
		p.conversationID = newConvID
	}
	if newRespID != "" {
		p.responseID = newRespID
	}
	if newChoiceID != "" {
		p.choiceID = newChoiceID
	}
	p.mu.Unlock()

	if finalContent == "" {
		return nil, errors.New("tidak ada konten yang dihasilkan oleh Gemini Web. Periksa kembali autentikasi")
	}

	if req.Stream && req.StreamCallback != nil {
		req.StreamCallback(StreamChunk{
			Done: true,
		})
	}

	latency := time.Since(startTime)
	approxTokens := (len(fullPrompt) + len(finalContent)) / 4

	return &ChatResponse{
		Content:          finalContent,
		Thinking:         finalThinking,
		PromptTokens:     len(fullPrompt) / 4,
		CompletionTokens: len(finalContent) / 4,
		TotalTokens:      approxTokens,
		CostUSD:          0, // Free web scraping
		Latency:          latency,
		Model:            p.defaultModel,
		ProviderName:     p.name,
	}, nil
}

// ResetConversation resets the ongoing conversation context
func (p *GeminiWebProvider) ResetConversation() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conversationID = ""
	p.responseID = ""
	p.choiceID = ""
}

// parseGeminiWebChunk parses the raw wrb.fr line into text content and metadata
func parseGeminiWebChunk(line string) (content string, thought string, convID string, respID string, choiceID string, err error) {
	var outer []interface{}
	if err := json.Unmarshal([]byte(line), &outer); err != nil {
		return "", "", "", "", "", err
	}

	if len(outer) == 0 {
		return "", "", "", "", "", errors.New("empty chunk")
	}

	firstElem, ok := outer[0].([]interface{})
	if !ok || len(firstElem) < 3 {
		return "", "", "", "", "", errors.New("invalid outer structure")
	}

	payloadStr, ok := firstElem[2].(string)
	if !ok || payloadStr == "" {
		return "", "", "", "", "", errors.New("empty payload string")
	}

	var inner []interface{}
	if err := json.Unmarshal([]byte(payloadStr), &inner); err != nil {
		return "", "", "", "", "", err
	}

	// inner[1] usually holds [conversation_id, response_id]
	if len(inner) > 1 {
		if metaList, ok := inner[1].([]interface{}); ok {
			if len(metaList) > 0 {
				if s, ok := metaList[0].(string); ok {
					convID = s
				}
			}
			if len(metaList) > 1 {
				if s, ok := metaList[1].(string); ok {
					respID = s
				}
			}
		}
	}

	// inner[4] holds candidates and text chunks
	if len(inner) > 4 {
		if candidates, ok := inner[4].([]interface{}); ok && len(candidates) > 0 {
			if firstCand, ok := candidates[0].([]interface{}); ok {
				if len(firstCand) > 0 {
					if s, ok := firstCand[0].(string); ok {
						choiceID = s
					}
				}
				if len(firstCand) > 1 {
					if textList, ok := firstCand[1].([]interface{}); ok && len(textList) > 0 {
						if textStr, ok := textList[0].(string); ok {
							content = textStr
						}
					}
				}
			}
		}
	}

	// Fallback recursive string search if content is still empty
	if content == "" {
		content = extractTextRecursively(inner)
	}

	return content, thought, convID, respID, choiceID, nil
}

func extractTextRecursively(v interface{}) string {
	switch val := v.(type) {
	case string:
		if len(val) > 20 && !strings.HasPrefix(val, "c_") && !strings.HasPrefix(val, "r_") {
			return val
		}
	case []interface{}:
		for _, item := range val {
			if res := extractTextRecursively(item); res != "" {
				return res
			}
		}
	}
	return ""
}

func quoteJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
