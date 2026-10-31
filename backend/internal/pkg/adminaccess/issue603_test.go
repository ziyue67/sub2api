package adminaccess

import "testing"

func TestIssue603ObservabilityRequiresGlobalReadPermission(t *testing.T) {
	for _, tc := range []struct{ path, permission string }{
		{"/usage/latency-report", "usage.read"}, {"/ops/dashboard/latency-report", "ops.read"}, {"/ops/dashboard/network-series", "ops.read"},
	} {
		path := "/api/v1/admin" + tc.path
		if RouteAllowed(Policy{}, "GET", path) {
			t.Fatal("empty policy can read global telemetry")
		}
		if !RouteAllowed(Policy{GlobalPermissions: []string{tc.permission}}, "GET", path) {
			t.Fatalf("explicit global permission must read %s", path)
		}
		if RouteAllowed(Policy{Rules: []Rule{{Permissions: []string{tc.permission}, AllGroups: true}}}, "GET", path) {
			t.Fatal("account/group rules must not grant global telemetry")
		}
	}
}
