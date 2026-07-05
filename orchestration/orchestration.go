// Package orchestration exposes Windmill-compatible service capability manifests.
package orchestration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/registry"
)

const (
	DefaultContractVersion = "0.1"
	WellKnownPath          = "/.well-known/chassis-capabilities.json"
)

// Profile is a Windmill orchestration readiness profile.
type Profile string

const (
	ProfileL0      Profile = "L0"
	ProfileL1      Profile = "L1"
	ProfileL2Local Profile = "L2-local"
	ProfileL2Prod  Profile = "L2-prod"
	ProfileL3      Profile = "L3"
	ProfileC1      Profile = "C1"
	ProfileD1      Profile = "D1"
)

// Manifest is the contract-compatible capability manifest shape.
type Manifest struct {
	Service         string          `json:"service"`
	Version         string          `json:"version"`
	Profile         Profile         `json:"profile"`
	ContractVersion string          `json:"contract_version"`
	Capabilities    []string        `json:"capabilities"`
	Idempotency     *Idempotency    `json:"idempotency,omitempty"`
	Endpoints       []Endpoint      `json:"endpoints,omitempty"`
	OpenAPIPath     string          `json:"openapi_path,omitempty"`
	DaemonCommands  []DaemonCommand `json:"daemon_commands,omitempty"`
}

// Idempotency declares the idempotency store used by the service.
type Idempotency struct {
	Store      string `json:"store"`
	Durable    bool   `json:"durable"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// Endpoint is a named callable URL.
type Endpoint struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind,omitempty"`
}

// DaemonCommand declares a registry command that Windmill may invoke.
type DaemonCommand struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	ArgsSchema  map[string]any `json:"args_schema,omitempty"`
}

// Validate checks the manifest fields required by the shared contract.
func (m Manifest) Validate() error {
	chassis.AssertVersionChecked()
	if m.Service == "" {
		return fmt.Errorf("orchestration: service is required")
	}
	if m.Version == "" {
		return fmt.Errorf("orchestration: version is required")
	}
	if m.Profile == "" {
		return fmt.Errorf("orchestration: profile is required")
	}
	if !validProfile(m.Profile) {
		return fmt.Errorf("orchestration: unsupported profile %q", m.Profile)
	}
	if m.ContractVersion == "" {
		return fmt.Errorf("orchestration: contract_version is required")
	}
	if !validContractVersion(m.ContractVersion) {
		return fmt.Errorf("orchestration: invalid contract_version %q", m.ContractVersion)
	}
	if len(m.Capabilities) == 0 {
		return fmt.Errorf("orchestration: at least one capability is required")
	}
	seen := map[string]bool{}
	for _, capability := range m.Capabilities {
		if capability == "" {
			return fmt.Errorf("orchestration: empty capability")
		}
		if seen[capability] {
			return fmt.Errorf("orchestration: duplicate capability %q", capability)
		}
		seen[capability] = true
	}
	for _, endpoint := range m.Endpoints {
		if endpoint.Name == "" || endpoint.URL == "" {
			return fmt.Errorf("orchestration: endpoint name and url are required")
		}
	}
	if m.Idempotency != nil {
		if !validIdempotencyStore(m.Idempotency.Store) {
			return fmt.Errorf("orchestration: unsupported idempotency store %q", m.Idempotency.Store)
		}
		if m.Idempotency.TTLSeconds < 0 {
			return fmt.Errorf("orchestration: idempotency ttl_seconds must be non-negative")
		}
	}
	return nil
}

// Normalize fills default contract values and de-duplicates/sorts capabilities.
func (m Manifest) Normalize() Manifest {
	chassis.AssertVersionChecked()
	if m.ContractVersion == "" {
		m.ContractVersion = DefaultContractVersion
	}
	seen := map[string]bool{}
	caps := make([]string, 0, len(m.Capabilities))
	for _, cap := range m.Capabilities {
		if cap != "" && !seen[cap] {
			seen[cap] = true
			caps = append(caps, cap)
		}
	}
	sort.Strings(caps)
	m.Capabilities = caps
	return m
}

// Handler serves the manifest as application/json.
func Handler(manifest Manifest) http.Handler {
	chassis.AssertVersionChecked()
	manifest = manifest.Normalize()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteManifest(w, r, manifest)
	})
}

// WriteManifest writes a manifest response.
func WriteManifest(w http.ResponseWriter, r *http.Request, manifest Manifest) {
	chassis.AssertVersionChecked()
	_ = r
	manifest = manifest.Normalize()
	if err := manifest.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(manifest)
}

// OpenAPIHandler serves an authored OpenAPI document. This package deliberately
// does not generate schemas by reflection.
func OpenAPIHandler(document []byte, contentType string) http.Handler {
	chassis.AssertVersionChecked()
	if contentType == "" {
		contentType = "application/yaml"
	}
	body := append([]byte(nil), document...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	})
}

// RegistryInfo converts a manifest into optional registry metadata.
func RegistryInfo(manifest Manifest) *registry.OrchestrationInfo {
	chassis.AssertVersionChecked()
	manifest = manifest.Normalize()
	info := &registry.OrchestrationInfo{
		Profile:         string(manifest.Profile),
		ContractVersion: manifest.ContractVersion,
		Capabilities:    append([]string(nil), manifest.Capabilities...),
		OpenAPIPath:     manifest.OpenAPIPath,
	}
	if manifest.Idempotency != nil {
		info.Idempotency = &registry.IdempotencyInfo{Store: manifest.Idempotency.Store, Durable: manifest.Idempotency.Durable, TTLSeconds: manifest.Idempotency.TTLSeconds}
	}
	for _, endpoint := range manifest.Endpoints {
		info.Endpoints = append(info.Endpoints, registry.OrchestrationEndpoint{Name: endpoint.Name, URL: endpoint.URL, Kind: endpoint.Kind})
	}
	return info
}

// Register stores manifest metadata for future registry.Init calls.
func Register(manifest Manifest) error {
	chassis.AssertVersionChecked()
	manifest = manifest.Normalize()
	if err := manifest.Validate(); err != nil {
		return err
	}
	registry.SetOrchestration(RegistryInfo(manifest))
	return nil
}

func validProfile(profile Profile) bool {
	switch profile {
	case ProfileL0, ProfileL1, ProfileL2Local, ProfileL2Prod, ProfileL3, ProfileC1, ProfileD1:
		return true
	default:
		return false
	}
}

func validContractVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	return err == nil && minor >= 0
}

func validIdempotencyStore(store string) bool {
	switch store {
	case "none", "memory", "postgres", "redis":
		return true
	default:
		return false
	}
}
