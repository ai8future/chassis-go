// Package inferkit provides a provider-agnostic HTTP client for OpenAI-compatible
// LLM inference APIs. It supports chat completions, streaming (SSE), and embeddings.
// Works with OpenAI, DeepInfra, Groq, Ollama (OpenAI-compat mode), or any server
// implementing /v1/chat/completions and /v1/embeddings.
package inferkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/call"
)

// Provider base URLs for common OpenAI-compatible services.
const (
	OpenAI    = "https://api.openai.com/v1"
	DeepInfra = "https://api.deepinfra.com/v1/openai"
	Groq      = "https://api.groq.com/openai/v1"
)

// --------------------------------------------------------------------------
// Types
// --------------------------------------------------------------------------

// Config holds the client configuration.
type Config struct {
	BaseURL string        `env:"INFER_BASE_URL" default:"https://api.openai.com/v1"`
	APIKey  string        `env:"INFER_API_KEY" required:"false"`
	Model   string        `env:"INFER_MODEL"   required:"false"`
	Timeout time.Duration `env:"INFER_TIMEOUT" default:"120s"`
}

// ChatRequest is the payload for a chat completion.
type ChatRequest struct {
	Model          string          // Override per-request (optional, falls back to Config.Model)
	Messages       []Message       //
	Temperature    *float64        //
	MaxTokens      *int            //
	ResponseFormat *ResponseFormat // Structured output: {"type": "json_object"}
	ExtraBody      map[string]any  // Provider-specific top-level fields (typed fields win on conflict)
}

// Message is a single message in a chat conversation.
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"` //
}

// ResponseFormat controls the output format.
type ResponseFormat struct {
	Type string `json:"type"` // "json_object" or "text"
}

// ChatResponse is the result of a chat completion.
type ChatResponse struct {
	Content      string
	FinishReason string
	Usage        Usage
	Meta         ResponseMeta
}

// Usage reports token consumption.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ResponseMeta captures HTTP-level metadata from the inference API response.
type ResponseMeta struct {
	StatusCode int
	Header     http.Header
}

// ChatChunk is a single piece of a streamed chat response.
type ChatChunk struct {
	Content      string
	FinishReason string
	Done         bool
	Err          error
}

// EmbedRequest is the payload for an embeddings call.
type EmbedRequest struct {
	Model     string         // Override per-request
	Input     []string       // Texts to embed
	ExtraBody map[string]any // Provider-specific top-level fields (typed fields win on conflict)
}

// EmbedResponse contains the embedding vectors.
type EmbedResponse struct {
	Vectors [][]float32 // One vector per input text
	Usage   Usage
	Meta    ResponseMeta
}

// --------------------------------------------------------------------------
// Wire types (match OpenAI JSON format)
// --------------------------------------------------------------------------

type apiChatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
}

type apiChatResponse struct {
	Choices []apiChoice `json:"choices"`
	Usage   apiUsage    `json:"usage"`
}

type apiChoice struct {
	Message      apiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type apiMessage struct {
	Content string `json:"content"`
}

type apiStreamChunk struct {
	Choices []apiStreamChoice `json:"choices"`
}

type apiStreamChoice struct {
	Delta        apiDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type apiDelta struct {
	Content string `json:"content"`
}

type apiEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type apiEmbedResponse struct {
	Data  []apiEmbedData `json:"data"`
	Usage apiUsage       `json:"usage"`
}

type apiEmbedData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// --------------------------------------------------------------------------
// Options
// --------------------------------------------------------------------------

// Option configures a Client.
type Option func(*options)

type options struct {
	maxAttempts int
	baseDelay   time.Duration
	cbName      string
	cbThreshold int
	cbCooldown  time.Duration
	httpClient  *http.Client
}

// WithRetry enables retry with exponential backoff and jitter for transient
// errors (429 rate-limit and 5xx). Retry is handled at the inferkit level so
// that 429 responses are retried (call.Retrier only retries 5xx).
func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
	return func(o *options) {
		if maxAttempts > 1 {
			o.maxAttempts = maxAttempts
		}
		o.baseDelay = baseDelay
	}
}

// WithCircuitBreaker enables a circuit breaker that opens after consecutive
// failures. Delegates to call.CircuitBreaker which provides singleton
// breakers by name, a proper probing state, and OTel instrumentation.
func WithCircuitBreaker(name string, threshold int, cooldown time.Duration) Option {
	return func(o *options) {
		o.cbName = name
		o.cbThreshold = threshold
		o.cbCooldown = cooldown
	}
}

// WithHTTPClient replaces the underlying *http.Client used by the inference
// client. This is useful when you need a custom Transport (e.g. SSRF-safe
// dialer, proxy routing). The custom client is used for both the call.Client
// (Chat, Embed, Ping) and the streaming HTTP client (ChatStream).
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) {
		o.httpClient = hc
	}
}

// --------------------------------------------------------------------------
// Token source
// --------------------------------------------------------------------------

// staticToken implements call.TokenSource for a fixed API key.
type staticToken string

func (t staticToken) Token(_ context.Context) (string, error) {
	return string(t), nil
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for OpenAI-compatible inference APIs.
// It uses call.Client for timeout, circuit breaker, Bearer token injection,
// and OTel tracing on Chat, Embed, and Ping calls. ChatStream uses a plain
// http.Client to avoid imposing a timeout on long-running streams.
type Client struct {
	baseURL     string
	apiKey      string // stored for ChatStream auth
	model       string
	caller      *call.Client  // Chat, Embed, Ping
	streamHTTP  *http.Client  // ChatStream (no timeout)
	maxAttempts int           // inferkit-level retry (429 + 5xx)
	baseDelay   time.Duration //
}

// New creates a new inference client.
func New(cfg Config, opts ...Option) *Client {
	chassis.AssertVersionChecked()

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	// Build call.Client for request-response methods.
	callOpts := []call.Option{call.WithTimeout(timeout)}
	if cfg.APIKey != "" {
		callOpts = append(callOpts, call.WithTokenSource(staticToken(cfg.APIKey)))
	}
	if o.cbName != "" {
		callOpts = append(callOpts, call.WithCircuitBreaker(o.cbName, o.cbThreshold, o.cbCooldown))
	}
	if o.httpClient != nil {
		callOpts = append(callOpts, call.WithHTTPClient(o.httpClient))
	}

	maxAttempts := 1
	if o.maxAttempts > 1 {
		maxAttempts = o.maxAttempts
	}

	streamHTTP := &http.Client{}
	if o.httpClient != nil {
		streamHTTP = o.httpClient
	}

	return &Client{
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		caller:      call.New(callOpts...),
		streamHTTP:  streamHTTP,
		maxAttempts: maxAttempts,
		baseDelay:   o.baseDelay,
	}
}

// --------------------------------------------------------------------------
// API methods
// --------------------------------------------------------------------------

// Ping checks connectivity by hitting the models endpoint.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("inferkit: build request: %w", err)
	}

	resp, err := c.caller.Do(req)
	if err != nil {
		return fmt.Errorf("inferkit: ping: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("inferkit: ping: status %d", resp.StatusCode)
}

// Chat sends a chat completion request and returns the full response.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	apiReq := apiChatRequest{
		Model:          model,
		Messages:       req.Messages,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseFormat: req.ResponseFormat,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("inferkit: marshal request: %w", err)
	}
	body, err = mergeExtraBody(body, req.ExtraBody)
	if err != nil {
		return nil, err
	}

	var apiResp apiChatResponse
	meta, err := c.doWithRetry(ctx, c.baseURL+"/chat/completions", body, &apiResp)
	if err != nil {
		return nil, err
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("inferkit: no choices in response")
	}

	return &ChatResponse{
		Content:      apiResp.Choices[0].Message.Content,
		FinishReason: apiResp.Choices[0].FinishReason,
		Usage: Usage{
			PromptTokens:     apiResp.Usage.PromptTokens,
			CompletionTokens: apiResp.Usage.CompletionTokens,
			TotalTokens:      apiResp.Usage.TotalTokens,
		},
		Meta: meta,
	}, nil
}

// ChatStream sends a streaming chat completion request and returns a channel
// of chunks. The channel is closed when the stream ends. A final chunk with
// Done=true is sent before the channel closes on a clean finish.
//
// ChatStream uses a plain http.Client (not call.Client) to avoid imposing a
// timeout on long-running streams. Auth is set from the configured API key.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest) (*ResponseMeta, <-chan ChatChunk, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	apiReq := apiChatRequest{
		Model:          model,
		Messages:       req.Messages,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseFormat: req.ResponseFormat,
		Stream:         true,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("inferkit: marshal request: %w", err)
	}
	body, err = mergeExtraBody(body, req.ExtraBody)
	if err != nil {
		return nil, nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("inferkit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

	resp, err := c.streamHTTP.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("inferkit: chat stream: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		herr := parseHTTPError(resp)
		resp.Body.Close()
		return nil, nil, herr
	}

	meta := &ResponseMeta{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
	}

	ch := make(chan ChatChunk, 16)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				select {
				case ch <- ChatChunk{Done: true}:
				case <-ctx.Done():
				}
				return
			}

			var chunk apiStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				select {
				case ch <- ChatChunk{Err: fmt.Errorf("inferkit: parse chunk: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			cc := ChatChunk{Content: chunk.Choices[0].Delta.Content}
			if chunk.Choices[0].FinishReason != nil {
				cc.FinishReason = *chunk.Choices[0].FinishReason
			}
			select {
			case ch <- cc:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case ch <- ChatChunk{Err: fmt.Errorf("inferkit: read stream: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return meta, ch, nil
}

// Embed generates embeddings for the input texts.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	apiReq := apiEmbedRequest{
		Model: model,
		Input: req.Input,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("inferkit: marshal request: %w", err)
	}
	body, err = mergeExtraBody(body, req.ExtraBody)
	if err != nil {
		return nil, err
	}

	var apiResp apiEmbedResponse
	meta, err := c.doWithRetry(ctx, c.baseURL+"/embeddings", body, &apiResp)
	if err != nil {
		return nil, err
	}

	// Convert float64 → float32 and order by index.
	vectors := make([][]float32, len(apiResp.Data))
	for _, d := range apiResp.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return nil, fmt.Errorf("inferkit: embedding index %d out of range [0, %d)", d.Index, len(vectors))
		}
		v := make([]float32, len(d.Embedding))
		for i, f := range d.Embedding {
			v[i] = float32(f)
		}
		vectors[d.Index] = v
	}

	return &EmbedResponse{
		Vectors: vectors,
		Usage: Usage{
			PromptTokens:     apiResp.Usage.PromptTokens,
			CompletionTokens: apiResp.Usage.CompletionTokens,
			TotalTokens:      apiResp.Usage.TotalTokens,
		},
		Meta: meta,
	}, nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// setAuth adds the Authorization header (used by ChatStream which bypasses
// call.Client's TokenSource).
func (c *Client) setAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// mergeExtraBody merges extra top-level keys into a JSON-encoded API request.
// Typed fields (already in base) always win — ExtraBody keys are added only if
// the key does not already exist in the base JSON.
func mergeExtraBody(base []byte, extra map[string]any) ([]byte, error) {
	if len(extra) == 0 {
		return base, nil
	}

	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, fmt.Errorf("inferkit: unmarshal for merge: %w", err)
	}

	for k, v := range extra {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("inferkit: re-marshal after merge: %w", err)
	}
	return out, nil
}

// doWithRetry sends a POST with JSON body, decoding the response into dst.
// It retries on transient errors (429 rate-limit, 5xx server errors) with
// exponential backoff and jitter. Each attempt goes through call.Client
// which provides timeout, circuit breaker, token injection, and OTel tracing.
func (c *Client) doWithRetry(ctx context.Context, url string, body []byte, dst any) (ResponseMeta, error) {
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		if attempt > 0 {
			if err := c.backoff(ctx, attempt); err != nil {
				return ResponseMeta{}, err
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return ResponseMeta{}, fmt.Errorf("inferkit: build request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		// Set GetBody so call.Retrier can rewind the body if needed.
		httpReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}

		resp, err := c.caller.Do(httpReq)
		if err != nil {
			// Circuit breaker open — don't retry.
			if errors.Is(err, call.ErrCircuitOpen) {
				return ResponseMeta{}, fmt.Errorf("inferkit: %w", err)
			}
			// Network error — retryable.
			lastErr = fmt.Errorf("inferkit: %w", err)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			meta := ResponseMeta{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
			}
			err := json.NewDecoder(resp.Body).Decode(dst)
			resp.Body.Close()
			if err != nil {
				return ResponseMeta{}, fmt.Errorf("inferkit: decode response: %w", err)
			}
			return meta, nil
		}

		// Non-2xx: parse error, close body.
		lastErr = parseHTTPError(resp)
		resp.Body.Close()

		// Retry on 429 (rate-limit) or 5xx (server error).
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			continue
		}
		return ResponseMeta{}, lastErr // non-retryable (401, 403, etc.)
	}
	return ResponseMeta{}, lastErr
}

// backoff sleeps with exponential delay and jitter, matching the pattern
// in call.Retrier. Returns ctx.Err() if the context is cancelled.
func (c *Client) backoff(ctx context.Context, attempt int) error {
	delay := c.baseDelay
	for range attempt - 1 {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	// Jitter: add random duration in [0, delay/2).
	if half := int64(delay / 2); half > 0 {
		delay += time.Duration(rand.Int64N(half))
	}

	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// httpError is an HTTP-level error with status code for retry decisions.
type httpError struct {
	status int
	detail string
}

func (e *httpError) Error() string {
	if e.detail != "" {
		return e.detail
	}
	return fmt.Sprintf("inferkit: status %d", e.status)
}

// parseHTTPError reads the response body (up to 4KB) and returns a
// descriptive error preserving the status code for retry decisions.
func parseHTTPError(resp *http.Response) *httpError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var apiErr apiErrorBody
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
		return &httpError{
			status: resp.StatusCode,
			detail: fmt.Sprintf("inferkit: %s (status %d)", apiErr.Error.Message, resp.StatusCode),
		}
	}

	detail := strings.TrimSpace(string(body))
	if detail != "" {
		return &httpError{
			status: resp.StatusCode,
			detail: fmt.Sprintf("inferkit: status %d: %s", resp.StatusCode, detail),
		}
	}
	return &httpError{
		status: resp.StatusCode,
		detail: fmt.Sprintf("inferkit: status %d", resp.StatusCode),
	}
}
