package upstreamroute

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func configForTest() Config {
	return Config{Enabled: true, Regions: []Region{{ID: "us", ProxyURLEnv: "TEST_EGRESS_US"}, {ID: "eu", ProxyURLEnv: "TEST_EGRESS_EU"}}, Rules: []Rule{{Domain: "*.example.com", Region: "us"}, {Domain: "*.eu.example.com", Region: "eu"}, {Domain: "API.EXAMPLE.COM.", Region: "eu"}, {Domain: "127.0.0.1", Region: "us"}}}
}
func TestDomainRoutingSpecificityAndBoundaries(t *testing.T) {
	t.Setenv("TEST_EGRESS_US", "socks5://127.0.0.1:1081")
	t.Setenv("TEST_EGRESS_EU", "https://eu.example:8443")
	r, err := New(configForTest())
	require.NoError(t, err)
	for _, tc := range []struct{ target, region string }{
		{"https://api.example.com/v1?q=us", "eu"}, {"wss://API.EXAMPLE.COM:443/v1", "eu"}, {"http://a.eu.example.com:80/", "eu"}, {"https://a.example.com./", "us"}, {"https://example.com/", ""}, {"https://notexample.com/", ""}, {"https://example.com.attacker.test/", ""}, {"https://127.0.0.1:443/", "us"}, {"ftp://a.example.com/", ""},
	} {
		u, e := url.Parse(tc.target)
		require.NoError(t, e)
		proxy, region := r.Match(u)
		require.Equal(t, tc.region, region, tc.target)
		if region == "us" {
			require.Equal(t, "socks5h://127.0.0.1:1081", proxy)
		}
	}
	t.Setenv("TEST_EGRESS_US", "http://changed.example:80")
	u, err := url.Parse("https://a.example.com")
	require.NoError(t, err)
	proxy, _ := r.Match(u)
	require.Equal(t, "socks5h://127.0.0.1:1081", proxy, "existing connections and route policy must not rotate on environment changes")
}

func TestAccountScopedRouting(t *testing.T) {
	t.Setenv("TEST_EGRESS_US", "http://127.0.0.1:1081")
	t.Setenv("TEST_EGRESS_EU", "http://127.0.0.1:1082")
	c := Config{Enabled: true,
		Regions: []Region{{ID: "us", ProxyURLEnv: "TEST_EGRESS_US"}, {ID: "eu", ProxyURLEnv: "TEST_EGRESS_EU"}},
		Rules: []Rule{
			{Domain: "api.example.com", Region: "eu"},
			{Domain: "api.example.com", Region: "us", AccountIDs: []int64{101}},
			{Domain: "*.example.com", Region: "us", AccountIDs: []int64{102}},
		},
	}
	r, err := New(c)
	require.NoError(t, err)
	exact, _ := url.Parse("https://api.example.com/v1/responses")
	other, _ := url.Parse("https://other.example.com/v1/responses")
	_, region := r.MatchForAccount(exact, 101)
	require.Equal(t, "us", region)
	_, region = r.MatchForAccount(exact, 102)
	require.Equal(t, "eu", region, "an exact global rule takes precedence over a scoped wildcard")
	_, region = r.MatchForAccount(exact, 103)
	require.Equal(t, "eu", region)
	_, region = r.Match(exact)
	require.Equal(t, "eu", region)
	_, region = r.MatchForAccount(other, 102)
	require.Equal(t, "us", region)
	_, region = r.MatchForAccount(other, 101)
	require.Empty(t, region)
}
func TestInvalidRoutingNeverDisclosesSecrets(t *testing.T) {
	for _, raw := range []string{"", "http://test-user:private-secret@[::bad", "file://test-user:private-secret@host", "http://test-user:private-secret@host/path", "http://host:65536", "http://host?private-secret", "http://host#private-secret"} {
		t.Run(fmt.Sprint(len(raw), raw == ""), func(t *testing.T) {
			t.Setenv("TEST_EGRESS_US", raw)
			t.Setenv("TEST_EGRESS_EU", "http://127.0.0.1:8080")
			_, err := New(configForTest())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-secret")
			require.NotContains(t, err.Error(), "test-user")
		})
	}
}
func TestValidateRoutingRules(t *testing.T) {
	t.Setenv("TEST_EGRESS_US", "http://127.0.0.1:8080")
	t.Setenv("TEST_EGRESS_EU", "http://127.0.0.1:8081")
	for _, domain := range []string{"*", "*.com", "https://api.example.com", "example.com/path", "foo.*.com", "-invalid.example", "api.example.com:443", "", "*.127.0.0.1"} {
		c := configForTest()
		c.Rules = []Rule{{Domain: domain, Region: "us"}}
		_, err := New(c)
		require.Error(t, err, domain)
	}
	c := configForTest()
	c.Rules = append(c.Rules, Rule{Domain: "api.example.com", Region: "us"})
	_, err := New(c)
	require.Error(t, err)
	for _, ids := range [][]int64{{0}, {-1}, {101, 101}} {
		c = configForTest()
		c.Rules[0].AccountIDs = ids
		_, err = New(c)
		require.Error(t, err)
	}
	c = configForTest()
	c.Rules = append(c.Rules, Rule{Domain: "api.example.com", Region: "us", AccountIDs: []int64{101}})
	_, err = New(c)
	require.NoError(t, err, "global and scoped rules may share a domain")
	c.Rules = append(c.Rules, Rule{Domain: "api.example.com", Region: "eu", AccountIDs: []int64{101}})
	_, err = New(c)
	require.Error(t, err, "an account must not have two rules for the same domain")
	c = configForTest()
	c.Rules[0].Region = "missing"
	_, err = New(c)
	require.Error(t, err)
	c = configForTest()
	c.Regions = append(c.Regions, c.Regions[0])
	_, err = New(c)
	require.Error(t, err)
	c = configForTest()
	c.Rules = nil
	_, err = New(c)
	require.Error(t, err)
	c = configForTest()
	c.Enabled = false
	t.Setenv("TEST_EGRESS_US", "")
	t.Setenv("TEST_EGRESS_EU", "")
	r, err := New(c)
	require.NoError(t, err)
	require.Nil(t, r)
	_, region := r.Match(nil)
	require.Empty(t, region)
	err = TransportError(fmt.Errorf("private-secret: %w", context.Canceled))
	require.True(t, errors.Is(err, context.Canceled))
	require.NotContains(t, err.Error(), "private-secret")
}
