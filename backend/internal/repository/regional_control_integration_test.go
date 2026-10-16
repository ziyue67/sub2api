//go:build integration

package repository

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/stretchr/testify/require"
)

func regionalControlTLS(t *testing.T) (*x509.CertPool, tls.Certificate, tls.Certificate, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-regional-control-ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	makeCert := func(serial int64, usage x509.ExtKeyUsage) tls.Certificate {
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: fmt.Sprintf("peer-%d", serial)}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		raw, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
		require.NoError(t, err)
		return tls.Certificate{Certificate: [][]byte{raw, der}, PrivateKey: leafKey}
	}
	return roots, makeCert(2, x509.ExtKeyUsageServerAuth), makeCert(3, x509.ExtKeyUsageClientAuth), makeCert(4, x509.ExtKeyUsageClientAuth)
}
func regionalControlPin(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}
func TestRegionalControlMTLSScopeAndDurableOutbox(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	quota := &ledger.Primary{DB: integrationDB}
	local := &ledger.Local{DB: integrationDB, Region: "us"}
	roots, serverCert, clientCert, otherClient := regionalControlTLS(t)
	scope := control.Scope{Region: "us", CertificateSHA256: regionalControlPin(clientCert), UserIDs: []int64{user}, APIKeyIDs: []int64{1}, AccountIDs: []int64{2}, MaximumGrant: 1_000_000_000, MaximumSlots: 2, MaximumLifetime: time.Hour}
	verified := 0
	handler, err := control.NewHandler(scope, quota, NewRegionalSlotAuthority(integrationDB), func(context.Context, *sql.Tx, ledger.Event) error { verified++; return nil })
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, Certificates: []tls.Certificate{serverCert}}
	server.StartTLS()
	defer server.Close()
	client, err := control.NewClient(server.URL, roots, clientCert, regionalControlPin(serverCert))
	require.NoError(t, err)
	defer client.Close()
	lease := regionalLedgerLease(fmt.Sprintf("control-%d", user), user, 1_000_000_000)
	require.NoError(t, client.IssueQuota(ctx, lease))
	require.NoError(t, client.IssueQuota(ctx, lease))
	require.NoError(t, local.Install(ctx, lease))
	// A CA-trusted peer with an unassigned leaf pin is still unauthorized.
	wrong, err := control.NewClient(server.URL, roots, otherClient, regionalControlPin(serverCert))
	require.NoError(t, err)
	defer wrong.Close()
	require.Error(t, wrong.IssueQuota(ctx, lease))
	badServer, err := control.NewClient(server.URL, roots, clientCert, regionalControlPin(otherClient))
	require.NoError(t, err)
	defer badServer.Close()
	require.Error(t, badServer.IssueQuota(ctx, lease))
	foreign := lease
	foreign.LeaseID += "-foreign"
	foreign.RegionID = "eu"
	require.Error(t, client.IssueQuota(ctx, foreign))
	oversized := lease
	oversized.LeaseID += "-oversized"
	oversized.Granted += 10
	require.Error(t, client.IssueQuota(ctx, oversized))
	r := regionalLedgerReservation(lease, fmt.Sprintf("control-req-%d", user), 1_000_000_000)
	fresh, err := local.Reserve(ctx, r)
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, local.Start(ctx, r.RequestID))
	event := regionalLedgerEvent(r, 500_000_000)
	require.NoError(t, local.Complete(ctx, event))
	count, err := local.Flush(ctx, client, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1)
	require.Equal(t, 1, verified)
	require.NoError(t, client.Settle(ctx, event))
	require.Equal(t, 1, verified)
	require.NoError(t, local.Drain(ctx, lease.LeaseID))
	receipt, err := local.Close(ctx, lease.LeaseID)
	require.NoError(t, err)
	require.NoError(t, client.CloseQuota(ctx, receipt))
	var balance string
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, "9.50000000", balance)
}
