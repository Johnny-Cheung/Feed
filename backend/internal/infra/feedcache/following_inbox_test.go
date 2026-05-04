package feedcache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type markUserActiveRedisError string

func (e markUserActiveRedisError) Error() string {
	return string(e)
}

func (markUserActiveRedisError) RedisError() {}

type markUserActiveHook struct {
	t                *testing.T
	userID           uint64
	activeTTLSeconds int64
	evalErr          error
	useEvalFallback  bool

	commandNames  []string
	pipelineCalls int
	refreshes     int
	refreshedKeys []string
}

func (h *markUserActiveHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *markUserActiveHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.commandNames = append(h.commandNames, cmd.Name())

		switch cmd.Name() {
		case "evalsha":
			if h.useEvalFallback {
				return markUserActiveRedisError("NOSCRIPT No matching script. Please use EVAL.")
			}
			return h.runMarkUserActiveScript(cmd, false)
		case "eval":
			return h.runMarkUserActiveScript(cmd, true)
		default:
			return fmt.Errorf("unexpected redis command %q", cmd.Name())
		}
	}
}

func (h *markUserActiveHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelineCalls++
		return fmt.Errorf("unexpected redis pipeline with %d commands", len(cmds))
	}
}

func (h *markUserActiveHook) runMarkUserActiveScript(cmd redis.Cmder, hasScriptSource bool) error {
	h.t.Helper()
	if h.evalErr != nil {
		return h.evalErr
	}

	args := cmd.Args()
	if len(args) < 5 {
		h.t.Fatalf("script command has too few args: %#v", args)
	}
	if hasScriptSource && fmt.Sprint(args[1]) != markUserActiveScriptSource {
		h.t.Fatalf("script source mismatch")
	}

	numKeys, err := redisArgInt(args[2])
	if err != nil {
		h.t.Fatalf("invalid key count %v: %v", args[2], err)
	}
	keyStart := 3
	argStart := keyStart + int(numKeys)
	if len(args) != argStart+2 {
		h.t.Fatalf("unexpected script arg count: %#v", args)
	}

	keys := make([]string, 0, numKeys)
	for _, arg := range args[keyStart:argStart] {
		keys = append(keys, fmt.Sprint(arg))
	}

	expectedKeys := []string{
		userActiveKey(h.userID),
		followingInboxKey(h.userID),
	}
	expectedKeys = append(expectedKeys, userRelationTTLKeys(h.userID)...)
	if !reflect.DeepEqual(keys, expectedKeys) {
		h.t.Fatalf("script keys mismatch: got %#v want %#v", keys, expectedKeys)
	}

	ttlSeconds, err := redisArgInt(args[argStart])
	if err != nil {
		h.t.Fatalf("invalid ttl arg %v: %v", args[argStart], err)
	}
	if ttlSeconds != int64(FollowingActiveTTL/time.Second) {
		h.t.Fatalf("ttl arg mismatch: got %d want %d", ttlSeconds, int64(FollowingActiveTTL/time.Second))
	}

	thresholdSeconds, err := redisArgInt(args[argStart+1])
	if err != nil {
		h.t.Fatalf("invalid threshold arg %v: %v", args[argStart+1], err)
	}
	if thresholdSeconds != int64(FollowingActiveRefreshThreshold/time.Second) {
		h.t.Fatalf("threshold arg mismatch: got %d want %d", thresholdSeconds, int64(FollowingActiveRefreshThreshold/time.Second))
	}

	redisCmd, ok := cmd.(*redis.Cmd)
	if !ok {
		h.t.Fatalf("script command type = %T, want *redis.Cmd", cmd)
	}
	if h.activeTTLSeconds > thresholdSeconds {
		redisCmd.SetVal(int64(0))
		return nil
	}

	h.refreshes++
	h.refreshedKeys = append([]string(nil), keys...)
	redisCmd.SetVal(int64(1))
	return nil
}

func redisArgInt(value interface{}) (int64, error) {
	switch current := value.(type) {
	case int:
		return int64(current), nil
	case int64:
		return current, nil
	case string:
		return strconv.ParseInt(current, 10, 64)
	default:
		return strconv.ParseInt(fmt.Sprint(current), 10, 64)
	}
}

func newMarkUserActiveTestCache(t *testing.T, hook *markUserActiveHook) *Cache {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr:       "127.0.0.1:0",
		MaxRetries: -1,
	})
	client.AddHook(hook)
	t.Cleanup(func() {
		_ = client.Close()
	})

	return NewCache(nil, client)
}

func TestMarkUserActiveNoop(t *testing.T) {
	ctx := context.Background()

	var nilCache *Cache
	if err := nilCache.MarkUserActive(ctx, 1); err != nil {
		t.Fatalf("nil cache MarkUserActive returned error: %v", err)
	}
	if err := (&Cache{}).MarkUserActive(ctx, 1); err != nil {
		t.Fatalf("nil redis MarkUserActive returned error: %v", err)
	}

	hook := &markUserActiveHook{
		t:                t,
		userID:           42,
		activeTTLSeconds: -2,
		useEvalFallback:  true,
	}
	cache := newMarkUserActiveTestCache(t, hook)
	if err := cache.MarkUserActive(ctx, 0); err != nil {
		t.Fatalf("userID=0 MarkUserActive returned error: %v", err)
	}
	if len(hook.commandNames) != 0 {
		t.Fatalf("userID=0 sent redis commands: %#v", hook.commandNames)
	}
	if hook.pipelineCalls != 0 {
		t.Fatalf("userID=0 used redis pipeline %d times", hook.pipelineCalls)
	}
}

func TestMarkUserActiveSkipsRefreshWhenActiveTTLAboveThreshold(t *testing.T) {
	hook := &markUserActiveHook{
		t:                t,
		userID:           42,
		activeTTLSeconds: int64(FollowingActiveRefreshThreshold/time.Second) + 1,
		useEvalFallback:  true,
	}
	cache := newMarkUserActiveTestCache(t, hook)

	if err := cache.MarkUserActive(context.Background(), 42); err != nil {
		t.Fatalf("MarkUserActive returned error: %v", err)
	}

	if !reflect.DeepEqual(hook.commandNames, []string{"evalsha", "eval"}) {
		t.Fatalf("redis commands = %#v, want evalsha fallback to eval", hook.commandNames)
	}
	if hook.refreshes != 0 {
		t.Fatalf("refreshed keys despite sufficient TTL: %#v", hook.refreshedKeys)
	}
	if hook.pipelineCalls != 0 {
		t.Fatalf("used redis pipeline %d times", hook.pipelineCalls)
	}
}

func TestMarkUserActiveRefreshesWhenTTLIsMissingOrBelowThreshold(t *testing.T) {
	thresholdSeconds := int64(FollowingActiveRefreshThreshold / time.Second)

	tests := []struct {
		name string
		ttl  int64
	}{
		{name: "missing", ttl: -2},
		{name: "without_expire", ttl: -1},
		{name: "below_threshold", ttl: thresholdSeconds - 1},
		{name: "at_threshold", ttl: thresholdSeconds},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := &markUserActiveHook{
				t:                t,
				userID:           42,
				activeTTLSeconds: tt.ttl,
				useEvalFallback:  true,
			}
			cache := newMarkUserActiveTestCache(t, hook)

			if err := cache.MarkUserActive(context.Background(), 42); err != nil {
				t.Fatalf("MarkUserActive returned error: %v", err)
			}

			if hook.refreshes != 1 {
				t.Fatalf("refresh count = %d, want 1", hook.refreshes)
			}
			expectedKeys := []string{
				userActiveKey(42),
				followingInboxKey(42),
			}
			expectedKeys = append(expectedKeys, userRelationTTLKeys(42)...)
			if !reflect.DeepEqual(hook.refreshedKeys, expectedKeys) {
				t.Fatalf("refreshed keys = %#v, want %#v", hook.refreshedKeys, expectedKeys)
			}
			if hook.pipelineCalls != 0 {
				t.Fatalf("used redis pipeline %d times", hook.pipelineCalls)
			}
		})
	}
}

func TestMarkUserActiveReturnsRedisScriptError(t *testing.T) {
	scriptErr := errors.New("script failed")
	hook := &markUserActiveHook{
		t:       t,
		userID:  42,
		evalErr: scriptErr,
	}
	cache := newMarkUserActiveTestCache(t, hook)

	err := cache.MarkUserActive(context.Background(), 42)
	if !errors.Is(err, scriptErr) {
		t.Fatalf("MarkUserActive error = %v, want wrapped script error", err)
	}
	if err == nil || !strings.Contains(err.Error(), "mark user active") {
		t.Fatalf("MarkUserActive error should include operation context: %v", err)
	}
}
