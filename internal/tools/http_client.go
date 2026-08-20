package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPClientTool struct{}

func (t *HTTPClientTool) Name() string {
	return "http_request"
}

func (t *HTTPClientTool) Description() string {
	return "Mengirim HTTP request (GET, POST, PUT, DELETE) ke REST API atau URL eksternal."
}

func (t *HTTPClientTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"url": {
				Type:        "string",
				Description: "URL target yang akan dipanggil.",
			},
			"method": {
				Type:        "string",
				Description: "HTTP Method: GET, POST, PUT, DELETE (Default: GET).",
				Enum:        []string{"GET", "POST", "PUT", "DELETE"},
			},
			"body": {
				Type:        "string",
				Description: "Request body (opsional, untuk POST/PUT, misal format JSON).",
			},
		},
		Required: []string{"url"},
	}
}

func (t *HTTPClientTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	urlStr, ok := args["url"].(string)
	if !ok || strings.TrimSpace(urlStr) == "" {
		return "", fmt.Errorf("parameter 'url' wajib diisi")
	}

	method := "GET"
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(strings.TrimSpace(m))
	}

	var bodyReader io.Reader
	if b, ok := args["body"].(string); ok && b != "" {
		bodyReader = bytes.NewBufferString(b)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return "", fmt.Errorf("gagal membuat http request: %w", err)
	}

	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "GoAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gagal membaca response body: %w", err)
	}

	result := string(respBody)
	if len(result) > 5000 {
		result = result[:5000] + "\n...[response truncated]"
	}

	return fmt.Sprintf("HTTP Status: %s (%d)\nResponse:\n%s", resp.Status, resp.StatusCode, result), nil
}
