// Package conformance provides small Windmill readiness probes and report
// aggregation for chassis services.
//
// It intentionally checks L0-L2 runtime evidence plus L3 declaration evidence
// only. Durable outbox and domain-event behavior remain service/addon concerns.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/orchestration"
)

const (
	ContractVersion = orchestration.DefaultContractVersion
)

// Level is a cumulative Windmill readiness target.
type Level string

const (
	LevelL0 Level = "L0"
	LevelL1 Level = "L1"
	LevelL2 Level = "L2"
	LevelL3 Level = "L3"
)

// Requirement names mirror testdata/windmill/contracts/conformance.yaml.
const (
	ReqAcceptsXTraceID                     = "accepts_x_trace_id"
	ReqEmitsProblemJSONErrors              = "emits_problem_json_errors"
	ReqScopedBearerAuthOnMutatingRoutes    = "scoped_bearer_auth_on_mutating_routes"
	ReqErrorProblemJSONRetryClass          = "error_problem_json_retry_class"
	ReqCapabilityManifest                  = "capability_manifest"
	ReqIdempotencyKeyReplayForMutating     = "idempotency_key_replay_for_mutating_routes"
	ReqIdempotencyStoreDeclared            = "idempotency_store_declared"
	ReqIdempotencyKeyTenantScoped          = "idempotency_key_tenant_scoped"
	ReqOutboxOrEquivalentDeclared          = "chassis_outbox_or_equivalent_declared"
	ReqDomainEventsTraceAndEntityRefs      = "domain_events_preserve_trace_and_entity_refs"
	ReqDurableStoreRequiredForL2Production = "durable_store_required_for_l2_prod"
)

// Evidence is caller-supplied proof collected from runtime probes, fixtures, or
// service metadata. Check interprets Manifest when present, so callers do not
// need to set CapabilityManifest or IdempotencyStoreDeclared separately.
type Evidence struct {
	AcceptsXTraceID                  bool
	EmitsProblemJSONErrors           bool
	ErrorProblemJSONRetryClass       bool
	ScopedBearerAuthOnMutatingRoutes bool
	CapabilityManifest               bool
	Manifest                         *orchestration.Manifest
	IdempotencyKeyReplayForMutating  bool
	IdempotencyStoreDeclared         bool
	IdempotencyKeyTenantScoped       bool
	OutboxOrEquivalentDeclared       bool
	DomainEventsTraceAndEntityRefs   bool
}

// Report is the result of evaluating evidence against a target level.
type Report struct {
	Level                   Level    `json:"level"`
	Passed                  bool     `json:"passed"`
	Checked                 []string `json:"checked"`
	Missing                 []string `json:"missing,omitempty"`
	Warnings                []string `json:"warnings,omitempty"`
	L3DeclarationOnly       bool     `json:"l3_declaration_only,omitempty"`
	ManifestContractVersion string   `json:"manifest_contract_version,omitempty"`
	ManifestProfile         string   `json:"manifest_profile,omitempty"`
}

// TestingT is the minimal testing.TB surface used by Require.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// ProbeResult is returned by individual probe helpers.
type ProbeResult struct {
	Passed  bool           `json:"passed"`
	Reason  string         `json:"reason,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// ProblemObservation captures an HTTP problem response for validation.
type ProblemObservation struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// IdempotencyReplayObservation captures first/replay responses for the same
// tenant and idempotency key.
type IdempotencyReplayObservation struct {
	FirstStatus  int
	ReplayStatus int
	FirstBody    []byte
	ReplayBody   []byte
	ReplayHeader http.Header
}

// TenantIsolationObservation captures same-key responses across two tenants.
type TenantIsolationObservation struct {
	TenantAStatus          int
	TenantBStatus          int
	TenantABody            []byte
	TenantBBody            []byte
	TenantBReplayHeader    http.Header
	TenantBHandlerExecuted bool
}

var (
	canonicalTraceID = regexp.MustCompile(`^tr_[0-9a-f]{32}$`)
	legacyTraceID    = regexp.MustCompile(`^tr_[0-9a-f]{12}$`)
)

// RequirementsFor returns cumulative requirement names for the target level.
func RequirementsFor(level Level) []string {
	chassis.AssertVersionChecked()
	requirements := []string{ReqAcceptsXTraceID, ReqEmitsProblemJSONErrors}
	if levelAtLeast(level, LevelL1) {
		requirements = append(requirements,
			ReqScopedBearerAuthOnMutatingRoutes,
			ReqErrorProblemJSONRetryClass,
			ReqCapabilityManifest,
		)
	}
	if levelAtLeast(level, LevelL2) {
		requirements = append(requirements,
			ReqIdempotencyKeyReplayForMutating,
			ReqIdempotencyStoreDeclared,
			ReqIdempotencyKeyTenantScoped,
		)
	}
	if levelAtLeast(level, LevelL3) {
		requirements = append(requirements,
			ReqOutboxOrEquivalentDeclared,
			ReqDomainEventsTraceAndEntityRefs,
		)
	}
	return append([]string(nil), requirements...)
}

// LevelForProfile maps an orchestration manifest profile to the closest
// cumulative conformance level. CLI/daemon profiles are not HTTP readiness
// levels and therefore return an empty Level.
func LevelForProfile(profile orchestration.Profile) Level {
	chassis.AssertVersionChecked()
	switch profile {
	case orchestration.ProfileL0:
		return LevelL0
	case orchestration.ProfileL1:
		return LevelL1
	case orchestration.ProfileL2Local, orchestration.ProfileL2Prod:
		return LevelL2
	case orchestration.ProfileL3:
		return LevelL3
	default:
		return ""
	}
}

// Check evaluates evidence against a cumulative level.
func Check(level Level, evidence Evidence) Report {
	chassis.AssertVersionChecked()
	report := Report{Level: level, Checked: RequirementsFor(level)}
	if evidence.Manifest != nil {
		normalized := evidence.Manifest.Normalize()
		report.ManifestContractVersion = normalized.ContractVersion
		report.ManifestProfile = string(normalized.Profile)
	}
	for _, req := range report.Checked {
		if !evidence.satisfies(req) {
			report.Missing = append(report.Missing, req)
		}
	}
	if needsProductionDurability(evidence.Manifest) {
		report.Checked = append(report.Checked, ReqDurableStoreRequiredForL2Production)
		if !manifestDeclaresDurableStore(evidence.Manifest) {
			report.Missing = append(report.Missing, ReqDurableStoreRequiredForL2Production)
		}
	}
	if levelAtLeast(level, LevelL3) {
		report.L3DeclarationOnly = true
		report.Warnings = append(report.Warnings, "L3 checks are declaration-only in chassis core; verify durable outbox and event semantics in the owning service/addon tests")
	}
	report.Passed = len(report.Missing) == 0
	return report
}

// Require fails t when Check does not pass.
func Require(t TestingT, level Level, evidence Evidence) Report {
	chassis.AssertVersionChecked()
	t.Helper()
	report := Check(level, evidence)
	if !report.Passed {
		t.Fatalf("windmill conformance %s failed: missing %s", level, strings.Join(report.Missing, ", "))
	}
	return report
}

// ValidCanonicalTraceID reports whether traceID matches the generated canonical
// X-Trace-ID format tr_[0-9a-f]{32}.
func ValidCanonicalTraceID(traceID string) bool {
	chassis.AssertVersionChecked()
	return canonicalTraceID.MatchString(traceID)
}

// AcceptsTraceID reports whether traceID is acceptable at ingress. It allows
// the bounded legacy tr_[0-9a-f]{12} migration form but new IDs must be
// canonical.
func AcceptsTraceID(traceID string) bool {
	chassis.AssertVersionChecked()
	return canonicalTraceID.MatchString(traceID) || legacyTraceID.MatchString(traceID)
}

// ProbeProblemJSON validates the Windmill problem+json shape used for L0/L1.
func ProbeProblemJSON(obs ProblemObservation) ProbeResult {
	chassis.AssertVersionChecked()
	if obs.StatusCode < 400 || obs.StatusCode > 599 {
		return fail("status is not an HTTP error", "status", obs.StatusCode)
	}
	ct := obs.Header.Get("Content-Type")
	if ct == "" {
		return fail("content type is missing")
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil || mediaType != "application/problem+json" {
		return fail("content type is not application/problem+json", "content_type", ct)
	}
	var problem map[string]any
	if err := json.Unmarshal(obs.Body, &problem); err != nil {
		return fail("body is not JSON", "error", err.Error())
	}
	for _, key := range []string{"type", "title", "status", "code"} {
		if problem[key] == nil || problem[key] == "" {
			return fail("problem is missing required field", "field", key)
		}
	}
	if intFromJSON(problem["status"]) != obs.StatusCode {
		return fail("problem status does not match response status", "problem_status", problem["status"], "status", obs.StatusCode)
	}
	if _, ok := problem["retryable"].(bool); !ok {
		return fail("problem is missing boolean retryable classification")
	}
	if retryAfter, ok := problem["retry_after"]; ok && intFromJSON(retryAfter) < 0 {
		return fail("retry_after must be non-negative", "retry_after", retryAfter)
	}
	if traceID, ok := problem["trace_id"].(string); ok && !AcceptsTraceID(traceID) {
		return fail("trace_id has invalid format", "trace_id", traceID)
	}
	return pass("problem JSON is valid")
}

// ProbeManifest validates a capability manifest response body.
func ProbeManifest(body []byte) (orchestration.Manifest, ProbeResult) {
	chassis.AssertVersionChecked()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return orchestration.Manifest{}, fail("manifest is not JSON", "error", err.Error())
	}
	allowed := map[string]bool{
		"service": true, "version": true, "profile": true, "contract_version": true,
		"capabilities": true, "idempotency": true, "endpoints": true, "openapi_path": true,
		"daemon_commands": true,
	}
	for key := range raw {
		if !allowed[key] {
			return orchestration.Manifest{}, fail("manifest has unknown top-level field", "field", key)
		}
	}
	var manifest orchestration.Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return orchestration.Manifest{}, fail("manifest cannot be decoded", "error", err.Error())
	}
	manifest = manifest.Normalize()
	if err := manifest.Validate(); err != nil {
		return manifest, fail("manifest is invalid", "error", err.Error())
	}
	if manifest.ContractVersion != ContractVersion {
		return manifest, fail("unsupported manifest contract_version", "contract_version", manifest.ContractVersion)
	}
	return manifest, pass("manifest is valid")
}

// ProbeIdempotencyReplay validates same-tenant idempotency replay behavior.
func ProbeIdempotencyReplay(obs IdempotencyReplayObservation) ProbeResult {
	chassis.AssertVersionChecked()
	if obs.FirstStatus == 0 || obs.ReplayStatus == 0 {
		return fail("missing response status")
	}
	if obs.FirstStatus != obs.ReplayStatus {
		return fail("replay status differs from first status", "first_status", obs.FirstStatus, "replay_status", obs.ReplayStatus)
	}
	if !bytes.Equal(obs.FirstBody, obs.ReplayBody) {
		return fail("replay body differs from first body")
	}
	if strings.ToLower(obs.ReplayHeader.Get("Idempotency-Replayed")) != "true" {
		return fail("replay header is missing", "header", obs.ReplayHeader.Get("Idempotency-Replayed"))
	}
	return pass("idempotency replay is valid")
}

// ProbeTenantIsolation validates that a same idempotency key in tenant B is not
// replayed from tenant A.
func ProbeTenantIsolation(obs TenantIsolationObservation) ProbeResult {
	chassis.AssertVersionChecked()
	if !obs.TenantBHandlerExecuted {
		return fail("tenant B handler did not execute")
	}
	if strings.ToLower(obs.TenantBReplayHeader.Get("Idempotency-Replayed")) == "true" {
		return fail("tenant B response was replayed")
	}
	if len(obs.TenantABody) > 0 && bytes.Equal(obs.TenantABody, obs.TenantBBody) {
		return fail("tenant B body matches tenant A body; isolation evidence is ambiguous")
	}
	if obs.TenantAStatus == 0 || obs.TenantBStatus == 0 {
		return fail("missing tenant response status")
	}
	return pass("idempotency key is tenant-scoped")
}

func (e Evidence) satisfies(req string) bool {
	switch req {
	case ReqAcceptsXTraceID:
		return e.AcceptsXTraceID
	case ReqEmitsProblemJSONErrors:
		return e.EmitsProblemJSONErrors
	case ReqScopedBearerAuthOnMutatingRoutes:
		return e.ScopedBearerAuthOnMutatingRoutes
	case ReqErrorProblemJSONRetryClass:
		return e.ErrorProblemJSONRetryClass
	case ReqCapabilityManifest:
		return e.CapabilityManifest || manifestValid(e.Manifest)
	case ReqIdempotencyKeyReplayForMutating:
		return e.IdempotencyKeyReplayForMutating
	case ReqIdempotencyStoreDeclared:
		return e.IdempotencyStoreDeclared || manifestDeclaresIdempotencyStore(e.Manifest)
	case ReqIdempotencyKeyTenantScoped:
		return e.IdempotencyKeyTenantScoped
	case ReqOutboxOrEquivalentDeclared:
		return e.OutboxOrEquivalentDeclared
	case ReqDomainEventsTraceAndEntityRefs:
		return e.DomainEventsTraceAndEntityRefs
	default:
		return false
	}
}

func manifestValid(manifest *orchestration.Manifest) bool {
	if manifest == nil {
		return false
	}
	normalized := manifest.Normalize()
	return normalized.Validate() == nil && normalized.ContractVersion == ContractVersion
}

func manifestDeclaresIdempotencyStore(manifest *orchestration.Manifest) bool {
	if manifest == nil || manifest.Idempotency == nil {
		return false
	}
	store := strings.ToLower(strings.TrimSpace(manifest.Idempotency.Store))
	return store != "" && store != "none"
}

func needsProductionDurability(manifest *orchestration.Manifest) bool {
	return manifest != nil && manifest.Profile == orchestration.ProfileL2Prod && manifestDeclaresIdempotencyStore(manifest)
}

func manifestDeclaresDurableStore(manifest *orchestration.Manifest) bool {
	if manifest == nil || manifest.Idempotency == nil {
		return false
	}
	store := strings.ToLower(strings.TrimSpace(manifest.Idempotency.Store))
	return manifest.Idempotency.Durable && store != "" && store != "none" && store != "memory"
}

func levelAtLeast(got, want Level) bool {
	return levelRank(got) >= levelRank(want) && levelRank(want) > 0
}

func levelRank(level Level) int {
	switch level {
	case LevelL0:
		return 1
	case LevelL1:
		return 2
	case LevelL2:
		return 3
	case LevelL3:
		return 4
	default:
		return 0
	}
}

func pass(reason string) ProbeResult {
	return ProbeResult{Passed: true, Reason: reason}
}

func fail(reason string, kv ...any) ProbeResult {
	return ProbeResult{Passed: false, Reason: reason, Details: details(kv...)}
}

func details(kv ...any) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	out := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		out[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return out
}

func intFromJSON(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return -1
	}
}

// SortedMissing returns a copy of the missing requirements in stable order.
func (r Report) SortedMissing() []string {
	chassis.AssertVersionChecked()
	out := append([]string(nil), r.Missing...)
	sort.Strings(out)
	return out
}
