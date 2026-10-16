//go:build integration

package repository

import (
	"errors"
	"fmt"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func registrationIPTestUser(email string) *service.User {
	return &service.User{Email: email, PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive}
}

func (s *UserRepoSuite) TestRegistrationIPConcurrentAcrossCreatePaths() {
	ctx := authguard.WithRegistrationIP(s.ctx, "192.0.2.10")
	const attempts = 8
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			user := registrationIPTestUser(fmt.Sprintf("user%d@domain%d.example", i, i))
			switch i % 3 {
			case 0:
				errs <- s.repo.Create(ctx, user) // automatic OAuth signup
			case 1:
				errs <- s.repo.CreateWithEmailAliasGuard(ctx, user) // email signup
			default:
				errs <- s.repo.CreateWithEmailAliasGuardAndDomainLimit(ctx, user, fmt.Sprintf("domain%d.example", i))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	var created, limited int
	for err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, service.ErrRegistrationIPLimit):
			limited++
		default:
			s.Require().NoError(err)
		}
	}
	s.Require().Equal(2, created)
	s.Require().Equal(attempts-2, limited)
	count, err := s.client.User.Query().Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(2, count, "quota losers must not leave users behind")
	var quota int
	err = integrationDB.QueryRowContext(s.ctx, "SELECT account_count FROM registration_ip_quotas WHERE ip_hash = $1", authguard.IPKey("192.0.2.10")).Scan(&quota)
	s.Require().NoError(err)
	s.Require().Equal(2, quota)
}

func (s *UserRepoSuite) TestRegistrationIPRollbackAndDeletion() {
	ctx := authguard.WithRegistrationIP(s.ctx, "192.0.2.20")
	tx, err := s.client.Tx(ctx)
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	s.Require().NoError(s.repo.CreateWithEmailAliasGuard(txCtx, registrationIPTestUser("rollback@example.com")))
	s.Require().NoError(tx.Rollback(), "outer rollback must also release the IP quota")

	first := registrationIPTestUser("first@example.com")
	s.Require().NoError(s.repo.Create(ctx, first))
	s.Require().ErrorIs(s.repo.Create(ctx, registrationIPTestUser(first.Email)), service.ErrEmailExists)
	s.Require().NoError(s.repo.Create(ctx, registrationIPTestUser("second@example.com")))
	s.Require().NoError(s.repo.Delete(s.ctx, first.ID))
	s.Require().ErrorIs(s.repo.Create(ctx, registrationIPTestUser("third@example.com")), service.ErrRegistrationIPLimit, "deletion must not refund lifetime quota")
	s.Require().NoError(s.repo.Create(s.ctx, registrationIPTestUser("admin-created@example.com")), "admin creation has no public signup marker")
	otherIP := authguard.WithRegistrationIP(s.ctx, "192.0.2.21")
	s.Require().NoError(s.repo.Create(otherIP, registrationIPTestUser("other-ip@example.com")))
}

func (s *UserRepoSuite) TestRegistrationIPInjectedTransactionAndInvalidIP() {
	ctx := authguard.WithRegistrationIP(s.ctx, "2001:db8::1")
	tx, err := s.client.Tx(ctx)
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	// Client injection without dbent.NewTxContext must still use the same tx
	// for raw quota SQL, rather than accidentally writing through integrationDB.
	repo := newUserRepositoryWithSQL(tx.Client(), integrationDB)
	s.Require().NoError(repo.Create(ctx, registrationIPTestUser("injected@example.com")))
	s.Require().NoError(tx.Rollback())

	var count int
	err = integrationDB.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM registration_ip_quotas WHERE ip_hash = $1", authguard.IPKey("2001:db8::1")).Scan(&count)
	s.Require().NoError(err)
	s.Require().Zero(count)
	ctx = authguard.WithRegistrationIP(s.ctx, "invalid")
	s.Require().ErrorIs(s.repo.Create(ctx, registrationIPTestUser("invalid-ip@example.com")), service.ErrServiceUnavailable)
}

func (s *UserRepoSuite) TestRegistrationIPv6PrefixQuotaAndTimestamp() {
	first := authguard.WithRegistrationIP(s.ctx, "2001:db8:2::1")
	rotated := authguard.WithRegistrationIP(s.ctx, "2001:db8:2::abcd")
	s.Require().NoError(s.repo.Create(first, registrationIPTestUser("ipv6-first@example.com")))
	s.Require().NoError(s.repo.Create(rotated, registrationIPTestUser("ipv6-second@example.com")))
	s.Require().ErrorIs(s.repo.Create(rotated, registrationIPTestUser("ipv6-third@example.com")), service.ErrRegistrationIPLimit)
	otherPrefix := authguard.WithRegistrationIP(s.ctx, "2001:db8:2:1::1")
	s.Require().NoError(s.repo.Create(otherPrefix, registrationIPTestUser("ipv6-independent@example.com")))
	var createdAt time.Time
	err := integrationDB.QueryRowContext(s.ctx, "SELECT created_at FROM registration_ip_quotas WHERE ip_hash = $1", authguard.IPKey("2001:db8:2::1")).Scan(&createdAt)
	s.Require().NoError(err)
	s.Require().False(createdAt.IsZero(), "quota age is observable but never used to refund signup slots")
}
