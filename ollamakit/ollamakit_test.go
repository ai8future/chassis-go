package ollamakit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
)

func init() {
	chassis.RequireMajor(10)
}

// --------------------------------------------------------------------------
// 1. Ping — success
// --------------------------------------------------------------------------

func TestPing_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected /api/tags, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 2. Chat — success
// --------------------------------------------------------------------------

func TestChat_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected /api/chat, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req chatPayload
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "llama3.2" {
			t.Errorf("expected model=llama3.2, got %q", req.Model)
		}
		if req.Stream {
			t.Error("expected stream=false")
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatAPIResponse{
			Model:           "llama3.2",
			Message:         Message{Role: "assistant", Content: "Hi there!"},
			Done:            true,
			TotalDuration:   500000000,
			PromptEvalCount: 5,
			EvalCount:       10,
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hi there!" {
		t.Errorf("expected content='Hi there!', got %q", resp.Content)
	}
	if resp.Model != "llama3.2" {
		t.Errorf("expected model=llama3.2, got %q", resp.Model)
	}
	if resp.EvalCount != 10 {
		t.Errorf("expected eval_count=10, got %d", resp.EvalCount)
	}
}

// --------------------------------------------------------------------------
// 3. Chat — model override
// --------------------------------------------------------------------------

func TestChat_ModelOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatPayload
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "mistral" {
			t.Errorf("expected model=mistral, got %q", req.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatAPIResponse{
			Model:   "mistral",
			Message: Message{Role: "assistant", Content: "ok"},
			Done:    true,
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "mistral",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != "mistral" {
		t.Errorf("expected model=mistral, got %q", resp.Model)
	}
}

// --------------------------------------------------------------------------
// 4. Chat — model not found
// --------------------------------------------------------------------------

func TestChat_ModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'nonexistent' not found, try pulling it first"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	_, err := client.Chat(context.Background(), ChatRequest{
		Model:    "nonexistent",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 5. ChatStream — NDJSON streaming
// --------------------------------------------------------------------------

func TestChatStream_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected /api/chat, got %s", r.URL.Path)
		}

		var req chatPayload
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("expected stream=true")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("server does not support flushing")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")

		chunks := []string{
			`{"model":"llama3.2","message":{"role":"assistant","content":"Hello"},"done":false}`,
			`{"model":"llama3.2","message":{"role":"assistant","content":" world"},"done":false}`,
			`{"model":"llama3.2","message":{"role":"assistant","content":"!"},"done":false}`,
			`{"model":"llama3.2","message":{"role":"assistant","content":""},"done":true}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	ch, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var content string
	var count int
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		content += chunk.Content
		count++
	}

	if content != "Hello world!" {
		t.Errorf("expected 'Hello world!', got %q", content)
	}
	if count != 4 {
		t.Errorf("expected 4 chunks, got %d", count)
	}
}

// --------------------------------------------------------------------------
// 6. ChatStream — error status
// --------------------------------------------------------------------------

func TestChatStream_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'bad' not found"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	_, err := client.ChatStream(context.Background(), ChatRequest{
		Model:    "bad",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 7. ChatStream — context cancellation
// --------------------------------------------------------------------------

func TestChatStream_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"model":"llama3.2","message":{"role":"assistant","content":"tok1"},"done":false}`)
		flusher.Flush()
		// Block until client disconnects
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	ch, err := client.ChatStream(ctx, ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read first chunk
	chunk := <-ch
	if chunk.Content != "tok1" {
		t.Errorf("expected tok1, got %q", chunk.Content)
	}

	// Cancel context
	cancel()

	// Channel should drain and close
	for range ch {
	}
}

// --------------------------------------------------------------------------
// 8. Generate — success
// --------------------------------------------------------------------------

func TestGenerate_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("expected /api/generate, got %s", r.URL.Path)
		}

		var req generatePayload
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "llama3.2" {
			t.Errorf("expected model=llama3.2, got %q", req.Model)
		}
		if req.Prompt != "Why is the sky blue?" {
			t.Errorf("unexpected prompt: %q", req.Prompt)
		}
		if req.System != "Be concise." {
			t.Errorf("unexpected system: %q", req.System)
		}
		if req.Stream {
			t.Error("expected stream=false")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(generateAPIResponse{
			Model:           "llama3.2",
			Response:        "Rayleigh scattering.",
			Done:            true,
			TotalDuration:   300000000,
			PromptEvalCount: 8,
			EvalCount:       3,
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	resp, err := client.Generate(context.Background(), GenerateRequest{
		Prompt: "Why is the sky blue?",
		System: "Be concise.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Response != "Rayleigh scattering." {
		t.Errorf("expected 'Rayleigh scattering.', got %q", resp.Response)
	}
	if resp.PromptEvalCount != 8 {
		t.Errorf("expected prompt_eval_count=8, got %d", resp.PromptEvalCount)
	}
}

// --------------------------------------------------------------------------
// 9. Embed — success
// --------------------------------------------------------------------------

func TestEmbed_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("expected /api/embed, got %s", r.URL.Path)
		}

		var req embedPayload
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Input) != 2 {
			t.Errorf("expected 2 inputs, got %d", len(req.Input))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(embedAPIResponse{
			Model:      "all-minilm",
			Embeddings: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "all-minilm"})
	resp, err := client.Embed(context.Background(), EmbedRequest{
		Input: []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(resp.Vectors))
	}
	if resp.Vectors[0][0] != 0.1 {
		t.Errorf("expected first element=0.1, got %f", resp.Vectors[0][0])
	}
	if resp.Model != "all-minilm" {
		t.Errorf("expected model=all-minilm, got %q", resp.Model)
	}
}

// --------------------------------------------------------------------------
// 10. ListModels — success
// --------------------------------------------------------------------------

func TestListModels_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected /api/tags, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"models": [
				{
					"name": "llama3.2:latest",
					"model": "llama3.2:latest",
					"modified_at": "2025-01-15T10:00:00Z",
					"size": 4661224676,
					"digest": "abc123",
					"details": {
						"format": "gguf",
						"family": "llama",
						"families": ["llama"],
						"parameter_size": "7B",
						"quantization_level": "Q4_0"
					}
				},
				{
					"name": "mistral:latest",
					"model": "mistral:latest",
					"modified_at": "2025-01-10T08:00:00Z",
					"size": 3800000000,
					"digest": "def456",
					"details": {
						"format": "gguf",
						"family": "mistral",
						"families": ["mistral"],
						"parameter_size": "7B",
						"quantization_level": "Q4_0"
					}
				}
			]
		}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "llama3.2:latest" {
		t.Errorf("expected name=llama3.2:latest, got %q", models[0].Name)
	}
	if models[0].Size != 4661224676 {
		t.Errorf("expected size=4661224676, got %d", models[0].Size)
	}
	if models[0].Details.Family != "llama" {
		t.Errorf("expected family=llama, got %q", models[0].Details.Family)
	}
	if models[1].Name != "mistral:latest" {
		t.Errorf("expected name=mistral:latest, got %q", models[1].Name)
	}
}

// --------------------------------------------------------------------------
// 11. ShowModel — success
// --------------------------------------------------------------------------

func TestShowModel_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("expected /api/show, got %s", r.URL.Path)
		}

		var req showPayload
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "llama3.2" {
			t.Errorf("expected model=llama3.2, got %q", req.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(showAPIResponse{
			License:    "MIT",
			Modelfile:  "FROM llama3.2",
			Parameters: "num_ctx 4096",
			Template:   "{{ .System }}\n{{ .Prompt }}",
			Details: modelDetailsJSON{
				Format:            "gguf",
				Family:            "llama",
				Families:          []string{"llama"},
				ParameterSize:     "7B",
				QuantizationLevel: "Q4_0",
			},
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	detail, err := client.ShowModel(context.Background(), "llama3.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.License != "MIT" {
		t.Errorf("expected license=MIT, got %q", detail.License)
	}
	if detail.Details.ParameterSize != "7B" {
		t.Errorf("expected parameter_size=7B, got %q", detail.Details.ParameterSize)
	}
	if detail.Template != "{{ .System }}\n{{ .Prompt }}" {
		t.Errorf("unexpected template: %q", detail.Template)
	}
}

// --------------------------------------------------------------------------
// 12. ShowModel — not found
// --------------------------------------------------------------------------

func TestShowModel_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'nonexistent' not found"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	_, err := client.ShowModel(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 13. PullModel — success
// --------------------------------------------------------------------------

func TestPullModel_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("expected /api/pull, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req pullPayload
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "llama3.2" {
			t.Errorf("expected model=llama3.2, got %q", req.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	err := client.PullModel(context.Background(), "llama3.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 14. PullModel — not found
// --------------------------------------------------------------------------

func TestPullModel_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"pull model manifest: file does not exist"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	err := client.PullModel(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 15. ShowModel — 404 preserves error detail
// --------------------------------------------------------------------------

func TestShowModel_NotFoundIncludesDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'xyz' not found, try pulling it first"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	_, err := client.ShowModel(context.Background(), "xyz")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got: %v", err)
	}
	if !strings.Contains(err.Error(), "try pulling it first") {
		t.Errorf("expected error to include Ollama detail, got: %q", err.Error())
	}
}

// --------------------------------------------------------------------------
// 16. Negative timeout uses default
// --------------------------------------------------------------------------

func TestNew_NegativeTimeout(t *testing.T) {
	client := New(Config{Timeout: -5 * time.Second})
	if client.http.Timeout != 120*time.Second {
		t.Errorf("expected negative timeout to use default 120s, got %v", client.http.Timeout)
	}
}

// --------------------------------------------------------------------------
// 17. Client defaults
// --------------------------------------------------------------------------

func TestNew_Defaults(t *testing.T) {
	client := New(Config{})
	if client.host != "http://localhost:11434" {
		t.Errorf("expected default host, got %q", client.host)
	}
	if client.model != "llama3.2" {
		t.Errorf("expected default model=llama3.2, got %q", client.model)
	}
	if client.http.Timeout != 120*time.Second {
		t.Errorf("expected default timeout=120s, got %v", client.http.Timeout)
	}
}

// --------------------------------------------------------------------------
// 18. WithTimeout option
// --------------------------------------------------------------------------

func TestWithTimeout(t *testing.T) {
	client := New(Config{}, WithTimeout(5*time.Second))
	if client.http.Timeout != 5*time.Second {
		t.Errorf("expected timeout=5s, got %v", client.http.Timeout)
	}
}

// --------------------------------------------------------------------------
// 19. HTTP timeout
// --------------------------------------------------------------------------

func TestChat_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL}, WithTimeout(50*time.Millisecond))
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, ErrOllamaUnavailable) {
		t.Errorf("expected ErrOllamaUnavailable, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 20. Ollama error response (non-404)
// --------------------------------------------------------------------------

func TestChat_OllamaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"out of memory"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expected := "ollamakit: out of memory"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

// --------------------------------------------------------------------------
// 21. Host trailing slash trimmed
// --------------------------------------------------------------------------

func TestNew_TrimsTrailingSlash(t *testing.T) {
	client := New(Config{Host: "http://localhost:11434/"})
	if client.host != "http://localhost:11434" {
		t.Errorf("expected trailing slash trimmed, got %q", client.host)
	}
}

// --------------------------------------------------------------------------
// 22. ChatStream — malformed NDJSON mid-stream
// --------------------------------------------------------------------------

func TestChatStream_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"model":"llama3.2","message":{"role":"assistant","content":"Hi"},"done":false}`)
		flusher.Flush()
		fmt.Fprintln(w, `{corrupt json!!!}`)
		flusher.Flush()
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	ch, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First chunk should be valid
	chunk := <-ch
	if chunk.Err != nil {
		t.Fatalf("expected valid first chunk, got error: %v", chunk.Err)
	}
	if chunk.Content != "Hi" {
		t.Errorf("expected content='Hi', got %q", chunk.Content)
	}

	// Second chunk should carry the decode error
	chunk = <-ch
	if chunk.Err == nil {
		t.Fatal("expected error chunk for malformed JSON, got nil Err")
	}
	if !strings.Contains(chunk.Err.Error(), "decode chunk") {
		t.Errorf("expected 'decode chunk' in error, got: %v", chunk.Err)
	}

	// Channel should close after error
	_, open := <-ch
	if open {
		t.Error("expected channel to be closed after error")
	}
}

// --------------------------------------------------------------------------
// 23. ListModels — empty response
// --------------------------------------------------------------------------

func TestListModels_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if models == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

// --------------------------------------------------------------------------
// 24. Chat — multi-turn conversation serialized correctly
// --------------------------------------------------------------------------

func TestChat_MultiTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatPayload
		json.NewDecoder(r.Body).Decode(&req)

		if len(req.Messages) != 4 {
			t.Errorf("expected 4 messages, got %d", len(req.Messages))
		}
		expected := []struct{ role, content string }{
			{"system", "You are helpful."},
			{"user", "What is 2+2?"},
			{"assistant", "4"},
			{"user", "And 3+3?"},
		}
		for i, e := range expected {
			if i >= len(req.Messages) {
				break
			}
			if req.Messages[i].Role != e.role {
				t.Errorf("message[%d] role: expected %q, got %q", i, e.role, req.Messages[i].Role)
			}
			if req.Messages[i].Content != e.content {
				t.Errorf("message[%d] content: expected %q, got %q", i, e.content, req.Messages[i].Content)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatAPIResponse{
			Model:   "llama3.2",
			Message: Message{Role: "assistant", Content: "6"},
			Done:    true,
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "What is 2+2?"},
			{Role: "assistant", Content: "4"},
			{Role: "user", Content: "And 3+3?"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "6" {
		t.Errorf("expected '6', got %q", resp.Content)
	}
}

// --------------------------------------------------------------------------
// 25. Generate — empty system omitted from JSON
// --------------------------------------------------------------------------

func TestGenerate_EmptySystemOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"system"`) {
			t.Error("expected 'system' field to be omitted when empty")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(generateAPIResponse{
			Model:    "llama3.2",
			Response: "ok",
			Done:     true,
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	_, err := client.Generate(context.Background(), GenerateRequest{
		Prompt: "hello",
		// System intentionally empty
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 26. Ping — error status returns Ollama message
// --------------------------------------------------------------------------

func TestPing_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"GPU not available"}`)
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL})
	err := client.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "GPU not available") {
		t.Errorf("expected Ollama error message in output, got: %q", err.Error())
	}
}

// --------------------------------------------------------------------------
// 27. ChatStream — server drops connection mid-stream
// --------------------------------------------------------------------------

func TestChatStream_ServerDisconnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"model":"llama3.2","message":{"role":"assistant","content":"partial"},"done":false}`)
		flusher.Flush()
		// Hijack and close the connection abruptly
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "llama3.2"})
	ch, err := client.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First chunk should arrive
	chunk := <-ch
	if chunk.Err != nil {
		t.Fatalf("expected valid first chunk, got error: %v", chunk.Err)
	}
	if chunk.Content != "partial" {
		t.Errorf("expected 'partial', got %q", chunk.Content)
	}

	// Channel should close (scanner hits EOF or error from broken conn).
	// May get an error chunk or just a closed channel depending on timing.
	for c := range ch {
		// If there's a trailing chunk, it should be an error or done
		if c.Err == nil && !c.Done && c.Content != "" {
			t.Errorf("unexpected chunk after disconnect: %+v", c)
		}
	}
}

// --------------------------------------------------------------------------
// 28. Embed — single input
// --------------------------------------------------------------------------

func TestEmbed_SingleInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedPayload
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Input) != 1 {
			t.Errorf("expected 1 input, got %d", len(req.Input))
		}
		if req.Input[0] != "single text" {
			t.Errorf("expected 'single text', got %q", req.Input[0])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(embedAPIResponse{
			Model:      "all-minilm",
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		})
	}))
	defer srv.Close()

	client := New(Config{Host: srv.URL, Model: "all-minilm"})
	resp, err := client.Embed(context.Background(), EmbedRequest{
		Input: []string{"single text"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(resp.Vectors))
	}
	if len(resp.Vectors[0]) != 3 {
		t.Errorf("expected 3 dimensions, got %d", len(resp.Vectors[0]))
	}
}
