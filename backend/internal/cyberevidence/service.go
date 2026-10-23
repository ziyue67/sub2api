package cyberevidence

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var ErrUnavailable = errors.New("cyber evidence unavailable")
var ErrNotFound = errors.New("cyber evidence not found")

type Cipher interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}
type Config struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
}

func DefaultConfig() Config  { return Config{Enabled: true, RetentionDays: 7} }
func (c Config) Valid() bool { return c.RetentionDays >= 1 && c.RetentionDays <= 30 }

type preparationLease struct {
	once    sync.Once
	release func()
}

// Release frees the bounded pre-queue reservation, including when the existing
// moderation scope declines the event or the parent audit insert fails.
func (p Prepared) Release() {
	if p.lease != nil {
		p.lease.once.Do(p.lease.release)
	}
}

type Prepared struct {
	lease            *preparationLease
	Ciphertext       string
	Status           string
	Truncated        bool
	RedactionVersion string
	OriginalBytes    int
	CapturedAt       time.Time
}
type Record struct {
	LogID      int64      `json:"log_id"`
	UserID     *int64     `json:"user_id,omitempty"`
	RequestID  string     `json:"request_id"`
	Endpoint   string     `json:"endpoint"`
	Model      string     `json:"model"`
	OccurredAt time.Time  `json:"occurred_at"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Ciphertext string     `json:"-"`
	Payload    *Payload   `json:"payload,omitempty"`
}

type Store interface {
	LoadConfig(context.Context) (Config, error)
	SaveConfig(context.Context, Config) error
	Save(context.Context, int64, Prepared, int) error
	Status(context.Context, int64, string) error
	Get(context.Context, int64) (Record, error)
	Delete(context.Context, int64) error
	Cleanup(context.Context, int) (int64, error)
}

type Stats struct {
	Queued            uint64 `json:"queued"`
	Saved             uint64 `json:"saved"`
	Failed            uint64 `json:"failed"`
	Dropped           uint64 `json:"dropped"`
	StatusWriteFailed uint64 `json:"status_write_failed"`
	CleanupFailed     uint64 `json:"cleanup_failed"`
	ConfigLoadFailed  uint64 `json:"config_load_failed"`
	EncryptionReady   bool   `json:"encryption_ready"`
	QueueDepth        int    `json:"queue_depth"`
	QueueCapacity     int    `json:"queue_capacity"`
}
type job struct {
	id int64
	p  Prepared
}
type Service struct {
	pendingPreparations                                                       chan struct{}
	store                                                                     Store
	cipher                                                                    Cipher
	ready                                                                     bool
	config                                                                    atomic.Pointer[Config]
	queue                                                                     chan job
	admission                                                                 chan struct{}
	stop                                                                      chan struct{}
	done                                                                      chan struct{}
	once                                                                      sync.Once
	mu                                                                        sync.RWMutex
	closed                                                                    bool
	queued, saved, failed, dropped, statusFailed, cleanupFailed, configFailed atomic.Uint64
}

func New(store Store, cipher Cipher, ready bool) *Service {
	s := &Service{pendingPreparations: make(chan struct{}, 64), store: store, cipher: cipher, ready: ready && cipher != nil, queue: make(chan job, 64), admission: make(chan struct{}, 2), stop: make(chan struct{}), done: make(chan struct{})}
	c := DefaultConfig()
	s.config.Store(&c)
	// All configuration reads have deadlines; an unavailable store disables capture.
	s.refresh()
	go s.run()
	return s
}
func (s *Service) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := s.store.LoadConfig(ctx)
	if err != nil || !c.Valid() {
		s.configFailed.Add(1)
		c = DefaultConfig()
		c.Enabled = false
	}
	s.config.Store(&c)
}
func (s *Service) Config() Config { return *s.config.Load() }
func (s *Service) UpdateConfig(ctx context.Context, c Config) error {
	if !c.Valid() {
		return errors.New("retention_days must be 1..30")
	}
	if err := s.store.SaveConfig(ctx, c); err != nil {
		return ErrUnavailable
	}
	s.config.Store(&c)
	return nil
}
func (s *Service) Prepare(body []byte, reason string, upstreamStatus ...int) Prepared {
	return s.prepare(body, reason, "", upstreamStatus...)
}

// PrepareWithSession also records the explicit sanitized session reference ID.
func (s *Service) PrepareWithSession(body []byte, reason, sessionID string, upstreamStatus ...int) Prepared {
	return s.prepare(body, reason, sessionID, upstreamStatus...)
}

func (s *Service) prepare(body []byte, reason, sessionID string, upstreamStatus ...int) Prepared {
	p := Prepared{Status: "disabled", CapturedAt: time.Now().UTC()}
	if !s.Config().Enabled {
		return p
	}
	if !s.ready {
		p.Status = "encryption_unavailable"
		return p
	}
	select {
	case s.pendingPreparations <- struct{}{}:
		p.lease = &preparationLease{release: func() { <-s.pendingPreparations }}
	default:
		p.Status = "capture_busy"
		s.dropped.Add(1)
		return p
	}
	keepLease := false
	defer func() {
		if !keepLease {
			p.Release()
		}
	}()
	select {
	case s.admission <- struct{}{}:
		defer func() { <-s.admission }()
	default:
		p.Status = "capture_busy"
		s.dropped.Add(1)
		return p
	}
	payload := SnapshotWithSession(body, reason, sessionID)
	if len(upstreamStatus) > 0 {
		payload.UpstreamHTTPStatus = upstreamStatus[0]
	}
	p.Truncated = payload.Truncated
	p.OriginalBytes = payload.OriginalBytes
	p.RedactionVersion = payload.RedactionVersion
	if payload.Input == "" {
		p.Status = payload.Omission
		return p
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		p.Status = "capture_failed"
		return p
	}
	ciphertext, err := s.cipher.Encrypt(string(raw))
	if err != nil || ciphertext == "" {
		p.Status = "encryption_failed"
		return p
	}
	p.Ciphertext = ciphertext
	p.Status = "pending"
	keepLease = true
	return p
}
func (s *Service) setStatus(id int64, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.store.Status(ctx, id, status) != nil {
		s.statusFailed.Add(1)
	}
}

// Enqueue receives only a redacted, encrypted payload; raw request bodies never
// enter the queue. The parent moderation row already exposes pending status.
func (s *Service) Enqueue(id int64, p Prepared) {
	defer p.Release()
	if id <= 0 {
		return
	}
	if p.Status != "pending" {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		s.dropped.Add(1)
		s.setStatus(id, "interrupted")
		return
	}
	select {
	case s.queue <- job{id, p}:
		s.queued.Add(1)
	default:
		s.dropped.Add(1)
		s.setStatus(id, "queue_full")
	}
}
func (s *Service) write(j job) {
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := s.store.Save(ctx, j.id, j.p, s.Config().RetentionDays)
		cancel()
		if err == nil {
			s.saved.Add(1)
			return
		}
		if errors.Is(err, ErrNotFound) {
			s.failed.Add(1)
			return
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-s.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				s.failed.Add(1)
				s.setStatus(j.id, "interrupted")
				return
			}
		}
	}
	s.failed.Add(1)
	s.setStatus(j.id, "storage_failed")
}

// markInterrupted records that a queued payload was not persisted. All status
// writes during one shutdown share a deadline so an unavailable database cannot
// stall process exit for the size of the queue.
func (s *Service) markInterrupted(id int64, deadline time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if s.store.Status(ctx, id, "interrupted") != nil {
		s.statusFailed.Add(1)
	}
}
func (s *Service) run() {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var interruptDeadline time.Time
			for j := range s.queue {
				select {
				case <-s.stop:
					if interruptDeadline.IsZero() {
						interruptDeadline = time.Now().Add(time.Second)
					}
					s.dropped.Add(1)
					s.markInterrupted(j.id, interruptDeadline)
					continue
				default:
				}
				s.write(j)
			}
		}()
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			wg.Wait()
			return
		case <-ticker.C:
			s.refresh()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := s.store.Cleanup(ctx, s.Config().RetentionDays)
			cancel()
			if err != nil {
				s.cleanupFailed.Add(1)
			}
		}
	}
}
func (s *Service) Close() {
	s.once.Do(func() { s.mu.Lock(); s.closed = true; close(s.queue); close(s.stop); s.mu.Unlock() })
	<-s.done
}
func (s *Service) Stats() Stats {
	return Stats{Queued: s.queued.Load(), Saved: s.saved.Load(), Failed: s.failed.Load(), Dropped: s.dropped.Load(), StatusWriteFailed: s.statusFailed.Load(), CleanupFailed: s.cleanupFailed.Load(), ConfigLoadFailed: s.configFailed.Load(), EncryptionReady: s.ready, QueueDepth: len(s.queue), QueueCapacity: cap(s.queue)}
}
func (s *Service) Get(ctx context.Context, id int64) (Record, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Status != "saved" {
		return r, nil
	}
	if !s.ready {
		return Record{}, ErrUnavailable
	}
	raw, err := s.cipher.Decrypt(r.Ciphertext)
	if err != nil {
		return Record{}, ErrUnavailable
	}
	var p Payload
	if json.Unmarshal([]byte(raw), &p) != nil {
		return Record{}, ErrUnavailable
	}
	r.Ciphertext = ""
	r.Payload = &p
	return r, nil
}
func (s *Service) Delete(ctx context.Context, id int64) error { return s.store.Delete(ctx, id) }
