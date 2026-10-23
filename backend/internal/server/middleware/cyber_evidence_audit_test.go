package middleware

import "testing"

func TestCyberEvidenceSensitiveReadsAndExportsAreAudited(t *testing.T) {
	for key, want := range map[string]string{
		"GET /api/v1/admin/risk-control/logs/:id/evidence":        "admin.cyber_evidence.read",
		"GET /api/v1/admin/risk-control/logs/:id/evidence/export": "admin.cyber_evidence.export",
		"GET /api/v1/admin/risk-control/evidence/config":          "admin.cyber_evidence.config.read",
	} {
		if got := auditSensitiveReads[key]; got != want {
			t.Fatalf("%s audit=%q want %q", key, got, want)
		}
	}
	for key, want := range map[string]string{
		"DELETE /api/v1/admin/risk-control/logs/:id/evidence": "admin.cyber_evidence.delete",
		"PUT /api/v1/admin/risk-control/evidence/config":      "admin.cyber_evidence.config.update",
	} {
		if got := auditActionOverrides[key]; got != want {
			t.Fatalf("%s audit=%q want %q", key, got, want)
		}
	}
}
