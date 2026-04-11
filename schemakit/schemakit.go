// Package schemakit provides Avro schema loading, validation, serialization,
// deserialization, and registration with a Schema Registry (e.g. Redpanda).
package schemakit

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hamba/avro/v2"
)

// Schema represents a registered Avro schema for an event subject.
type Schema struct {
	Subject  string
	Version  int
	AvroJSON string
	SchemaID int // assigned by Schema Registry (0 = not yet registered)

	parsed avro.Schema // cached parsed schema
}

// Registry manages schema registration and validation.
type Registry struct {
	url    string
	cache  map[string]*Schema
	mu     sync.RWMutex
	client *http.Client
}

// NewRegistry creates a new schema registry client. The schemaRegistryURL is
// the base URL of the Schema Registry (e.g. "http://localhost:8081").
func NewRegistry(schemaRegistryURL string) (*Registry, error) {
	if schemaRegistryURL == "" {
		return nil, fmt.Errorf("schemakit: schema registry URL must not be empty")
	}
	return &Registry{
		url:    strings.TrimRight(schemaRegistryURL, "/"),
		cache:  make(map[string]*Schema),
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// GetSchema returns a previously loaded schema by subject key, or nil if not found.
func (r *Registry) GetSchema(subject string) *Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cache[subject]
}

// LoadSchemas walks the given directory recursively, reads all .avsc files,
// parses the Avro schema from each, and stores them keyed by "namespace.name".
func (r *Registry) LoadSchemas(schemasDir string) error {
	return filepath.Walk(schemasDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".avsc" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("schemakit: read %s: %w", path, err)
		}

		// Parse the raw JSON to extract namespace and name for the subject key.
		var raw struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("schemakit: parse JSON from %s: %w", path, err)
		}
		if raw.Name == "" {
			return fmt.Errorf("schemakit: schema in %s has no name", path)
		}

		subject := raw.Name
		if raw.Namespace != "" {
			subject = raw.Namespace + "." + raw.Name
		}

		// Parse with hamba/avro to validate and cache the parsed schema.
		parsed, err := avro.Parse(string(data))
		if err != nil {
			return fmt.Errorf("schemakit: parse avro schema from %s: %w", path, err)
		}

		schema := &Schema{
			Subject:  subject,
			Version:  1,
			AvroJSON: string(data),
			SchemaID: 0,
			parsed:   parsed,
		}
		r.mu.Lock()
		r.cache[subject] = schema
		r.mu.Unlock()
		return nil
	})
}

// Validate checks that the given data map is valid according to the schema.
// It does this by attempting to serialize the data; any missing or mistyped
// fields will cause an error.
func (r *Registry) Validate(schema *Schema, data map[string]any) error {
	if schema.parsed == nil {
		return fmt.Errorf("schemakit: schema %q has no parsed schema", schema.Subject)
	}
	_, err := avro.Marshal(schema.parsed, data)
	if err != nil {
		return fmt.Errorf("schemakit: validation failed for %s: %w", schema.Subject, err)
	}
	return nil
}

// Serialize encodes data according to the schema and prepends the Confluent
// wire format header: magic byte (0x00) + 4-byte schema ID (big-endian).
func (r *Registry) Serialize(schema *Schema, data map[string]any) ([]byte, error) {
	if schema.parsed == nil {
		return nil, fmt.Errorf("schemakit: schema %q has no parsed schema", schema.Subject)
	}

	payload, err := avro.Marshal(schema.parsed, data)
	if err != nil {
		return nil, fmt.Errorf("schemakit: serialize %s: %w", schema.Subject, err)
	}

	// Build wire format: 0x00 + 4-byte big-endian schema ID + avro payload
	var buf bytes.Buffer
	buf.WriteByte(0x00)
	idBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(idBytes, uint32(r.schemaID(schema)))
	buf.Write(idBytes)
	buf.Write(payload)

	return buf.Bytes(), nil
}

// Deserialize reads the Confluent wire format header to extract the schema ID,
// looks up the corresponding schema, and decodes the Avro payload.
func (r *Registry) Deserialize(raw []byte) (map[string]any, error) {
	if len(raw) < 5 {
		return nil, fmt.Errorf("schemakit: payload too short (%d bytes), need at least 5", len(raw))
	}
	if raw[0] != 0x00 {
		return nil, fmt.Errorf("schemakit: invalid magic byte 0x%02x, expected 0x00", raw[0])
	}

	schemaID := int(binary.BigEndian.Uint32(raw[1:5]))

	// Find schema by ID in cache
	r.mu.RLock()
	var schema *Schema
	for _, s := range r.cache {
		if s.SchemaID == schemaID {
			schema = s
			break
		}
	}
	r.mu.RUnlock()
	if schema == nil {
		return nil, fmt.Errorf("schemakit: no cached schema for ID %d", schemaID)
	}
	if schema.parsed == nil {
		return nil, fmt.Errorf("schemakit: schema %q has no parsed schema", schema.Subject)
	}

	var result map[string]any
	if err := avro.Unmarshal(schema.parsed, raw[5:], &result); err != nil {
		return nil, fmt.Errorf("schemakit: deserialize: %w", err)
	}

	return result, nil
}

// Register registers the schema with the Schema Registry via HTTP POST.
// On success, the schema's SchemaID is updated with the ID returned by the registry.
func (r *Registry) Register(ctx context.Context, schema *Schema) error {
	// Build the request body per Schema Registry API
	body := map[string]string{
		"schema":     schema.AvroJSON,
		"schemaType": "AVRO",
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("schemakit: marshal register body: %w", err)
	}

	regURL := fmt.Sprintf("%s/subjects/%s/versions", r.url, url.PathEscape(schema.Subject))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, regURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("schemakit: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("schemakit: register %s: %w", schema.Subject, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("schemakit: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("schemakit: register %s: HTTP %d: %s", schema.Subject, resp.StatusCode, string(respBody))
	}

	var result struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("schemakit: decode register response: %w", err)
	}

	r.mu.Lock()
	schema.SchemaID = result.ID
	r.mu.Unlock()
	return nil
}

// schemaID returns the schema's ID under the registry's read lock to avoid
// data races with concurrent Register calls.
func (r *Registry) schemaID(schema *Schema) int {
	r.mu.RLock()
	id := schema.SchemaID
	r.mu.RUnlock()
	return id
}
