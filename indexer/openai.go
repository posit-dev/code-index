// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// openaiClient is a shared HTTP client for OpenAI-compatible APIs.
// Used by both OpenAILLMBackend and OpenAIEmbedder.
type openaiClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// newOpenAIClient creates an HTTP client for an OpenAI-compatible API.
// baseURL defaults to "https://api.openai.com/v1" if empty.
// apiKeyEnv defaults to "OPENAI_API_KEY" if empty.
// The API key is allowed to be empty for local servers like Ollama.
func newOpenAIClient(baseURL, apiKeyEnv string) (*openaiClient, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	if apiKeyEnv == "" {
		apiKeyEnv = "OPENAI_API_KEY"
	}
	apiKey := os.Getenv(apiKeyEnv)

	// Require an API key for non-localhost URLs.
	if apiKey == "" && !isLocalhost(baseURL) {
		return nil, fmt.Errorf("environment variable %s is not set (required for %s)", apiKeyEnv, baseURL)
	}

	return &openaiClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}, nil
}

// post sends a JSON POST request and returns the response body.
// Retries with exponential backoff on 429 and 5xx errors.
func (c *openaiClient) post(ctx context.Context, path string, body interface{}) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := c.baseURL + path

	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = classifyConnectionError(c.baseURL, err)
			// Connection errors on localhost are not transient — Ollama isn't running.
			if isLocalhost(c.baseURL) {
				return nil, lastErr
			}
			// For remote servers, retry on connection errors.
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() //nolint:errcheck
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			return respBody, nil
		}

		lastErr = classifyHTTPError(resp.StatusCode, respBody)

		// Retry on 429 (rate limit) and 5xx (server errors).
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}

		// Non-retryable error.
		return nil, lastErr
	}

	return nil, fmt.Errorf("after retries: %w", lastErr)
}

// classifyConnectionError returns a user-friendly error for connection failures.
func classifyConnectionError(baseURL string, err error) error {
	if isLocalhost(baseURL) {
		return fmt.Errorf("could not connect to %s — if using Ollama, install it from https://ollama.com and start it with `ollama serve`: %w", baseURL, err)
	}
	return fmt.Errorf("connection error: %w", err)
}

// classifyHTTPError returns a user-friendly error for HTTP error responses.
func classifyHTTPError(statusCode int, body []byte) error {
	// Try to extract an error message from the response.
	var errResp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		// Ollama returns 404 when a model isn't pulled.
		if statusCode == http.StatusNotFound {
			msg := errResp.Error.Message
			if strings.Contains(strings.ToLower(msg), "not found") {
				return fmt.Errorf("model not found: %s — run `ollama pull <model>` to download it", msg)
			}
		}
		return fmt.Errorf("API error %d: %s", statusCode, errResp.Error.Message)
	}

	return fmt.Errorf("API error %d: %s", statusCode, string(body))
}

// isLocalhost returns true if the URL points to a local server.
func isLocalhost(baseURL string) bool {
	return strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1")
}
