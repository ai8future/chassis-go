// Package deploy provides convention-based deploy directory discovery.
package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v9"
)

type Deploy struct {
	dir     string
	found   bool
	name    string
	created time.Time
}

type TLSPaths struct {
	Cert string
	Key  string
	CA   string
}

type DeployMeta struct {
	Version    string         `json:"version"`
	MinVersion string         `json:"min_version"`
	Notes      string         `json:"notes"`
	Resources  map[string]any `json:"resources"`
}

type FlagLookup struct {
	flags map[string]string
}

func (f *FlagLookup) Lookup(name string) (string, bool) {
	v, ok := f.flags[name]
	return v, ok
}

func Discover(name string) *Deploy {
	chassis.AssertVersionChecked()

	now := time.Now()

	if dir := os.Getenv("CHASSIS_DEPLOY_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return &Deploy{dir: dir, found: true, name: name, created: now}
		}
	}

	dir := filepath.Join("/app/deploy", name)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return &Deploy{dir: dir, found: true, name: name, created: now}
	}

	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, "deploy", name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return &Deploy{dir: dir, found: true, name: name, created: now}
		}
	}

	dir = filepath.Join("/deploy", name)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return &Deploy{dir: dir, found: true, name: name, created: now}
	}

	return &Deploy{name: name, created: now}
}

// Environment holds auto-detected and operator-declared runtime info.
type Environment struct {
	Runtime   string `json:"runtime"`
	Hostname  string `json:"hostname"`
	Service   string `json:"service"`
	Env       string `json:"env,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Region    string `json:"region,omitempty"`
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	PodName   string `json:"pod_name,omitempty"`
}

func (d *Deploy) Found() bool { return d.found }
func (d *Deploy) Dir() string  { return d.dir }
func (d *Deploy) Name() string { return d.name }

func (d *Deploy) Spec() string {
	if !d.found {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(d.dir, "deploy.json"))
	if err != nil {
		return ""
	}
	var raw struct {
		Chassis string `json:"chassis"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}
	if raw.Chassis == "" {
		return "8.0"
	}
	return raw.Chassis
}

func (d *Deploy) Path(rel string) string {
	if !d.found {
		return ""
	}
	return filepath.Join(d.dir, rel)
}

func (d *Deploy) LoadEnv() {
	if !d.found {
		return
	}

	configVars := parseEnvFile(filepath.Join(d.dir, "config.env"))
	secretVars := parseEnvFile(filepath.Join(d.dir, "secrets.env"))

	merged := make(map[string]string)
	for k, v := range configVars {
		merged[k] = v
	}
	for k, v := range secretVars {
		merged[k] = v
	}

	for k, v := range merged {
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func (d *Deploy) TLS() (TLSPaths, bool) {
	if !d.found {
		return TLSPaths{}, false
	}
	certPath := filepath.Join(d.dir, "tls", "cert.pem")
	keyPath := filepath.Join(d.dir, "tls", "key.pem")

	if _, err := os.Stat(certPath); err != nil {
		return TLSPaths{}, false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return TLSPaths{}, false
	}

	tls := TLSPaths{Cert: certPath, Key: keyPath}
	caPath := filepath.Join(d.dir, "tls", "ca.pem")
	if _, err := os.Stat(caPath); err == nil {
		tls.CA = caPath
	}
	return tls, true
}

func (d *Deploy) Meta() *DeployMeta {
	if !d.found {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(d.dir, "deploy.json"))
	if err != nil {
		return nil
	}
	var meta DeployMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

// Endpoint describes a named network endpoint for a service.
type Endpoint struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Path     string `json:"path,omitempty"`
}

// Dependency describes a service dependency.
type Dependency struct {
	Service  string `json:"service"`
	Endpoint string `json:"endpoint,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Port     int    `json:"port,omitempty"`
	Required *bool  `json:"required,omitempty"`
}

// HealthStatus is the standard health payload for a service.
type HealthStatus struct {
	Service     string              `json:"service"`
	Version     string              `json:"version,omitempty"`
	ChassisSpec string              `json:"chassis_spec,omitempty"`
	Runtime     string              `json:"runtime"`
	Uptime      float64             `json:"uptime"`
	Environment string              `json:"environment,omitempty"`
	Endpoints   map[string]Endpoint `json:"endpoints,omitempty"`
	Components  map[string]string   `json:"components,omitempty"`
}

// Endpoints returns all named endpoints from deploy.json.
// Protocol defaults to "http" when omitted.
func (d *Deploy) Endpoints() map[string]Endpoint {
	if !d.found {
		return map[string]Endpoint{}
	}
	data, err := os.ReadFile(filepath.Join(d.dir, "deploy.json"))
	if err != nil {
		return map[string]Endpoint{}
	}
	var raw struct {
		Endpoints map[string]Endpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]Endpoint{}
	}
	if raw.Endpoints == nil {
		return map[string]Endpoint{}
	}
	for name, ep := range raw.Endpoints {
		if ep.Protocol == "" {
			ep.Protocol = "http"
			raw.Endpoints[name] = ep
		}
	}
	return raw.Endpoints
}

// Endpoint returns a single named endpoint and whether it was found.
func (d *Deploy) Endpoint(name string) (Endpoint, bool) {
	eps := d.Endpoints()
	ep, ok := eps[name]
	return ep, ok
}

// Dependencies returns the list of service dependencies from deploy.json.
// Required defaults to true when omitted.
func (d *Deploy) Dependencies() []Dependency {
	if !d.found {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(d.dir, "deploy.json"))
	if err != nil {
		return nil
	}
	var raw struct {
		Dependencies []Dependency `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if raw.Dependencies == nil {
		return nil
	}
	for i := range raw.Dependencies {
		if raw.Dependencies[i].Required == nil {
			v := true
			raw.Dependencies[i].Required = &v
		}
	}
	return raw.Dependencies
}

// Health returns a standard health status payload for the service.
func (d *Deploy) Health(components map[string]string) HealthStatus {
	status := HealthStatus{
		Service:    d.name,
		Runtime:    d.Environment().Runtime,
		Uptime:     time.Since(d.created).Seconds(),
		Endpoints:  d.Endpoints(),
		Components: components,
	}

	if meta := d.Meta(); meta != nil {
		status.Version = meta.Version
	}
	status.ChassisSpec = d.Spec()
	status.Environment = d.Environment().Env

	// Clean up empty maps/nil for consistent output
	if len(status.Endpoints) == 0 {
		status.Endpoints = nil
	}

	return status
}

func (d *Deploy) FlagSource() *FlagLookup {
	flags := make(map[string]string)
	if !d.found {
		return &FlagLookup{flags: flags}
	}
	data, err := os.ReadFile(filepath.Join(d.dir, "flags.json"))
	if err != nil {
		return &FlagLookup{flags: flags}
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return &FlagLookup{flags: flags}
	}
	return &FlagLookup{flags: raw}
}

func (d *Deploy) RunHook(name string) {
	if !d.found {
		return
	}
	hooksDir := filepath.Join(d.dir, "hooks")
	hookPath := filepath.Join(hooksDir, name)
	if !strings.HasPrefix(hookPath, hooksDir+string(os.PathSeparator)) {
		return // path traversal attempt
	}
	if _, err := os.Stat(hookPath); err != nil {
		return
	}
	runHookExec(hookPath)
}

func parseEnvFile(path string) map[string]string {
	result := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		// Strip surrounding quotes (single or double).
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		result[strings.TrimSpace(k)] = v
	}
	return result
}

func runHookExec(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = filepath.Dir(path)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		_ = out
	}
	if err != nil {
		_ = err
	}
}

// detectRuntime returns the detected runtime environment.
// Detection order (first match wins):
// 1. KUBERNETES_SERVICE_HOST env var → "kubernetes"
// 2. /.dockerenv exists → "container"
// 3. /proc/1/cgroup contains docker/containerd/podman → "container"
// 4. /sys/class/dmi/id/product_name contains vm indicators → "vm"
// 5. /sys/hypervisor/type exists → "vm"
// 6. fallback → "bare-metal"
func detectRuntime() string {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "kubernetes"
	}

	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "container"
	}

	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := strings.ToLower(string(data))
		if strings.Contains(content, "docker") ||
			strings.Contains(content, "containerd") ||
			strings.Contains(content, "podman") {
			return "container"
		}
	}

	if data, err := os.ReadFile("/sys/class/dmi/id/product_name"); err == nil {
		product := strings.ToLower(strings.TrimSpace(string(data)))
		vmIndicators := []string{"virtualbox", "vmware", "qemu", "kvm", "xen", "hyper-v"}
		for _, indicator := range vmIndicators {
			if strings.Contains(product, indicator) {
				return "vm"
			}
		}
	}

	if _, err := os.Stat("/sys/hypervisor/type"); err == nil {
		return "vm"
	}

	return "bare-metal"
}

// Environment returns auto-detected and operator-declared runtime info.
// It reads the "environment" block from deploy.json (if present), then
// applies env var overrides (CHASSIS_ENV, CHASSIS_PROVIDER, CHASSIS_REGION,
// CHASSIS_CLUSTER). For Kubernetes, it also reads namespace and pod name.
func (d *Deploy) Environment() Environment {
	env := Environment{
		Runtime: detectRuntime(),
		Service: d.name,
	}

	if h, err := os.Hostname(); err == nil {
		env.Hostname = h
	}

	// Read environment block from deploy.json if deploy dir was found.
	if d.found {
		data, err := os.ReadFile(filepath.Join(d.dir, "deploy.json"))
		if err == nil {
			var raw struct {
				Environment struct {
					Env      string `json:"env"`
					Provider string `json:"provider"`
					Region   string `json:"region"`
					Cluster  string `json:"cluster"`
				} `json:"environment"`
			}
			if json.Unmarshal(data, &raw) == nil {
				env.Env = raw.Environment.Env
				env.Provider = raw.Environment.Provider
				env.Region = raw.Environment.Region
				env.Cluster = raw.Environment.Cluster
			}
		}
	}

	// Env var overrides (highest priority).
	if v := os.Getenv("CHASSIS_ENV"); v != "" {
		env.Env = v
	}
	if v := os.Getenv("CHASSIS_PROVIDER"); v != "" {
		env.Provider = v
	}
	if v := os.Getenv("CHASSIS_REGION"); v != "" {
		env.Region = v
	}
	if v := os.Getenv("CHASSIS_CLUSTER"); v != "" {
		env.Cluster = v
	}

	// Kubernetes-specific fields.
	if env.Runtime == "kubernetes" {
		if nsData, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			env.Namespace = strings.TrimSpace(string(nsData))
		}
		if podName := os.Getenv("HOSTNAME"); podName != "" {
			env.PodName = podName
		}
	}

	return env
}
