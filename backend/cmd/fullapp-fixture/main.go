//go:build fullappfixture

// CI fixture only. Never linked into cmd/server or a production release.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type fixtureInput struct {
	Database  config.DatabaseConfig `json:"database"`
	Redis     config.RedisConfig    `json:"redis"`
	APIKey    string                `json:"api_key"`
	Origin    string                `json:"origin"`
	UserID    int64                 `json:"user_id"`
	APIKeyID  int64                 `json:"api_key_id"`
	AccountID int64                 `json:"account_id"`
}

type fixtureOutput struct {
	UserID       int64   `json:"user_id"`
	APIKeyID     int64   `json:"api_key_id"`
	AccountID    int64   `json:"account_id"`
	UsageRows    int64   `json:"usage_rows"`
	DedupRows    int64   `json:"dedup_rows"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Balance      float64 `json:"balance"`
	UserSlots    int     `json:"user_slots"`
	AccountSlots int     `json:"account_slots"`
	APIKeySlots  int     `json:"api_key_slots"`
}

var fixtureStage = "input"

func main() {
	// Repository failures can contain driver details. Only a fixed outer error
	// is returned; no SQL/credentials/config/error strings enter fixture logs.
	log.SetOutput(io.Discard)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if os.Getenv("CI_FULLAPP_FIXTURE") != "1" || len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "explicit isolated CI fixture required")
		os.Exit(2)
	}
	if os.Args[1] == "serve" {
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "synthetic upstream stopped")
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "isolated seed or observation failed stage="+fixtureStage)
		os.Exit(1)
	}
}

func run(op string) error {
	var in fixtureInput
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return err
	}
	// Reject arbitrary existing endpoints even if somebody invokes this tool.
	if !strings.HasPrefix(in.Database.Host, "s2-bootstrap-") || !strings.HasSuffix(in.Database.Host, "-pg") ||
		in.Database.DBName != "bootstrap_fixture" || in.Database.Port != 5432 || in.Database.SSLMode != "disable" ||
		!strings.HasPrefix(in.Redis.Host, "s2-bootstrap-") || !strings.HasSuffix(in.Redis.Host, "-redis") || in.Redis.Port != 6379 || in.Redis.DB != 0 {
		return errors.New("fixture endpoints required")
	}
	prefix := strings.TrimSuffix(in.Database.Host, "-pg")
	if strings.TrimSuffix(in.Redis.Host, "-redis") != prefix {
		return errors.New("one fixture namespace required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := timezone.Init("UTC"); err != nil {
		return err
	}
	db, err := sql.Open("postgres", in.Database.DSNWithTimezone("UTC"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(2)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	cache := redis.NewClient(&redis.Options{Addr: in.Redis.Address(), MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	defer cache.Close()
	var out fixtureOutput
	switch op {
	case "seed":
		if in.Origin != "http://"+prefix+"-model:9090" ||
			!strings.HasPrefix(in.APIKey, "sk-fixture-") {
			return errors.New("synthetic model/key required")
		}
		fixtureStage = "create-user"
		user, err := client.User.Create().SetEmail("model-fixture@example.invalid").
			SetPasswordHash("not-a-real-login-hash").SetRole(service.RoleUser).
			SetStatus(service.StatusActive).SetBalance(10).SetConcurrency(2).Save(ctx)
		if err != nil {
			return err
		}
		fixtureStage = "create-group"
		group, err := client.Group.Create().SetName("model-fixture").SetPlatform(service.PlatformOpenAI).
			SetSubscriptionType(service.SubscriptionTypeStandard).SetStatus(service.StatusActive).
			SetRateMultiplier(1).Save(ctx)
		if err != nil {
			return err
		}
		key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: in.APIKey,
			Name: "model-fixture", Status: service.StatusActive, ConcurrencyLimit: 2}
		fixtureStage = "create-key"
		if err := repository.NewAPIKeyRepository(client, db).Create(ctx, key); err != nil {
			return err
		}
		snapshots := repository.NewSchedulerCache(cache)
		accounts := repository.NewAccountRepository(client, db, snapshots)
		account := &service.Account{Name: "model-fixture", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Concurrency: 2, Priority: 1, QuotaDimension: "global",
			Credentials: map[string]any{"api_key": "synthetic-upstream-only", "base_url": in.Origin,
				"model_mapping": map[string]string{"gpt-6.1-sol": "gpt-6.1-sol"}},
			Extra: map[string]any{"openai_passthrough": true}}
		fixtureStage = "create-account-group"
		atomicCreator, ok := accounts.(service.AccountDuplicateRepository)
		if !ok {
			return errors.New("fixture requires atomic account/group repository capability")
		}
		if err := atomicCreator.CreateWithAccountGroups(ctx, account, []service.AccountGroup{{GroupID: group.ID, Priority: 1}}); err != nil {
			return err
		}
		fixtureStage = "hydrate-account"
		actual, err := accounts.GetByID(ctx, account.ID)
		if err != nil {
			return err
		}
		if !actual.IsSchedulable() || actual.ParentAccountID != nil || actual.QuotaDimension != "global" {
			return errors.New("root account eligibility not preserved")
		}
		fixtureStage = "publish-snapshot"
		bucket := service.SchedulerBucket{GroupID: group.ID, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
		token, err := snapshots.CaptureBucketWriteToken(ctx, bucket)
		if err != nil {
			return err
		}
		if err := snapshots.SetSnapshot(ctx, bucket, token, []service.Account{*actual}); err != nil {
			return err
		}
		out.UserID, out.APIKeyID, out.AccountID = user.ID, key.ID, account.ID
	case "status":
		fixtureStage = "observe-usage-billing-slots"
		if in.UserID <= 0 || in.APIKeyID <= 0 || in.AccountID <= 0 {
			return errors.New("fixture ids required")
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(input_tokens),0),coalesce(sum(output_tokens),0)
			FROM usage_logs WHERE user_id=$1 AND api_key_id=$2 AND account_id=$3`,
			in.UserID, in.APIKeyID, in.AccountID).Scan(&out.UsageRows, &out.InputTokens, &out.OutputTokens); err != nil {
			return err
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1`, in.APIKeyID).Scan(&out.DedupRows); err != nil {
			return err
		}
		if err := db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, in.UserID).Scan(&out.Balance); err != nil {
			return err
		}
		slots := repository.NewConcurrencyCache(cache, 15, 30)
		out.UserSlots, err = slots.GetUserConcurrency(ctx, in.UserID)
		if err != nil {
			return err
		}
		out.AccountSlots, err = slots.GetAccountConcurrency(ctx, in.AccountID)
		if err != nil {
			return err
		}
		keys, err := slots.(service.APIKeyConcurrencyCache).GetAPIKeyConcurrencyBatch(ctx, []int64{in.APIKeyID})
		if err != nil {
			return err
		}
		out.APIKeySlots = keys[in.APIKeyID]
	default:
		return errors.New("unknown fixture operation")
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

type modelState struct {
	mu                          sync.Mutex
	accepted, completed, active int
	release                     chan struct{}
	releaseOnce                 sync.Once
	failureRelease              chan struct{}
	failureReleaseOnce          sync.Once
}

func serve() error {
	s := &modelState{release: make(chan struct{}), failureRelease: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/fixture/release", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.releaseOnce.Do(func() { close(s.release) })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fixture/release-failure", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.failureReleaseOnce.Do(func() { close(s.failureRelease) })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fixture/stats", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]int{"accepted": s.accepted, "completed": s.completed, "active": s.active})
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string          `json:"model"`
			Input json.RawMessage `json:"input"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&body) != nil ||
			body.Model != "gpt-6.1-sol" || r.Header.Get("Authorization") != "Bearer synthetic-upstream-only" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.accepted++
		id := s.accepted
		s.active++
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
		if bytes.Contains(body.Input, []byte("fixture-accepted-before-response-eof")) {
			// Body is fully read, authenticated and counted, but no response has
			// been written. This is distinct from EOF after semantic output.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				panic(http.ErrAbortHandler)
			}
			_ = connection.Close()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", fmt.Sprintf("fixture-model-%d", id))
		event := func(name string, payload any) error {
			encoded, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded)
			w.(http.Flusher).Flush()
			return err
		}
		responseID := fmt.Sprintf("resp_fixture_%d", id)
		response := map[string]any{"id": responseID, "object": "response", "created_at": 1700000000,
			"model": "gpt-6.1-sol", "status": "in_progress", "output": []any{}}
		if event("response.created", map[string]any{"type": "response.created", "response": response}) != nil {
			return
		}
		if event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": "OK"}) != nil {
			return
		}
		if bytes.Contains(body.Input, []byte("fixture-partial-sse-eof")) {
			// Deliberately finish the HTTP response after real semantic output,
			// without terminal usage/completion. The application must not turn
			// this accepted/partly emitted request into another generation.
			return
		}
		if bytes.Contains(body.Input, []byte("fixture-held-partial-sse-eof")) {
			// The failed stream has its own control so its peer's successful
			// stream stays active while real per-request leases are released.
			select {
			case <-s.failureRelease:
			case <-r.Context().Done():
				return
			case <-time.After(12 * time.Second):
				return
			}
			return // Semantic output, but no terminal usage or completion.
		}
		if bytes.Contains(body.Input, []byte("fixture-hold-until-release")) {
			// Synthetic control only, not a gateway replay/recovery mechanism.
			select {
			case <-s.release:
			case <-r.Context().Done():
				return
			case <-time.After(12 * time.Second):
				return
			}
		}
		response["status"] = "completed"
		response["output"] = []any{map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "OK", "annotations": []any{}}}}}
		response["usage"] = map[string]any{"input_tokens": 7, "output_tokens": 2, "total_tokens": 9}
		if event("response.completed", map[string]any{"type": "response.completed", "response": response}) != nil {
			return
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		s.mu.Lock()
		s.completed++
		s.mu.Unlock()
	})
	server := &http.Server{Addr: ":9090", Handler: mux, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second}
	return server.ListenAndServe()
}
