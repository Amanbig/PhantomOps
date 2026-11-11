// Package llm refines rule-based incident investigations with a language
// model reached through an OpenAI-compatible chat completions API (OpenAI,
// Ollama's /v1 endpoint, vLLM, LM Studio, ...).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxResponseBytes = 2 << 20

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client talks to an OpenAI-compatible API.
type Client struct {
	base   string
	model  string
	apiKey string
	http   *http.Client
}

// NewClient creates a client. baseURL includes the version prefix, e.g.
// http://localhost:11434/v1. apiKey may be empty (local servers).
func NewClient(baseURL, model, apiKey string) *Client {
	return &Client{
		base:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:  strings.TrimSpace(model),
		apiKey: strings.TrimSpace(apiKey),
		http:   &http.Client{Timeout: 4 * time.Minute}, // local models can be slow
	}
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.model }

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return resp.StatusCode, data, err
}

func apiError(status int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return fmt.Errorf("HTTP %d: %s", status, msg)
}

// Models lists the model IDs the server offers (sorted).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, apiError(status, body)
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	ids := make([]string, 0, len(r.Data))
	for _, m := range r.Data {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []Message         `json:"messages"`
	Temperature    float64           `json:"temperature"`
	Stream         bool              `json:"stream"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

// Chat sends the conversation and returns the assistant's text. It asks for
// JSON output and transparently retries without that hint if the server
// rejects it.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	req := chatRequest{Model: c.model, Messages: msgs, Temperature: 0.1,
		ResponseFormat: map[string]string{"type": "json_object"}}
	status, body, err := c.do(ctx, http.MethodPost, "/chat/completions", req)
	if err != nil {
		return "", err
	}
	if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
		req.ResponseFormat = nil
		if status, body, err = c.do(ctx, http.MethodPost, "/chat/completions", req); err != nil {
			return "", err
		}
	}
	if status != http.StatusOK {
		return "", apiError(status, body)
	}
	var r struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(r.Choices) == 0 || strings.TrimSpace(r.Choices[0].Message.Content) == "" {
		return "", errors.New("model returned an empty response")
	}
	return r.Choices[0].Message.Content, nil
}
