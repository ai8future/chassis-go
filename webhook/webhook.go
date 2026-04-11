package webhook

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/seal"
)

const maxDeliveries = 10000

var (
	ErrBadSignature = errors.New("webhook: signature verification failed")
	ErrClientError  = errors.New("webhook: client error (4xx)")
	ErrServerError  = errors.New("webhook: server error after all retries")
)

type Delivery struct {
	ID        string
	URL       string
	Status    string // "delivered", "failed", "pending"
	Attempts  int
	LastError string
	SentAt    time.Time
}

type Sender struct {
	mu          sync.Mutex
	maxAttempts int
	deliveries  map[string]*Delivery
	order       []string // insertion order for LRU eviction
	httpClient  *http.Client
}

type Option func(*Sender)

func MaxAttempts(n int) Option {
	return func(s *Sender) { s.maxAttempts = n }
}

func NewSender(opts ...Option) *Sender {
	chassis.AssertVersionChecked()
	s := &Sender{
		maxAttempts: 3,
		deliveries:  make(map[string]*Delivery),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Sender) Send(url string, payload any, secret string) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("webhook: marshal payload: %w", err)
	}

	id := generateID()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sigPayload := timestamp + "." + string(body)
	sig := "sha256=" + seal.Sign([]byte(sigPayload), secret)

	delivery := &Delivery{
		ID:     id,
		URL:    url,
		Status: "pending",
		SentAt: time.Now(),
	}
	s.mu.Lock()
	s.deliveries[id] = delivery
	s.order = append(s.order, id)
	for len(s.deliveries) > maxDeliveries {
		delete(s.deliveries, s.order[0])
		s.order[0] = "" // allow GC of evicted string
		s.order = s.order[1:]
	}
	if cap(s.order) > 2*len(s.order) && cap(s.order) > maxDeliveries {
		compacted := make([]string, len(s.order))
		copy(compacted, s.order)
		s.order = compacted
	}
	s.mu.Unlock()

	var lastErr error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		s.mu.Lock()
		delivery.Attempts = attempt
		s.mu.Unlock()

		req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Id", id)
		req.Header.Set("X-Webhook-Signature", sig)
		req.Header.Set("X-Webhook-Timestamp", timestamp)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < s.maxAttempts {
				delay := 100 * time.Millisecond
			for i := 1; i < attempt; i++ {
				delay *= 2
			}
			time.Sleep(delay + time.Duration(rand.Int64N(int64(delay/2))))
			}
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			s.mu.Lock()
			delivery.Status = "delivered"
			s.mu.Unlock()
			return id, nil
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			s.mu.Lock()
			delivery.Status = "failed"
			delivery.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
			s.mu.Unlock()
			return id, fmt.Errorf("%w: HTTP %d", ErrClientError, resp.StatusCode)
		}

		// 5xx — retry
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		if attempt < s.maxAttempts {
			delay := 100 * time.Millisecond
			for i := 1; i < attempt; i++ {
				delay *= 2
			}
			time.Sleep(delay + time.Duration(rand.Int64N(int64(delay/2))))
		}
	}

	s.mu.Lock()
	delivery.Status = "failed"
	delivery.LastError = lastErr.Error()
	s.mu.Unlock()
	return id, fmt.Errorf("%w: %v", ErrServerError, lastErr)
}

func (s *Sender) Status(id string) (Delivery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deliveries[id]
	if !ok {
		return Delivery{}, false
	}
	return *d, true
}

// VerifyPayload verifies a webhook payload signature on the receive side.
// Rejects payloads with timestamps older than 5 minutes to prevent replay attacks.
func VerifyPayload(headers http.Header, body []byte, secret string) ([]byte, error) {
	sig := headers.Get("X-Webhook-Signature")
	timestamp := headers.Get("X-Webhook-Timestamp")
	if sig == "" || timestamp == "" {
		return nil, ErrBadSignature
	}

	// Reject stale timestamps to prevent replay attacks.
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return nil, ErrBadSignature
	}
	age := time.Since(time.Unix(ts, 0))
	if age < 0 {
		age = -age
	}
	if age > 5*time.Minute {
		return nil, fmt.Errorf("%w: timestamp expired", ErrBadSignature)
	}

	// Strip "sha256=" prefix
	if len(sig) > 7 && sig[:7] == "sha256=" {
		sig = sig[7:]
	}

	sigPayload := timestamp + "." + string(body)
	if !seal.Verify([]byte(sigPayload), sig, secret) {
		return nil, ErrBadSignature
	}

	return body, nil
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		panic("webhook: crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
