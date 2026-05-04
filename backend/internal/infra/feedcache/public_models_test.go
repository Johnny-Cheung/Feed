package feedcache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

type videoStatsRedisError string

func (e videoStatsRedisError) Error() string {
	return string(e)
}

func (videoStatsRedisError) RedisError() {}

type loadVideoStatsHook struct {
	t               *testing.T
	videoIDs        []uint64
	values          []interface{}
	evalErr         error
	useEvalFallback bool

	commandNames  []string
	pipelineCalls int
}

func (h *loadVideoStatsHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *loadVideoStatsHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.commandNames = append(h.commandNames, cmd.Name())

		switch cmd.Name() {
		case "evalsha":
			if h.useEvalFallback {
				return videoStatsRedisError("NOSCRIPT No matching script. Please use EVAL.")
			}
			return h.runLoadVideoStatsScript(cmd, false)
		case "eval":
			return h.runLoadVideoStatsScript(cmd, true)
		default:
			return fmt.Errorf("unexpected redis command %q", cmd.Name())
		}
	}
}

func (h *loadVideoStatsHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelineCalls++
		return fmt.Errorf("unexpected redis pipeline with %d commands", len(cmds))
	}
}

func (h *loadVideoStatsHook) runLoadVideoStatsScript(cmd redis.Cmder, hasScriptSource bool) error {
	h.t.Helper()
	if h.evalErr != nil {
		return h.evalErr
	}

	args := cmd.Args()
	if len(args) < 3 {
		h.t.Fatalf("script command has too few args: %#v", args)
	}
	if hasScriptSource && fmt.Sprint(args[1]) != loadVideoStatsScriptSource {
		h.t.Fatalf("script source mismatch")
	}

	numKeys, err := redisTestArgInt(args[2])
	if err != nil {
		h.t.Fatalf("invalid key count %v: %v", args[2], err)
	}
	if len(args) != 3+int(numKeys)*2 {
		h.t.Fatalf("unexpected script arg count: %#v", args)
	}

	keys := make([]string, 0, numKeys)
	keyStart := 3
	argStart := keyStart + int(numKeys)
	for _, arg := range args[keyStart:argStart] {
		keys = append(keys, fmt.Sprint(arg))
	}

	expectedKeys := make([]string, 0, len(h.videoIDs))
	expectedArgs := make([]string, 0, len(h.videoIDs))
	for _, videoID := range h.videoIDs {
		expectedKeys = append(expectedKeys, videoStatsKey(videoID))
		expectedArgs = append(expectedArgs, strconv.FormatUint(videoID, 10))
	}
	if !reflect.DeepEqual(keys, expectedKeys) {
		h.t.Fatalf("script keys mismatch: got %#v want %#v", keys, expectedKeys)
	}

	actualArgs := make([]string, 0, numKeys)
	for _, arg := range args[argStart:] {
		actualArgs = append(actualArgs, fmt.Sprint(arg))
	}
	if !reflect.DeepEqual(actualArgs, expectedArgs) {
		h.t.Fatalf("script video id args mismatch: got %#v want %#v", actualArgs, expectedArgs)
	}

	redisCmd, ok := cmd.(*redis.Cmd)
	if !ok {
		h.t.Fatalf("script command type = %T, want *redis.Cmd", cmd)
	}
	redisCmd.SetVal(h.values)
	return nil
}

func redisTestArgInt(value interface{}) (int64, error) {
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

func newLoadVideoStatsTestCache(t *testing.T, hook *loadVideoStatsHook) *Cache {
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

func TestLoadVideoStatsByVideoIDsEmpty(t *testing.T) {
	hook := &loadVideoStatsHook{
		t:        t,
		videoIDs: nil,
		values:   nil,
	}
	cache := newLoadVideoStatsTestCache(t, hook)

	loaded, missing, err := cache.LoadVideoStatsByVideoIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("LoadVideoStatsByVideoIDs returned error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded = %#v, want empty", loaded)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %#v, want empty", missing)
	}
	if len(hook.commandNames) != 0 {
		t.Fatalf("empty input sent redis commands: %#v", hook.commandNames)
	}
	if hook.pipelineCalls != 0 {
		t.Fatalf("empty input used redis pipeline %d times", hook.pipelineCalls)
	}
}

func TestLoadVideoStatsByVideoIDsAllHit(t *testing.T) {
	hook := &loadVideoStatsHook{
		t:               t,
		videoIDs:        []uint64{1, 2},
		useEvalFallback: true,
		values: []interface{}{
			"1", "10", "2", "3", "4.5",
			"2", int64(11), int64(4), int64(5), "6.25",
		},
	}
	cache := newLoadVideoStatsTestCache(t, hook)

	loaded, missing, err := cache.LoadVideoStatsByVideoIDs(context.Background(), []uint64{1, 0, 2})
	if err != nil {
		t.Fatalf("LoadVideoStatsByVideoIDs returned error: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %#v, want empty", missing)
	}

	want := map[uint64]*VideoStats{
		1: {VideoID: 1, LikeCount: 10, CommentCount: 2, FavoriteCount: 3, HotScore: 4.5},
		2: {VideoID: 2, LikeCount: 11, CommentCount: 4, FavoriteCount: 5, HotScore: 6.25},
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("loaded = %#v, want %#v", loaded, want)
	}
	if !reflect.DeepEqual(hook.commandNames, []string{"evalsha", "eval"}) {
		t.Fatalf("redis commands = %#v, want evalsha fallback to eval", hook.commandNames)
	}
	if hook.pipelineCalls != 0 {
		t.Fatalf("used redis pipeline %d times", hook.pipelineCalls)
	}
}

func TestLoadVideoStatsByVideoIDsPartialMissing(t *testing.T) {
	hook := &loadVideoStatsHook{
		t:               t,
		videoIDs:        []uint64{1, 2, 3},
		useEvalFallback: true,
		values: []interface{}{
			"1", "10", "2", "3", "4.5",
			nil, nil, nil, nil, nil,
			"3", "12", "6", "7", "8.5",
		},
	}
	cache := newLoadVideoStatsTestCache(t, hook)

	loaded, missing, err := cache.LoadVideoStatsByVideoIDs(context.Background(), []uint64{1, 2, 3})
	if err != nil {
		t.Fatalf("LoadVideoStatsByVideoIDs returned error: %v", err)
	}
	if !reflect.DeepEqual(missing, []uint64{2}) {
		t.Fatalf("missing = %#v, want [2]", missing)
	}
	if _, ok := loaded[2]; ok {
		t.Fatalf("missing video was loaded: %#v", loaded[2])
	}
	if loaded[1] == nil || loaded[3] == nil {
		t.Fatalf("expected videos 1 and 3 loaded: %#v", loaded)
	}
}

func TestLoadVideoStatsByVideoIDsMismatchedVideoIDIsMissing(t *testing.T) {
	hook := &loadVideoStatsHook{
		t:               t,
		videoIDs:        []uint64{2},
		useEvalFallback: true,
		values:          []interface{}{"99", "10", "2", "3", "4.5"},
	}
	cache := newLoadVideoStatsTestCache(t, hook)

	loaded, missing, err := cache.LoadVideoStatsByVideoIDs(context.Background(), []uint64{2})
	if err != nil {
		t.Fatalf("LoadVideoStatsByVideoIDs returned error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded = %#v, want empty", loaded)
	}
	if !reflect.DeepEqual(missing, []uint64{2}) {
		t.Fatalf("missing = %#v, want [2]", missing)
	}
}

func TestLoadVideoStatsByVideoIDsReturnsRedisScriptError(t *testing.T) {
	scriptErr := errors.New("script failed")
	hook := &loadVideoStatsHook{
		t:        t,
		videoIDs: []uint64{1},
		evalErr:  scriptErr,
	}
	cache := newLoadVideoStatsTestCache(t, hook)

	loaded, missing, err := cache.LoadVideoStatsByVideoIDs(context.Background(), []uint64{1})
	if !errors.Is(err, scriptErr) {
		t.Fatalf("LoadVideoStatsByVideoIDs error = %v, want wrapped script error", err)
	}
	if err == nil || !strings.Contains(err.Error(), "load video stats cache") {
		t.Fatalf("LoadVideoStatsByVideoIDs error should include operation context: %v", err)
	}
	if loaded != nil || missing != nil {
		t.Fatalf("loaded/missing = %#v/%#v, want nil/nil on script error", loaded, missing)
	}
}
