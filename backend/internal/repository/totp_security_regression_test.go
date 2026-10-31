//go:build unit

package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type fixtureTotpEncryption struct{}

func (fixtureTotpEncryption) Encrypt(value string) (string, error) { return value, nil }
func (fixtureTotpEncryption) Decrypt(value string) (string, error) { return value, nil }

func TestTKSecurityTotpReplayAndCacheFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "one-time-code", true: "cache-fail-closed"}[failure], func(t *testing.T) {
			server := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = rdb.Close() })
			secret := "JBSWY3DPEHPK3PXP"
			repo := &sessionSecurityUsers{user: service.User{ID: 29, TotpEnabled: true, TotpSecretEncrypted: &secret}}
			svc := service.NewTotpService(repo, fixtureTotpEncryption{}, repository.NewTotpCache(rdb), nil, nil, nil)
			code, err := totp.GenerateCode(secret, time.Now())
			require.NoError(t, err)
			if failure {
				server.Close()
				require.Error(t, svc.VerifyCode(context.Background(), 29, code))
				return
			}
			require.NoError(t, svc.VerifyCode(context.Background(), 29, code))
			require.Error(t, svc.VerifyCode(context.Background(), 29, code), "an accepted time step must be consumed")
			_, err = svc.VerifyStepUp(context.Background(), 29, "another-device", code)
			require.Error(t, err, "a login TOTP code must not be reused for step-up")
		})
	}
}

func TestTKSecurityTotpConcurrency(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "bounded-attempts", true: "one-winner"}[valid], func(t *testing.T) {
			server := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			secret := "JBSWY3DPEHPK3PXP"
			repo := &sessionSecurityUsers{user: service.User{ID: 29, TotpEnabled: true, TotpSecretEncrypted: &secret}}
			svc := service.NewTotpService(repo, fixtureTotpEncryption{}, repository.NewTotpCache(rdb), nil, nil, nil)
			code := "invalid"
			if valid {
				var err error
				code, err = totp.GenerateCode(secret, time.Now())
				require.NoError(t, err)
			}
			var successes, limited atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := svc.VerifyCode(context.Background(), 29, code)
					if err == nil {
						successes.Add(1)
					}
					if err == service.ErrTotpTooManyAttempts {
						limited.Add(1)
					}
				}()
			}
			wg.Wait()
			if valid {
				require.EqualValues(t, 1, successes.Load())
			} else {
				require.Zero(t, successes.Load())
				require.EqualValues(t, 27, limited.Load())
			}
		})
	}
}
