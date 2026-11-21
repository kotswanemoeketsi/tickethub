// Package ai adds language-model features to the platform: smart search, the event
// writer and the event pre-check. It talks to DeepSeek through its OpenAI-compatible chat
// API, so any compatible provider (including a self-hosted DeepSeek model) can be used by
// changing AI_BASE_URL and the model names.
//
// The model only suggests: it never writes to the database, every answer is validated
// before use, and the platform keeps working when the model is unavailable.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request is one chat completion asking for a JSON object.
type Request struct {
	Model       string
	System      string
	User        string
	MaxTokens   int
	Temperature float64
}

// Usage is what a call cost, in tokens.
type Usage struct {
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
}

// Completer runs chat completions. The DeepSeek client implements it; tests may swap it.
type Completer interface {
	Complete(ctx context.Context, req Request) (string, Usage, error)
}

// ErrUnavailable means the provider could not answer (network, outage, rate limit, bad
// key or no credit). Callers fall back to working without AI.
var ErrUnavailable = errors.New("ai: provider unavailable")

// Client calls an OpenAI-compatible chat completions API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient returns a client for baseURL (for example https://api.deepseek.com).
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), apiKey: apiKey, http: &http.Client{Timeout: timeout}}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []chatMessage     `json:"messages"`
	ResponseFormat map[string]string `json:"response_format"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	Temperature    float64           `json:"temperature"`
	Stream         bool              `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"` // DeepSeek
		PromptTokensDetails  struct {
			CachedTokens int `json:"cached_tokens"` // OpenAI-style providers
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// Complete sends req and returns the JSON text of the answer and the tokens used. A
// failure that may clear (rate limit, server error, network, an empty answer) is retried
// once.
func (c *Client) Complete(ctx context.Context, req Request) (string, Usage, error) {
	body, err := json.Marshal(chatRequest{
		Model: req.Model,
		Messages: []chatMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
		MaxTokens:      req.MaxTokens,
		Temperature:    req.Temperature,
	})
	if err != nil {
		return "", Usage{}, err
	}
	// Tokens are billed for every attempt, so the usage adds them up.
	var total Usage
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", total, ctx.Err()
			case <-time.After(800 * time.Millisecond):
			}
		}
		text, usage, retry, err := c.once(ctx, body)
		total.PromptTokens += usage.PromptTokens
		total.CachedTokens += usage.CachedTokens
		total.CompletionTokens += usage.CompletionTokens
		if err == nil {
			return text, total, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return "", total, lastErr
}

func (c *Client) once(ctx context.Context, body []byte) (text string, usage Usage, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, ctx.Err() == nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", Usage{}, true, fmt.Errorf("%w: read response: %v", ErrUnavailable, err)
	}
	switch {
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return "", Usage{}, true, fmt.Errorf("%w: status %d", ErrUnavailable, res.StatusCode)
	case res.StatusCode != http.StatusOK:
		// 401 (bad key), 402 (no credit), 400/422 (a request this code got wrong): retrying
		// won't help. The body is not logged, as it may echo the request.
		return "", Usage{}, false, fmt.Errorf("%w: status %d", ErrUnavailable, res.StatusCode)
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", Usage{}, true, fmt.Errorf("%w: decode response: %v", ErrUnavailable, err)
	}
	usage = Usage{
		PromptTokens:     parsed.Usage.PromptTokens,
		CachedTokens:     max(parsed.Usage.PromptCacheHitTokens, parsed.Usage.PromptTokensDetails.CachedTokens),
		CompletionTokens: parsed.Usage.CompletionTokens,
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		// JSON mode can occasionally return an empty answer; a second try usually works.
		return "", usage, true, fmt.Errorf("%w: empty answer", ErrUnavailable)
	}
	if parsed.Choices[0].FinishReason == "length" {
		return "", usage, false, fmt.Errorf("%w: answer was cut off", ErrUnavailable)
	}
	return parsed.Choices[0].Message.Content, usage, false, nil
}
