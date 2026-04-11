// Package ollamakit provides an HTTP client for Ollama's native /api/ endpoints.
// Chat, generate, embeddings, and model management over raw HTTP.
// Zero external dependencies -- just chassis internals and stdlib.
package ollamakit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

// --------------------------------------------------------------------------
// Sentinel errors
// --------------------------------------------------------------------------

// ErrModelNotFound is returned when the requested model does not exist locally.
var ErrModelNotFound = errors.New("ollamakit: model not found")

// ErrOllamaUnavailable is returned when the Ollama server cannot be reached.
var ErrOllamaUnavailable = errors.New("ollamakit: ollama unavailable")

// --------------------------------------------------------------------------
// Config
// --------------------------------------------------------------------------

// Config holds the Ollama connection settings.
type Config struct {
	Host     string        `env:"OLLAMA_HOST" default:"http://localhost:11434"`
	Model    string        `env:"OLLAMA_MODEL" default:"llama3.2"`
	Timeout  time.Duration `env:"OLLAMA_TIMEOUT" default:"120s"`
	AutoPull bool          `env:"OLLAMA_AUTO_PULL" default:"false"`
}

// --------------------------------------------------------------------------
// Public types
// --------------------------------------------------------------------------

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the input for Chat and ChatStream.
type ChatRequest struct {
	Model    string
	Messages []Message
}

// ChatResponse is the result of a non-streaming chat call.
type ChatResponse struct {
	Content         string
	Model           string
	TotalDuration   time.Duration
	PromptEvalCount int
	EvalCount       int
}

// ChatChunk is a single piece of a streaming chat response.
type ChatChunk struct {
	Content string
	Done    bool
	Err     error
}

// GenerateRequest is the input for Generate.
type GenerateRequest struct {
	Model  string
	Prompt string
	System string
}

// GenerateResponse is the result of a non-streaming generate call.
type GenerateResponse struct {
	Response        string
	Model           string
	TotalDuration   time.Duration
	PromptEvalCount int
	EvalCount       int
}

// EmbedRequest is the input for Embed.
type EmbedRequest struct {
	Model string
	Input []string
}

// EmbedResponse is the result of an embedding call.
type EmbedResponse struct {
	Vectors [][]float32
	Model   string
}

// ModelInfo is a summary of a locally available model.
type ModelInfo struct {
	Name       string
	Model      string
	ModifiedAt time.Time
	Size       int64
	Digest     string
	Details    ModelDetails
}

// ModelDetails holds architecture metadata for a model.
type ModelDetails struct {
	Format            string
	Family            string
	Families          []string
	ParameterSize     string
	QuantizationLevel string
}

// ModelDetail is the full information returned by ShowModel.
type ModelDetail struct {
	License    string
	Modelfile  string
	Parameters string
	Template   string
	Details    ModelDetails
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for an Ollama server.
type Client struct {
	host  string
	model string
	http  *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout overrides the default HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.http.Timeout = d }
}

// New creates a new Ollama client.
func New(cfg Config, opts ...Option) *Client {
	chassis.AssertVersionChecked()
	host := cfg.Host
	if host == "" {
		host = "http://localhost:11434"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	model := cfg.Model
	if model == "" {
		model = "llama3.2"
	}
	c := &Client{
		host:  strings.TrimRight(host, "/"),
		model: model,
		http:  &http.Client{Timeout: timeout},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// --------------------------------------------------------------------------
// Ping
// --------------------------------------------------------------------------

// Ping checks that Ollama is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("ollamakit: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOllamaUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeError(resp)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// --------------------------------------------------------------------------
// Chat
// --------------------------------------------------------------------------

// Chat sends a non-streaming chat request and returns the complete response.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	payload := chatPayload{
		Model:    c.resolveModel(req.Model),
		Messages: req.Messages,
		Stream:   false,
	}

	var raw chatAPIResponse
	if err := c.postJSON(ctx, "/api/chat", payload, &raw); err != nil {
		return nil, err
	}

	return &ChatResponse{
		Content:         raw.Message.Content,
		Model:           raw.Model,
		TotalDuration:   time.Duration(raw.TotalDuration),
		PromptEvalCount: raw.PromptEvalCount,
		EvalCount:       raw.EvalCount,
	}, nil
}

// ChatStream sends a streaming chat request. Returns a channel of chunks
// parsed from Ollama's NDJSON streaming format. The channel is closed when
// the stream ends or the context is cancelled. Callers must either drain
// the channel or cancel the context to avoid leaking the reader goroutine.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	payload := chatPayload{
		Model:    c.resolveModel(req.Model),
		Messages: req.Messages,
		Stream:   true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ollamakit: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollamakit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOllamaUnavailable, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, decodeError(resp)
	}

	ch := make(chan ChatChunk, 16)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var raw chatStreamChunk
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				select {
				case ch <- ChatChunk{Err: fmt.Errorf("ollamakit: decode chunk: %w", err)}:
				case <-ctx.Done():
				}
				return
			}

			chunk := ChatChunk{
				Content: raw.Message.Content,
				Done:    raw.Done,
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
			if raw.Done {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case ch <- ChatChunk{Err: fmt.Errorf("ollamakit: read stream: %w", err)}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

// --------------------------------------------------------------------------
// Generate
// --------------------------------------------------------------------------

// Generate sends a non-streaming text generation request.
func (c *Client) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	payload := generatePayload{
		Model:  c.resolveModel(req.Model),
		Prompt: req.Prompt,
		System: req.System,
		Stream: false,
	}

	var raw generateAPIResponse
	if err := c.postJSON(ctx, "/api/generate", payload, &raw); err != nil {
		return nil, err
	}

	return &GenerateResponse{
		Response:        raw.Response,
		Model:           raw.Model,
		TotalDuration:   time.Duration(raw.TotalDuration),
		PromptEvalCount: raw.PromptEvalCount,
		EvalCount:       raw.EvalCount,
	}, nil
}

// --------------------------------------------------------------------------
// Embed
// --------------------------------------------------------------------------

// Embed returns embedding vectors for the given input texts.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	payload := embedPayload{
		Model: c.resolveModel(req.Model),
		Input: req.Input,
	}

	var raw embedAPIResponse
	if err := c.postJSON(ctx, "/api/embed", payload, &raw); err != nil {
		return nil, err
	}

	return &EmbedResponse{
		Vectors: raw.Embeddings,
		Model:   raw.Model,
	}, nil
}

// --------------------------------------------------------------------------
// Model management
// --------------------------------------------------------------------------

// ListModels returns all locally available models.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollamakit: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOllamaUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}

	var raw listAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ollamakit: decode models: %w", err)
	}

	models := make([]ModelInfo, len(raw.Models))
	for i, m := range raw.Models {
		models[i] = ModelInfo{
			Name:       m.Name,
			Model:      m.Model,
			ModifiedAt: m.ModifiedAt,
			Size:       m.Size,
			Digest:     m.Digest,
			Details: ModelDetails{
				Format:            m.Details.Format,
				Family:            m.Details.Family,
				Families:          m.Details.Families,
				ParameterSize:     m.Details.ParameterSize,
				QuantizationLevel: m.Details.QuantizationLevel,
			},
		}
	}
	return models, nil
}

// ShowModel returns detailed information about a model.
// Returns ErrModelNotFound when the model does not exist.
func (c *Client) ShowModel(ctx context.Context, name string) (*ModelDetail, error) {
	payload := showPayload{Model: name}

	var raw showAPIResponse
	if err := c.postJSON(ctx, "/api/show", payload, &raw); err != nil {
		return nil, err
	}

	return &ModelDetail{
		License:    raw.License,
		Modelfile:  raw.Modelfile,
		Parameters: raw.Parameters,
		Template:   raw.Template,
		Details: ModelDetails{
			Format:            raw.Details.Format,
			Family:            raw.Details.Family,
			Families:          raw.Details.Families,
			ParameterSize:     raw.Details.ParameterSize,
			QuantizationLevel: raw.Details.QuantizationLevel,
		},
	}, nil
}

// PullModel downloads a model from the Ollama registry. Blocks until complete.
// Returns ErrModelNotFound when the model does not exist in the registry.
func (c *Client) PullModel(ctx context.Context, name string) error {
	payload := pullPayload{Model: name, Stream: false}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("ollamakit: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollamakit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOllamaUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeError(resp)
	}

	io.Copy(io.Discard, resp.Body)
	return nil
}

// --------------------------------------------------------------------------
// Internal API types
// --------------------------------------------------------------------------

type chatPayload struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatAPIResponse struct {
	Model           string  `json:"model"`
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	TotalDuration   int64   `json:"total_duration"`
	PromptEvalCount int     `json:"prompt_eval_count"`
	EvalCount       int     `json:"eval_count"`
}

type chatStreamChunk struct {
	Model   string  `json:"model"`
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

type generatePayload struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream bool   `json:"stream"`
}

type generateAPIResponse struct {
	Model           string `json:"model"`
	Response        string `json:"response"`
	Done            bool   `json:"done"`
	TotalDuration   int64  `json:"total_duration"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

type embedPayload struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedAPIResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

type listAPIResponse struct {
	Models []modelJSON `json:"models"`
}

type modelJSON struct {
	Name       string           `json:"name"`
	Model      string           `json:"model"`
	ModifiedAt time.Time        `json:"modified_at"`
	Size       int64            `json:"size"`
	Digest     string           `json:"digest"`
	Details    modelDetailsJSON `json:"details"`
}

type modelDetailsJSON struct {
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type showPayload struct {
	Model string `json:"model"`
}

type showAPIResponse struct {
	License    string           `json:"license"`
	Modelfile  string           `json:"modelfile"`
	Parameters string           `json:"parameters"`
	Template   string           `json:"template"`
	Details    modelDetailsJSON `json:"details"`
}

type pullPayload struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

func (c *Client) resolveModel(override string) string {
	if override != "" {
		return override
	}
	return c.model
}

func (c *Client) postJSON(ctx context.Context, path string, reqBody, respBody any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ollamakit: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollamakit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOllamaUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeError(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
		return fmt.Errorf("ollamakit: decode response: %w", err)
	}
	return nil
}

func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var errResp struct {
		Error string `json:"error"`
	}
	json.Unmarshal(body, &errResp) // best-effort parse

	if resp.StatusCode == http.StatusNotFound ||
		(errResp.Error != "" && strings.Contains(strings.ToLower(errResp.Error), "not found")) {
		if errResp.Error != "" {
			return fmt.Errorf("%w: %s", ErrModelNotFound, errResp.Error)
		}
		return ErrModelNotFound
	}

	if errResp.Error != "" {
		return fmt.Errorf("ollamakit: %s", errResp.Error)
	}

	return fmt.Errorf("ollamakit: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
