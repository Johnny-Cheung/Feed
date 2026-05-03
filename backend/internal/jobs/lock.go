package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var releaseLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`)

type redisLock struct {
	client *redis.Client
	key    string
	token  string
}

func acquireRedisLock(ctx context.Context, client *redis.Client, key string, ttl time.Duration) (*redisLock, bool, error) {
	if client == nil {
		return &redisLock{}, true, nil
	}
	if ttl <= 0 {
		return nil, false, fmt.Errorf("lock ttl must be positive")
	}

	token, err := newLockToken()
	if err != nil {
		return nil, false, err
	}

	acquired, err := client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("acquire redis lock %s: %w", key, err)
	}
	if !acquired {
		return nil, false, nil
	}

	return &redisLock{client: client, key: key, token: token}, true, nil
}

func (l *redisLock) Release(ctx context.Context) error {
	if l == nil || l.client == nil {
		return nil
	}

	if _, err := releaseLockScript.Run(ctx, l.client, []string{l.key}, l.token).Result(); err != nil {
		return fmt.Errorf("release redis lock %s: %w", l.key, err)
	}
	return nil
}

func newLockToken() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate lock token: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}
