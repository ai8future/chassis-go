package inferkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/call"
)

func init() {
	chassis.RequireMajor(11)
}

// --------------------------------------------------------------------------
// 1. Ping success
// --------------------------------------------------------------------------

func TestPing_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("expected path /v1/models, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization=Bearer test-key, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 2. Ping unauthorized
// --------------------------------------------------------------------------

func TestPing_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "bad-key"})
	err := client.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

// --------------------------------------------------------------------------
// 3. Chat success
// --------------------------------------------------------------------------

func TestChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected path /v1/chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization=Bearer test-key, got %q", r.Header.Get("Authorization"))
		}

		var req apiChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Model != "gpt-4o" {
			t.Errorf("expected model=gpt-4o, got %q", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "Hello" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{
				Message:      apiMessage{Content: "Hi there!"},
				FinishReason: "stop",
			}},
			Usage: apiUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hi there!" {
		t.Errorf("expected Content='Hi there!', got %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected FinishReason=stop, got %q", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("expected TotalTokens=8, got %d", resp.Usage.TotalTokens)
	}
}

// --------------------------------------------------------------------------
// 4. Chat with per-request model override
// --------------------------------------------------------------------------

func TestChat_ModelOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "gpt-3.5-turbo" {
			t.Errorf("expected model=gpt-3.5-turbo, got %q", req.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "ok"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "gpt-3.5-turbo",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected Content='ok', got %q", resp.Content)
	}
}

// --------------------------------------------------------------------------
// 5. Chat with structured output (response_format)
// --------------------------------------------------------------------------

func TestChat_StructuredOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Errorf("expected response_format.type=json_object, got %+v", req.ResponseFormat)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{
				Message:      apiMessage{Content: `{"name":"Alice","age":30}`},
				FinishReason: "stop",
			}},
			Usage: apiUsage{PromptTokens: 10, CompletionTokens: 12, TotalTokens: 22},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages:       []Message{{Role: "user", Content: "Extract name and age"}},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != `{"name":"Alice","age":30}` {
		t.Errorf("expected JSON content, got %q", resp.Content)
	}
}

// --------------------------------------------------------------------------
// 6. Chat with temperature and max_tokens
// --------------------------------------------------------------------------

func TestChat_TemperatureAndMaxTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Temperature == nil || *req.Temperature != 0.5 {
			t.Errorf("expected temperature=0.5, got %v", req.Temperature)
		}
		if req.MaxTokens == nil || *req.MaxTokens != 100 {
			t.Errorf("expected max_tokens=100, got %v", req.MaxTokens)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "ok"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	temp := 0.5
	maxTok := 100
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages:    []Message{{Role: "user", Content: "Hi"}},
		Temperature: &temp,
		MaxTokens:   &maxTok,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 7. Chat unauthorized (401)
// --------------------------------------------------------------------------

func TestChat_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "bad-key", Model: "gpt-4o"})
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if got := err.Error(); got != "inferkit: Invalid API key (status 401)" {
		t.Errorf("expected API error message, got %q", got)
	}
}

// --------------------------------------------------------------------------
// 8. Chat rate limited (429)
// --------------------------------------------------------------------------

func TestChat_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`))
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
}

// --------------------------------------------------------------------------
// 9. ChatStream success
// --------------------------------------------------------------------------

func TestChatStream_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("expected stream=true")
		}
		if req.Model != "gpt-4o" {
			t.Errorf("expected model=gpt-4o, got %q", req.Model)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		chunks := []string{
			`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	_, ch, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []ChatChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	// 3 content chunks + 1 [DONE] chunk
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Content != "Hello" {
		t.Errorf("expected chunk[0].Content='Hello', got %q", chunks[0].Content)
	}
	if chunks[1].Content != " world" {
		t.Errorf("expected chunk[1].Content=' world', got %q", chunks[1].Content)
	}
	if chunks[2].FinishReason != "stop" {
		t.Errorf("expected chunk[2].FinishReason='stop', got %q", chunks[2].FinishReason)
	}
	if !chunks[3].Done {
		t.Error("expected final chunk to have Done=true")
	}
}

// --------------------------------------------------------------------------
// 10. ChatStream with HTTP error
// --------------------------------------------------------------------------

func TestChatStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"Internal error","type":"server_error"}}`))
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	_, _, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

// --------------------------------------------------------------------------
// 11. Embed success
// --------------------------------------------------------------------------

func TestEmbed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected path /v1/embeddings, got %s", r.URL.Path)
		}

		var req apiEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "text-embedding-ada-002" {
			t.Errorf("expected model=text-embedding-ada-002, got %q", req.Model)
		}
		if len(req.Input) != 1 || req.Input[0] != "Hello world" {
			t.Errorf("unexpected input: %+v", req.Input)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiEmbedResponse{
			Data: []apiEmbedData{{
				Embedding: []float64{0.1, 0.2, 0.3},
				Index:     0,
			}},
			Usage: apiUsage{PromptTokens: 2, TotalTokens: 2},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "text-embedding-ada-002"})
	resp, err := client.Embed(context.Background(), EmbedRequest{
		Input: []string{"Hello world"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(resp.Vectors))
	}
	if len(resp.Vectors[0]) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(resp.Vectors[0]))
	}
	if resp.Vectors[0][0] != float32(0.1) {
		t.Errorf("expected v[0]=0.1, got %f", resp.Vectors[0][0])
	}
	if resp.Usage.PromptTokens != 2 {
		t.Errorf("expected PromptTokens=2, got %d", resp.Usage.PromptTokens)
	}
}

// --------------------------------------------------------------------------
// 12. Embed multiple inputs (verify index ordering)
// --------------------------------------------------------------------------

func TestEmbed_MultipleInputs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return out of order to verify index-based placement.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiEmbedResponse{
			Data: []apiEmbedData{
				{Embedding: []float64{0.7, 0.8}, Index: 1},
				{Embedding: []float64{0.1, 0.2}, Index: 0},
			},
			Usage: apiUsage{PromptTokens: 4, TotalTokens: 4},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "text-embedding-ada-002"})
	resp, err := client.Embed(context.Background(), EmbedRequest{
		Input: []string{"first", "second"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(resp.Vectors))
	}
	// Index 0 should be [0.1, 0.2]
	if resp.Vectors[0][0] != float32(0.1) {
		t.Errorf("expected vectors[0][0]=0.1, got %f", resp.Vectors[0][0])
	}
	// Index 1 should be [0.7, 0.8]
	if resp.Vectors[1][0] != float32(0.7) {
		t.Errorf("expected vectors[1][0]=0.7, got %f", resp.Vectors[1][0])
	}
}

// --------------------------------------------------------------------------
// 13. Retry on 429 then succeed
// --------------------------------------------------------------------------

func TestChat_RetryOnRateLimit(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "finally!"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(
		Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"},
		WithRetry(4, 1*time.Millisecond),
	)
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "finally!" {
		t.Errorf("expected Content='finally!', got %q", resp.Content)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

// --------------------------------------------------------------------------
// 14. No retry on 401
// --------------------------------------------------------------------------

func TestChat_NoRetryOnUnauthorized(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	client := New(
		Config{BaseURL: srv.URL + "/v1", APIKey: "bad-key", Model: "gpt-4o"},
		WithRetry(3, 1*time.Millisecond),
	)
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected 1 attempt (no retry), got %d", got)
	}
}

// --------------------------------------------------------------------------
// 15. Circuit breaker opens after threshold (uses call.CircuitBreaker)
// --------------------------------------------------------------------------

func TestChat_CircuitBreakerOpens(t *testing.T) {
	const cbName = "inferkit-test-cb-opens"
	defer call.RemoveBreaker(cbName)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"server error","type":"server_error"}}`))
	}))
	defer srv.Close()

	client := New(
		Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"},
		WithCircuitBreaker(cbName, 2, 10*time.Second),
	)

	// First two calls fail and count towards threshold.
	for i := 0; i < 2; i++ {
		_, err := client.Chat(context.Background(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "Hello"}},
		})
		if err == nil {
			t.Fatalf("expected error on attempt %d", i+1)
		}
	}

	// Third call should be rejected by the circuit breaker.
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected circuit breaker error, got nil")
	}
	if !errors.Is(err, call.ErrCircuitOpen) {
		t.Errorf("expected call.ErrCircuitOpen, got %q", err)
	}
}

// --------------------------------------------------------------------------
// 16. No auth header when APIKey is empty
// --------------------------------------------------------------------------

func TestChat_NoAuthWhenKeyEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("expected no Authorization header, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "ok"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", Model: "local-model"})
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 17. Embed with out-of-range index returns error (not panic)
// --------------------------------------------------------------------------

func TestEmbed_BadIndexReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiEmbedResponse{
			Data: []apiEmbedData{
				{Embedding: []float64{0.1}, Index: 99}, // out of range
			},
			Usage: apiUsage{PromptTokens: 1, TotalTokens: 1},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "text-embedding-ada-002"})
	_, err := client.Embed(context.Background(), EmbedRequest{
		Input: []string{"test"},
	})
	if err == nil {
		t.Fatal("expected error for out-of-range index, got nil")
	}
}

// --------------------------------------------------------------------------
// 18. Provider constants are valid URLs
// --------------------------------------------------------------------------

func TestProviderConstants(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"OpenAI", OpenAI},
		{"DeepInfra", DeepInfra},
		{"Groq", Groq},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.url == "" {
				t.Error("expected non-empty URL")
			}
			if !hasPrefix(tc.url, "https://") {
				t.Errorf("expected HTTPS URL, got %q", tc.url)
			}
		})
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// --------------------------------------------------------------------------
// 19. mergeExtraBody — nil is no-op
// --------------------------------------------------------------------------

func TestMergeExtraBody_NilIsNoOp(t *testing.T) {
	base := []byte(`{"model":"gpt-4o","messages":[]}`)
	out, err := mergeExtraBody(base, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(base) {
		t.Errorf("expected no change, got %s", out)
	}
}

// --------------------------------------------------------------------------
// 20. mergeExtraBody — adds new keys
// --------------------------------------------------------------------------

func TestMergeExtraBody_AddsKeys(t *testing.T) {
	base := []byte(`{"model":"gpt-4o"}`)
	out, err := mergeExtraBody(base, map[string]any{"service_tier": "flex"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if m["service_tier"] != "flex" {
		t.Errorf("expected service_tier=flex, got %v", m["service_tier"])
	}
	if m["model"] != "gpt-4o" {
		t.Errorf("expected model=gpt-4o, got %v", m["model"])
	}
}

// --------------------------------------------------------------------------
// 21. mergeExtraBody — typed fields win (no override)
// --------------------------------------------------------------------------

func TestMergeExtraBody_TypedFieldsWin(t *testing.T) {
	base := []byte(`{"model":"gpt-4o"}`)
	out, err := mergeExtraBody(base, map[string]any{"model": "nope"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if m["model"] != "gpt-4o" {
		t.Errorf("typed field was overridden: got %v", m["model"])
	}
}

// --------------------------------------------------------------------------
// 22. Chat — ExtraBody key appears in wire JSON
// --------------------------------------------------------------------------

func TestChat_ExtraBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if raw["service_tier"] != "flex" {
			t.Errorf("expected service_tier=flex, got %v", raw["service_tier"])
		}
		if raw["model"] != "gpt-4o" {
			t.Errorf("expected model=gpt-4o, got %v", raw["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "ok"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages:  []Message{{Role: "user", Content: "Hi"}},
		ExtraBody: map[string]any{"service_tier": "flex"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 23. Chat — ExtraBody typed fields win
// --------------------------------------------------------------------------

func TestChat_ExtraBodyTypedFieldsWin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if raw["model"] != "gpt-4o" {
			t.Errorf("expected typed model=gpt-4o to win, got %v", raw["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "ok"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages:  []Message{{Role: "user", Content: "Hi"}},
		ExtraBody: map[string]any{"model": "should-not-win"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 24. Embed — ExtraBody works
// --------------------------------------------------------------------------

func TestEmbed_ExtraBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if raw["encoding_format"] != "float" {
			t.Errorf("expected encoding_format=float, got %v", raw["encoding_format"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiEmbedResponse{
			Data:  []apiEmbedData{{Embedding: []float64{0.1}, Index: 0}},
			Usage: apiUsage{PromptTokens: 1, TotalTokens: 1},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "text-embedding-ada-002"})
	_, err := client.Embed(context.Background(), EmbedRequest{
		Input:     []string{"hello"},
		ExtraBody: map[string]any{"encoding_format": "float"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 25. Chat — ResponseMeta populated
// --------------------------------------------------------------------------

func TestChat_ResponseMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-abc-123")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiChatResponse{
			Choices: []apiChoice{{Message: apiMessage{Content: "ok"}, FinishReason: "stop"}},
			Usage:   apiUsage{},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Meta.StatusCode != 200 {
		t.Errorf("expected StatusCode=200, got %d", resp.Meta.StatusCode)
	}
	if resp.Meta.Header.Get("X-Request-Id") != "req-abc-123" {
		t.Errorf("expected X-Request-Id=req-abc-123, got %q", resp.Meta.Header.Get("X-Request-Id"))
	}
}

// --------------------------------------------------------------------------
// 26. Embed — ResponseMeta populated
// --------------------------------------------------------------------------

func TestEmbed_ResponseMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "embed-req-789")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiEmbedResponse{
			Data:  []apiEmbedData{{Embedding: []float64{0.1}, Index: 0}},
			Usage: apiUsage{PromptTokens: 1, TotalTokens: 1},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "text-embedding-ada-002"})
	resp, err := client.Embed(context.Background(), EmbedRequest{
		Input: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Meta.StatusCode != 200 {
		t.Errorf("expected StatusCode=200, got %d", resp.Meta.StatusCode)
	}
	if resp.Meta.Header.Get("X-Request-Id") != "embed-req-789" {
		t.Errorf("expected X-Request-Id=embed-req-789, got %q", resp.Meta.Header.Get("X-Request-Id"))
	}
}

// --------------------------------------------------------------------------
// 27. ChatStream — ResponseMeta populated
// --------------------------------------------------------------------------

func TestChatStream_ResponseMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "stream-req-456")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	meta, ch, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.StatusCode != 200 {
		t.Errorf("expected StatusCode=200, got %d", meta.StatusCode)
	}
	if meta.Header.Get("X-Request-Id") != "stream-req-456" {
		t.Errorf("expected X-Request-Id=stream-req-456, got %q", meta.Header.Get("X-Request-Id"))
	}
	for range ch {
	}
}

// --------------------------------------------------------------------------
// 28. ChatStream — ExtraBody merged into stream request
// --------------------------------------------------------------------------

func TestChatStream_ExtraBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		if raw["service_tier"] != "flex" {
			t.Errorf("expected service_tier=flex, got %v", raw["service_tier"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "gpt-4o"})
	_, ch, err := client.ChatStream(context.Background(), ChatRequest{
		Messages:  []Message{{Role: "user", Content: "Hi"}},
		ExtraBody: map[string]any{"service_tier": "flex"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
}
