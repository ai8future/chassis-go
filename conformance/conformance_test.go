package conformance

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/orchestration"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func TestRequirementConstantsMatchPinnedVocabulary(t *testing.T) {
	data := mustReadFixture(t, "conformance.yaml")
	text := string(data)
	for _, req := range []string{
		ReqAcceptsXTraceID,
		ReqEmitsProblemJSONErrors,
		ReqScopedBearerAuthOnMutatingRoutes,
		ReqErrorProblemJSONRetryClass,
		ReqCapabilityManifest,
		ReqIdempotencyKeyReplayForMutating,
		ReqIdempotencyStoreDeclared,
		ReqIdempotencyKeyTenantScoped,
		ReqOutboxOrEquivalentDeclared,
		ReqDomainEventsTraceAndEntityRefs,
	} {
		if !strings.Contains(text, req) {
			t.Fatalf("pinned conformance vocabulary missing %s", req)
		}
	}
}

func TestTraceIDFormats(t *testing.T) {
	canonical := "tr_0123456789abcdef0123456789abcdef"
	legacy := "tr_0123456789ab"
	if !ValidCanonicalTraceID(canonical) || !AcceptsTraceID(canonical) {
		t.Fatalf("canonical trace id rejected")
	}
	if ValidCanonicalTraceID(legacy) || !AcceptsTraceID(legacy) {
		t.Fatalf("bounded legacy trace id handling is wrong")
	}
	for _, invalid := range []string{"tr_0123456789abc", "TR_0123456789abcdef0123456789abcdef", "tr_0123456789abcdef0123456789abcdeg", "anything"} {
		if AcceptsTraceID(invalid) {
			t.Fatalf("invalid trace id accepted: %s", invalid)
		}
	}
}

func TestProblemProbeAcceptsPinnedFixtures(t *testing.T) {
	for _, name := range []string{"problem.retryable.json", "problem.terminal.json"} {
		body := mustReadFixture(t, filepath.Join("fixtures", name))
		var problem struct {
			Status int `json:"status"`
		}
		if err := json.Unmarshal(body, &problem); err != nil {
			t.Fatalf("decode fixture %s: %v", name, err)
		}
		result := ProbeProblemJSON(ProblemObservation{
			StatusCode: problem.Status,
			Header:     http.Header{"Content-Type": {"application/problem+json"}},
			Body:       body,
		})
		if !result.Passed {
			t.Fatalf("%s should pass problem probe: %#v", name, result)
		}
	}
}

func TestProblemProbeRejectsMissingRetryClass(t *testing.T) {
	result := ProbeProblemJSON(ProblemObservation{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/problem+json"}},
		Body:       []byte(`{"type":"x","title":"x","status":503,"code":"dependency_unavailable"}`),
	})
	if result.Passed {
		t.Fatalf("probe should reject missing retryable classification")
	}
}

func TestManifestProbeAcceptsPinnedFixture(t *testing.T) {
	body := mustReadFixture(t, filepath.Join("fixtures", "manifest.l2-prod.json"))
	manifest, result := ProbeManifest(body)
	if !result.Passed {
		t.Fatalf("manifest fixture should pass: %#v", result)
	}
	if got := LevelForProfile(manifest.Profile); got != LevelL2 {
		t.Fatalf("LevelForProfile = %s, want L2", got)
	}
}

func TestManifestProbeRejectsUnknownTopLevelField(t *testing.T) {
	body := []byte(`{"service":"svc","version":"1","profile":"L0","contract_version":"0.1","capabilities":["problem-json"],"extra":true}`)
	_, result := ProbeManifest(body)
	if result.Passed {
		t.Fatalf("manifest probe should reject unknown top-level field")
	}
}

func TestCheckL2PassesWithPinnedManifestEvidence(t *testing.T) {
	manifest := pinnedManifest(t)
	problem := ProbeProblemJSON(ProblemObservation{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Content-Type": {"application/problem+json"}},
		Body:       mustReadFixture(t, filepath.Join("fixtures", "problem.retryable.json")),
	})
	replay := ProbeIdempotencyReplay(IdempotencyReplayObservation{
		FirstStatus:  http.StatusAccepted,
		ReplayStatus: http.StatusAccepted,
		FirstBody:    []byte(`{"message_id":"msg_123"}`),
		ReplayBody:   []byte(`{"message_id":"msg_123"}`),
		ReplayHeader: http.Header{"Idempotency-Replayed": {"true"}},
	})
	tenant := ProbeTenantIsolation(TenantIsolationObservation{
		TenantAStatus:          http.StatusAccepted,
		TenantBStatus:          http.StatusAccepted,
		TenantABody:            []byte(`{"message_id":"msg_A"}`),
		TenantBBody:            []byte(`{"message_id":"msg_B"}`),
		TenantBHandlerExecuted: true,
	})
	report := Check(LevelL2, Evidence{
		AcceptsXTraceID:                  AcceptsTraceID("tr_0123456789abcdef0123456789abcdef"),
		EmitsProblemJSONErrors:           problem.Passed,
		ErrorProblemJSONRetryClass:       problem.Passed,
		ScopedBearerAuthOnMutatingRoutes: true,
		Manifest:                         &manifest,
		IdempotencyKeyReplayForMutating:  replay.Passed,
		IdempotencyKeyTenantScoped:       tenant.Passed,
	})
	if !report.Passed {
		t.Fatalf("L2 report should pass: %#v", report)
	}
	if len(report.Checked) == 0 || report.ManifestProfile != string(orchestration.ProfileL2Prod) {
		t.Fatalf("unexpected report metadata: %#v", report)
	}
}

func TestCheckReportsMissingEvidenceAndProductionDurability(t *testing.T) {
	manifest := orchestration.Manifest{
		Service:         "svc",
		Version:         "1",
		Profile:         orchestration.ProfileL2Prod,
		ContractVersion: orchestration.DefaultContractVersion,
		Capabilities:    []string{"authkit", "idemkit", "problem-json"},
		Idempotency:     &orchestration.Idempotency{Store: "memory", Durable: false, TTLSeconds: 60},
	}
	report := Check(LevelL2, Evidence{Manifest: &manifest})
	for _, missing := range []string{
		ReqAcceptsXTraceID,
		ReqEmitsProblemJSONErrors,
		ReqScopedBearerAuthOnMutatingRoutes,
		ReqErrorProblemJSONRetryClass,
		ReqIdempotencyKeyReplayForMutating,
		ReqIdempotencyKeyTenantScoped,
		ReqDurableStoreRequiredForL2Production,
	} {
		if !contains(report.Missing, missing) {
			t.Fatalf("missing requirements %v do not include %s", report.Missing, missing)
		}
	}
}

func TestCheckL2MutantsFailOneRequirementEach(t *testing.T) {
	baseline := Evidence{
		AcceptsXTraceID:                  true,
		EmitsProblemJSONErrors:           true,
		ErrorProblemJSONRetryClass:       true,
		ScopedBearerAuthOnMutatingRoutes: true,
		CapabilityManifest:               true,
		IdempotencyStoreDeclared:         true,
		IdempotencyKeyReplayForMutating:  true,
		IdempotencyKeyTenantScoped:       true,
	}
	mutants := map[string]func(*Evidence){
		ReqAcceptsXTraceID:                  func(e *Evidence) { e.AcceptsXTraceID = false },
		ReqEmitsProblemJSONErrors:           func(e *Evidence) { e.EmitsProblemJSONErrors = false },
		ReqScopedBearerAuthOnMutatingRoutes: func(e *Evidence) { e.ScopedBearerAuthOnMutatingRoutes = false },
		ReqErrorProblemJSONRetryClass:       func(e *Evidence) { e.ErrorProblemJSONRetryClass = false },
		ReqCapabilityManifest:               func(e *Evidence) { e.CapabilityManifest = false },
		ReqIdempotencyKeyReplayForMutating:  func(e *Evidence) { e.IdempotencyKeyReplayForMutating = false },
		ReqIdempotencyStoreDeclared:         func(e *Evidence) { e.IdempotencyStoreDeclared = false },
		ReqIdempotencyKeyTenantScoped:       func(e *Evidence) { e.IdempotencyKeyTenantScoped = false },
	}
	for req, mutate := range mutants {
		t.Run(req, func(t *testing.T) {
			evidence := baseline
			mutate(&evidence)
			report := Check(LevelL2, evidence)
			if report.Passed || len(report.Missing) != 1 || report.Missing[0] != req {
				t.Fatalf("mutant report = %#v, want only missing %s", report, req)
			}
		})
	}
}

func TestCheckL3IsDeclarationOnly(t *testing.T) {
	manifest := pinnedManifest(t)
	manifest.Profile = orchestration.ProfileL3
	report := Check(LevelL3, Evidence{
		AcceptsXTraceID:                  true,
		EmitsProblemJSONErrors:           true,
		ErrorProblemJSONRetryClass:       true,
		ScopedBearerAuthOnMutatingRoutes: true,
		Manifest:                         &manifest,
		IdempotencyKeyReplayForMutating:  true,
		IdempotencyKeyTenantScoped:       true,
		OutboxOrEquivalentDeclared:       true,
		DomainEventsTraceAndEntityRefs:   true,
	})
	if !report.Passed || !report.L3DeclarationOnly || len(report.Warnings) == 0 {
		t.Fatalf("L3 report should pass with declaration warning: %#v", report)
	}
}

func pinnedManifest(t *testing.T) orchestration.Manifest {
	t.Helper()
	manifest, result := ProbeManifest(mustReadFixture(t, filepath.Join("fixtures", "manifest.l2-prod.json")))
	if !result.Passed {
		t.Fatalf("manifest fixture should pass: %#v", result)
	}
	return manifest
}

func mustReadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join("..", "testdata", "windmill", "contracts", rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
