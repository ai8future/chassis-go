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

	chassis "github.com/ai8future/chassis-go/v8"
)

type Deploy struct {
	dir   string
	found bool
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

	if dir := os.Getenv("CHASSIS_DEPLOY_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return &Deploy{dir: dir, found: true}
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, "deploy", name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return &Deploy{dir: dir, found: true}
		}
	}

	dir := filepath.Join("/deploy", name)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return &Deploy{dir: dir, found: true}
	}

	return &Deploy{}
}

func (d *Deploy) Found() bool { return d.found }
func (d *Deploy) Dir() string { return d.dir }

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
		if os.Getenv(k) == "" {
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
	hookPath := filepath.Join(d.dir, "hooks", name)
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
		result[strings.TrimSpace(k)] = strings.TrimSpace(v)
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
