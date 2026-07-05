package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/registry"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func testManifest() Manifest {
	return Manifest{
		Service:         "delphi_api",
		Version:         "11.2.0",
		Profile:         ProfileL2Prod,
		ContractVersion: DefaultContractVersion,
		Capabilities:    []string{"idemkit", "authkit", "problem-json"},
		Idempotency:     &Idempotency{Store: "postgres", Durable: true, TTLSeconds: 604800},
		Endpoints:       []Endpoint{{Name: "http", Kind: "http", URL: "http://delphi-api.local:8080"}},
		OpenAPIPath:     "/openapi.yaml",
	}
}

func TestManifestMatchesPinnedFixtureShape(t *testing.T) {
	fixturePath := filepath.Join("..", "testdata", "windmill", "contracts", "fixtures", "manifest.l2-prod.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture Manifest
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := fixture.Validate(); err != nil {
		t.Fatalf("fixture should validate: %v", err)
	}
	if fixture.ContractVersion != DefaultContractVersion {
		t.Fatalf("contract_version = %q", fixture.ContractVersion)
	}
	if len(fixture.Endpoints) != 1 || fixture.Endpoints[0].Name == "" || fixture.Endpoints[0].URL == "" {
		t.Fatalf("endpoint shape = %#v", fixture.Endpoints)
	}
}

func TestHandlerAndOpenAPIHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(testManifest()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, WellKnownPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d", rec.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, key := range []string{"service", "version", "profile", "contract_version", "capabilities"} {
		if got[key] == nil {
			t.Fatalf("manifest missing %s: %#v", key, got)
		}
	}
	if got["openapi_path"] != "/openapi.yaml" {
		t.Fatalf("openapi_path = %v", got["openapi_path"])
	}

	openapi := httptest.NewRecorder()
	OpenAPIHandler([]byte("openapi: 3.1.0\n"), "application/yaml").ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if openapi.Body.String() != "openapi: 3.1.0\n" {
		t.Fatalf("openapi body = %q", openapi.Body.String())
	}
}

func TestManifestValidateRejectsUnsupportedContractValues(t *testing.T) {
	for name, mutate := range map[string]func(*Manifest){
		"profile":          func(m *Manifest) { m.Profile = "experimental" },
		"contract_version": func(m *Manifest) { m.ContractVersion = "v0.1" },
		"idempotency":      func(m *Manifest) { m.Idempotency.Store = "sqlite" },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := testManifest()
			mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("Validate should reject unsupported contract value")
			}
		})
	}
}

func TestRegisterPersistsRegistryMetadata(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	if err := Register(testManifest()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Init(cancel, "11.3.0-test"); err != nil {
		t.Fatalf("registry.Init: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("done") })
	pidFile := filepath.Join(testSvcDir(t, tmp), strconv.Itoa(os.Getpid())+".json")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	var reg registry.Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if reg.Orchestration == nil || reg.Orchestration.ContractVersion != DefaultContractVersion {
		t.Fatalf("orchestration metadata = %#v", reg.Orchestration)
	}
	if len(reg.Orchestration.Endpoints) != 1 || reg.Orchestration.Endpoints[0].Name != "http" || reg.Orchestration.Endpoints[0].URL == "" {
		t.Fatalf("orchestration endpoints = %#v", reg.Orchestration.Endpoints)
	}
}

func TestRegisterPersistsCLIRegistryMetadata(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	if err := Register(testManifest()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.InitCLI("11.3.0-test"); err != nil {
		t.Fatalf("registry.InitCLI: %v", err)
	}
	t.Cleanup(func() { registry.ShutdownCLI(0) })
	pidFile := filepath.Join(testSvcDir(t, tmp), strconv.Itoa(os.Getpid())+".json")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	var reg registry.Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if reg.Orchestration == nil || reg.Orchestration.Profile != string(ProfileL2Prod) {
		t.Fatalf("cli orchestration metadata = %#v", reg.Orchestration)
	}
}

func testSvcDir(t *testing.T, base string) string {
	t.Helper()
	if name := os.Getenv("CHASSIS_SERVICE_NAME"); name != "" {
		return filepath.Join(base, name)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(base, filepath.Base(wd))
}
