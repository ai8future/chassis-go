package deploy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v9"
	"github.com/ai8future/chassis-go/v9/deploy"
)

func init() { chassis.RequireMajor(9) }

func TestDiscoverNotFound(t *testing.T) {
	t.Setenv("CHASSIS_DEPLOY_DIR", "/tmp/nonexistent-deploy-dir-test")
	t.Setenv("HOME", "/tmp/nonexistent-home-test")

	d := deploy.Discover("test-svc")
	if d.Found() {
		t.Fatal("expected not found")
	}
	if d.Dir() != "" {
		t.Fatalf("expected empty dir, got %q", d.Dir())
	}
}

func TestDiscoverFromEnvVar(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "test-svc")
	os.MkdirAll(svcDir, 0700)

	t.Setenv("CHASSIS_DEPLOY_DIR", svcDir)

	d := deploy.Discover("test-svc")
	if !d.Found() {
		t.Fatal("expected found")
	}
	if d.Dir() != svcDir {
		t.Fatalf("expected %q, got %q", svcDir, d.Dir())
	}
}

func TestLoadEnv(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.env"), []byte("MY_PORT=9090\nMY_HOST=localhost\n"), 0600)
	os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("MY_SECRET=hunter2\n"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	d.LoadEnv()

	if os.Getenv("MY_PORT") != "9090" {
		t.Fatalf("expected MY_PORT=9090, got %q", os.Getenv("MY_PORT"))
	}
	if os.Getenv("MY_HOST") != "localhost" {
		t.Fatalf("expected MY_HOST=localhost, got %q", os.Getenv("MY_HOST"))
	}
	if os.Getenv("MY_SECRET") != "hunter2" {
		t.Fatalf("expected MY_SECRET=hunter2, got %q", os.Getenv("MY_SECRET"))
	}
}

func TestLoadEnvNoOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.env"), []byte("EXISTING=from-file\n"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	t.Setenv("EXISTING", "from-env")

	d := deploy.Discover("test-svc")
	d.LoadEnv()

	if os.Getenv("EXISTING") != "from-env" {
		t.Fatal("LoadEnv should not override existing env vars")
	}
}

func TestLoadEnvSecretsOverrideConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.env"), []byte("DB_PASS=config-val\n"), 0600)
	os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("DB_PASS=secret-val\n"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	d.LoadEnv()

	if os.Getenv("DB_PASS") != "secret-val" {
		t.Fatal("secrets.env should override config.env")
	}
}

func TestTLS(t *testing.T) {
	dir := t.TempDir()
	tlsDir := filepath.Join(dir, "tls")
	os.MkdirAll(tlsDir, 0700)
	os.WriteFile(filepath.Join(tlsDir, "cert.pem"), []byte("cert"), 0600)
	os.WriteFile(filepath.Join(tlsDir, "key.pem"), []byte("key"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	tls, ok := d.TLS()
	if !ok {
		t.Fatal("expected TLS found")
	}
	if tls.Cert != filepath.Join(tlsDir, "cert.pem") {
		t.Fatalf("wrong cert path: %q", tls.Cert)
	}
	if tls.Key != filepath.Join(tlsDir, "key.pem") {
		t.Fatalf("wrong key path: %q", tls.Key)
	}
}

func TestTLSNotPresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	_, ok := d.TLS()
	if ok {
		t.Fatal("expected TLS not found")
	}
}

func TestMeta(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"version":"1.2.3","notes":"prod"}`), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	meta := d.Meta()
	if meta == nil {
		t.Fatal("expected meta")
	}
	if meta.Version != "1.2.3" {
		t.Fatalf("expected version 1.2.3, got %q", meta.Version)
	}
	if meta.Notes != "prod" {
		t.Fatalf("expected notes=prod, got %q", meta.Notes)
	}
}

func TestPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	got := d.Path("tls/cert.pem")
	expected := filepath.Join(dir, "tls/cert.pem")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFlagSource(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "flags.json"), []byte(`{"new-ui":"true","beta":"false"}`), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	src := d.FlagSource()

	val, ok := src.Lookup("new-ui")
	if !ok || val != "true" {
		t.Fatalf("expected new-ui=true, got (%q, %v)", val, ok)
	}
	_, ok = src.Lookup("missing")
	if ok {
		t.Fatal("expected miss for unknown flag")
	}
}

func TestEnvComments(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.env"), []byte("# comment\nKEY=val\n\n  \n"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	d.LoadEnv()

	if os.Getenv("KEY") != "val" {
		t.Fatalf("expected KEY=val, got %q", os.Getenv("KEY"))
	}
}

func TestEnvQuoteStripping(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.env"), []byte("DQ=\"double quoted\"\nSQ='single quoted'\nNQ=no quotes\n"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)

	d := deploy.Discover("test-svc")
	d.LoadEnv()

	if os.Getenv("DQ") != "double quoted" {
		t.Fatalf("expected stripped double quotes, got %q", os.Getenv("DQ"))
	}
	if os.Getenv("SQ") != "single quoted" {
		t.Fatalf("expected stripped single quotes, got %q", os.Getenv("SQ"))
	}
	if os.Getenv("NQ") != "no quotes" {
		t.Fatalf("expected unquoted value, got %q", os.Getenv("NQ"))
	}
}

func TestLoadEnvNoOverrideEmpty(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.env"), []byte("EMPTY_VAR=from-file\n"), 0600)

	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	t.Setenv("EMPTY_VAR", "")

	d := deploy.Discover("test-svc")
	d.LoadEnv()

	if os.Getenv("EMPTY_VAR") != "" {
		t.Fatal("LoadEnv should not override env var set to empty string")
	}
}

func TestSpec(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0","version":"1.0.0"}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	if d.Spec() != "9.0" {
		t.Fatalf("expected spec 9.0, got %s", d.Spec())
	}
}

func TestSpecDefaultV8(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"version":"1.0.0"}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	if d.Spec() != "8.0" {
		t.Fatalf("expected spec 8.0, got %s", d.Spec())
	}
}

func TestSpecNoDeployJson(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	if d.Spec() != "" {
		t.Fatalf("expected empty spec, got %s", d.Spec())
	}
}

func TestDiscoverName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("my-service")
	if d.Name() != "my-service" {
		t.Fatalf("expected name my-service, got %s", d.Name())
	}
}

func TestDiscoverNameNotFound(t *testing.T) {
	t.Setenv("CHASSIS_DEPLOY_DIR", "/tmp/nonexistent-deploy-dir-test")
	t.Setenv("HOME", "/tmp/nonexistent-home-test")
	d := deploy.Discover("my-service")
	if d.Name() != "my-service" {
		t.Fatalf("expected name my-service even when not found, got %s", d.Name())
	}
}

func TestEnvironmentFromDeployJSON(t *testing.T) {
	dir := t.TempDir()
	meta := `{"chassis":"9.0","environment":{"env":"prod","provider":"aws","region":"us-east-1","cluster":"main"}}`
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(meta), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	env := d.Environment()
	if env.Env != "prod" {
		t.Fatalf("expected prod, got %s", env.Env)
	}
	if env.Provider != "aws" {
		t.Fatalf("expected aws, got %s", env.Provider)
	}
	if env.Region != "us-east-1" {
		t.Fatalf("expected us-east-1, got %s", env.Region)
	}
	if env.Cluster != "main" {
		t.Fatalf("expected main, got %s", env.Cluster)
	}
	if env.Service != "test-svc" {
		t.Fatalf("expected test-svc, got %s", env.Service)
	}
	if env.Hostname == "" {
		t.Fatal("expected hostname")
	}
}

func TestEnvironmentEnvVarOverride(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0","environment":{"env":"staging"}}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	t.Setenv("CHASSIS_ENV", "prod")
	d := deploy.Discover("test-svc")
	env := d.Environment()
	if env.Env != "prod" {
		t.Fatalf("expected prod override, got %s", env.Env)
	}
}

func TestEnvironmentK8sDetection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0"}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	d := deploy.Discover("test-svc")
	env := d.Environment()
	if env.Runtime != "kubernetes" {
		t.Fatalf("expected kubernetes, got %s", env.Runtime)
	}
}

func TestEnvironmentRuntimeDetection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0"}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	env := d.Environment()
	valid := map[string]bool{"kubernetes": true, "container": true, "vm": true, "bare-metal": true}
	if !valid[env.Runtime] {
		t.Fatalf("unexpected runtime: %s", env.Runtime)
	}
}

func TestEnvironmentNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	env := d.Environment()
	if env.Service != "test-svc" {
		t.Fatalf("expected test-svc, got %s", env.Service)
	}
	if env.Hostname == "" {
		t.Fatal("expected hostname")
	}
}

// --- Endpoints tests ---

func TestEndpoints(t *testing.T) {
	dir := t.TempDir()
	meta := `{"chassis":"9.0","endpoints":{"api":{"port":50051,"protocol":"grpc"},"metrics":{"port":9090,"protocol":"http","path":"/metrics"},"health":{"port":8080,"protocol":"http","path":"/health"}}}`
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(meta), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	eps := d.Endpoints()
	if len(eps) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(eps))
	}
	api := eps["api"]
	if api.Port != 50051 {
		t.Fatalf("expected 50051, got %d", api.Port)
	}
	if api.Protocol != "grpc" {
		t.Fatalf("expected grpc, got %s", api.Protocol)
	}
}

func TestEndpointLookup(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0","endpoints":{"api":{"port":8080,"protocol":"http"}}}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	ep, ok := d.Endpoint("api")
	if !ok {
		t.Fatal("expected found")
	}
	if ep.Port != 8080 {
		t.Fatalf("expected 8080, got %d", ep.Port)
	}
	_, ok = d.Endpoint("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestEndpointsDefaultProtocol(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0","endpoints":{"web":{"port":3000}}}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	ep, _ := d.Endpoint("web")
	if ep.Protocol != "http" {
		t.Fatalf("expected http default, got %s", ep.Protocol)
	}
}

func TestEndpointsNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	if len(d.Endpoints()) != 0 {
		t.Fatal("expected empty")
	}
}

// --- Dependencies tests ---

func TestDependencies(t *testing.T) {
	dir := t.TempDir()
	meta := `{"chassis":"9.0","dependencies":[{"service":"airborne","endpoint":"api","protocol":"grpc","port":50051,"required":true},{"service":"redis","protocol":"tcp","port":6379,"required":false}]}`
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(meta), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	deps := d.Dependencies()
	if len(deps) != 2 {
		t.Fatalf("expected 2, got %d", len(deps))
	}
	if deps[0].Service != "airborne" {
		t.Fatalf("expected airborne, got %s", deps[0].Service)
	}
	if deps[0].Protocol != "grpc" {
		t.Fatalf("expected grpc, got %s", deps[0].Protocol)
	}
	if deps[0].Port != 50051 {
		t.Fatalf("expected 50051, got %d", deps[0].Port)
	}
	if deps[0].Required == nil || !*deps[0].Required {
		t.Fatal("expected required true")
	}
	if deps[1].Required == nil || *deps[1].Required {
		t.Fatal("expected required false")
	}
}

func TestDependenciesDefaultRequired(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{"chassis":"9.0","dependencies":[{"service":"db"}]}`), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	deps := d.Dependencies()
	if len(deps) != 1 {
		t.Fatalf("expected 1, got %d", len(deps))
	}
	if deps[0].Required == nil || !*deps[0].Required {
		t.Fatal("expected default required=true")
	}
}

func TestDependenciesEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	if len(d.Dependencies()) != 0 {
		t.Fatal("expected empty")
	}
}

// --- Health tests ---

func TestHealth(t *testing.T) {
	dir := t.TempDir()
	meta := `{"chassis":"9.0","version":"2.1.0","environment":{"env":"prod"},"endpoints":{"api":{"port":8080,"protocol":"http"}}}`
	os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(meta), 0600)
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	time.Sleep(5 * time.Millisecond)
	status := d.Health(map[string]string{"db": "ok", "cache": "degraded"})
	if status.Service != "test-svc" {
		t.Fatalf("expected test-svc, got %s", status.Service)
	}
	if status.Version != "2.1.0" {
		t.Fatalf("expected 2.1.0, got %s", status.Version)
	}
	if status.ChassisSpec != "9.0" {
		t.Fatalf("expected 9.0, got %s", status.ChassisSpec)
	}
	if status.Environment != "prod" {
		t.Fatalf("expected prod, got %s", status.Environment)
	}
	if status.Uptime <= 0 {
		t.Fatal("expected positive uptime")
	}
	if status.Components["db"] != "ok" {
		t.Fatal("expected db ok")
	}
	if len(status.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(status.Endpoints))
	}
}

func TestHealthNoDeployJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHASSIS_DEPLOY_DIR", dir)
	d := deploy.Discover("test-svc")
	status := d.Health(nil)
	if status.Service != "test-svc" {
		t.Fatalf("expected test-svc, got %s", status.Service)
	}
	if status.Uptime < 0 {
		t.Fatal("expected non-negative uptime")
	}
}
