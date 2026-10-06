// Package qualityqueue stores bounded, coalesced quality signals across replicas.
package qualityqueue

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const PendingKey = "quality:5xx:pending"

type Signal struct {
	AccountID  int64
	ObservedAt time.Time
}

type Queue interface {
	Enqueue(context.Context, int64, time.Time) error
	Score(context.Context, int64) (time.Time, error)
	Pending(context.Context, time.Time) ([]Signal, error)
	Acknowledge(context.Context, Signal) error
	Close() error
}

type RedisQueue struct{ client *redis.Client }

// NewRedis uses a separate small pool with bounded latency; shared is unmodified.
func NewRedis(shared *redis.Client) *RedisQueue {
	if shared == nil {
		return nil
	}
	opts := *shared.Options()
	opts.ContextTimeoutEnabled = true
	opts.ReadTimeout = 150 * time.Millisecond
	opts.WriteTimeout = 150 * time.Millisecond
	opts.DialTimeout = 150 * time.Millisecond
	opts.PoolTimeout = 150 * time.Millisecond
	opts.MaxRetries = -1
	opts.PoolSize = 4
	opts.MinIdleConns = 0
	return &RedisQueue{client: redis.NewClient(&opts)}
}

var enqueue = redis.NewScript(`
 local score = tonumber(ARGV[2])
 local prior = tonumber(redis.call('ZSCORE', KEYS[1], ARGV[1]))
 if prior and prior >= score then score = prior + 1 end
 redis.call('ZADD', KEYS[1], score, ARGV[1])
 redis.call('EXPIRE', KEYS[1], 1200)
 return 1`)

func (q *RedisQueue) Enqueue(ctx context.Context, id int64, at time.Time) error {
	return enqueue.Run(ctx, q.client, []string{PendingKey}, strconv.FormatInt(id, 10), at.UnixMilli()).Err()
}
func (q *RedisQueue) Score(ctx context.Context, id int64) (time.Time, error) {
	score, err := q.client.ZScore(ctx, PendingKey, strconv.FormatInt(id, 10)).Result()
	return time.UnixMilli(int64(score)), err
}
func (q *RedisQueue) Pending(ctx context.Context, before time.Time) ([]Signal, error) {
	if err := q.client.ZRemRangeByScore(ctx, PendingKey, "-inf", strconv.FormatInt(before.UnixMilli(), 10)).Err(); err != nil {
		return nil, err
	}
	entries, err := q.client.ZRangeWithScores(ctx, PendingKey, 0, 99).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Signal, 0, len(entries))
	for _, entry := range entries {
		member, ok := entry.Member.(string)
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, Signal{AccountID: id, ObservedAt: time.UnixMilli(int64(entry.Score))})
	}
	return out, nil
}
func (q *RedisQueue) Acknowledge(ctx context.Context, signal Signal) error {
	return q.client.Eval(ctx, `if tonumber(redis.call('ZSCORE',KEYS[1],ARGV[1])) == tonumber(ARGV[2]) then return redis.call('ZREM',KEYS[1],ARGV[1]) end return 0`, []string{PendingKey}, strconv.FormatInt(signal.AccountID, 10), signal.ObservedAt.UnixMilli()).Err()
}
func (q *RedisQueue) Close() error { return q.client.Close() }
