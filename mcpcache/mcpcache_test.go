package mcpcache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/mcp-cache-go/mcpcache"
)

// handlerReturning builds a MethodHandler that returns a fixed result and error.
func handlerReturning(res mcp.Result, err error) mcp.MethodHandler {
	return func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return res, err
	}
}

// run applies the middleware around a handler returning res, and hands back the
// result the client would see.
func run(t *testing.T, cfg mcpcache.Config, method string, res mcp.Result) mcp.Result {
	t.Helper()
	got, err := mcpcache.Middleware(cfg)(handlerReturning(res, nil))(context.Background(), method, nil)
	if err != nil {
		t.Fatalf("middleware returned unexpected error: %v", err)
	}
	return got
}

// ttlOf reads ttlMs back off a result through the SDK's own accessor, so the
// test asserts on the value a client would actually observe.
func ttlOf(t *testing.T, res mcp.Result) int {
	t.Helper()
	c, ok := res.(mcp.CacheableResult)
	if !ok {
		t.Fatalf("result %T does not implement mcp.CacheableResult", res)
	}
	return c.GetTTLMs()
}

// Every result type that embeds mcp.Cacheable must get stamped. This is the
// test that fails when the SDK adds a seventh cacheable type and the type
// switch in cacheableOf is not extended.
func TestMiddlewareStampsEveryCacheableResultType(t *testing.T) {
	cases := []struct {
		method string
		result mcp.Result
	}{
		{mcpcache.MethodDiscover, &mcp.DiscoverResult{}},
		{mcpcache.MethodListTools, &mcp.ListToolsResult{}},
		{mcpcache.MethodListPrompts, &mcp.ListPromptsResult{}},
		{mcpcache.MethodListResources, &mcp.ListResourcesResult{}},
		{mcpcache.MethodListResourceTemplates, &mcp.ListResourceTemplatesResult{}},
		{mcpcache.MethodReadResource, &mcp.ReadResourceResult{}},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			cfg := mcpcache.Config{
				TTLs: map[string]time.Duration{tc.method: 90 * time.Second},
			}
			got := run(t, cfg, tc.method, tc.result)
			if want := 90_000; ttlOf(t, got) != want {
				t.Errorf("ttlMs = %d, want %d", ttlOf(t, got), want)
			}
		})
	}
}

// A result with no cache hint must pass through untouched rather than panic.
func TestMiddlewareIgnoresNonCacheableResult(t *testing.T) {
	in := &mcp.CallToolResult{}
	cfg := mcpcache.Config{Default: time.Hour, Scope: mcpcache.ScopePrivate}

	got := run(t, cfg, "tools/call", in)

	if got != mcp.Result(in) {
		t.Errorf("result was replaced: got %#v, want the original pointer", got)
	}
}

func TestMiddlewarePassesErrorsThroughWithoutStamping(t *testing.T) {
	sentinel := errors.New("handler failed")
	res := &mcp.ListToolsResult{}
	cfg := mcpcache.Config{Default: time.Hour}

	_, err := mcpcache.Middleware(cfg)(handlerReturning(res, sentinel))(
		context.Background(), mcpcache.MethodListTools, nil)

	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if res.TTLMs != 0 {
		t.Errorf("ttlMs = %d on an errored call, want 0 (untouched)", res.TTLMs)
	}
}

func TestMiddlewareHandlesNilResult(t *testing.T) {
	cfg := mcpcache.Config{Default: time.Hour}

	got, err := mcpcache.Middleware(cfg)(handlerReturning(nil, nil))(
		context.Background(), mcpcache.MethodListTools, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("result = %#v, want nil", got)
	}
}

func TestDefaultAppliesWhenMethodHasNoEntry(t *testing.T) {
	cfg := mcpcache.Config{Default: 30 * time.Minute}

	got := run(t, cfg, mcpcache.MethodListTools, &mcp.ListToolsResult{})

	if want := 1_800_000; ttlOf(t, got) != want {
		t.Errorf("ttlMs = %d, want %d", ttlOf(t, got), want)
	}
}

// An explicit non-positive entry is how a caller opts one method out of a
// blanket Default. resources/read is the motivating case: it returns live
// content, not a slow-changing list.
func TestExplicitNonPositiveEntryOptsOutOfDefault(t *testing.T) {
	cfg := mcpcache.Config{
		Default: time.Hour,
		TTLs:    map[string]time.Duration{mcpcache.MethodReadResource: 0},
	}

	got := run(t, cfg, mcpcache.MethodReadResource, &mcp.ReadResourceResult{})

	if ttlOf(t, got) != 0 {
		t.Errorf("ttlMs = %d, want 0 (opted out)", ttlOf(t, got))
	}
}

func TestHandlerSetTTLWinsUnlessOverwrite(t *testing.T) {
	cfg := mcpcache.Config{Default: time.Hour}

	t.Run("preserved by default", func(t *testing.T) {
		in := &mcp.ListToolsResult{Cacheable: mcp.Cacheable{TTLMs: 5_000}}
		got := run(t, cfg, mcpcache.MethodListTools, in)
		if want := 5_000; ttlOf(t, got) != want {
			t.Errorf("ttlMs = %d, want %d (handler value preserved)", ttlOf(t, got), want)
		}
	})

	t.Run("replaced when Overwrite is set", func(t *testing.T) {
		overwriting := cfg
		overwriting.Overwrite = true
		in := &mcp.ListToolsResult{Cacheable: mcp.Cacheable{TTLMs: 5_000}}
		got := run(t, overwriting, mcpcache.MethodListTools, in)
		if want := 3_600_000; ttlOf(t, got) != want {
			t.Errorf("ttlMs = %d, want %d", ttlOf(t, got), want)
		}
	})
}

func TestScopeOverride(t *testing.T) {
	t.Run("empty Scope leaves the SDK value", func(t *testing.T) {
		in := &mcp.ListToolsResult{Cacheable: mcp.Cacheable{CacheScope: mcpcache.ScopePublic}}
		got := run(t, mcpcache.Config{Default: time.Hour}, mcpcache.MethodListTools, in)
		if scope := got.(mcp.CacheableResult).GetCacheScope(); scope != mcpcache.ScopePublic {
			t.Errorf("cacheScope = %q, want %q", scope, mcpcache.ScopePublic)
		}
	})

	// Scope alone, with no TTL configured, must still be applied.
	t.Run("applied with no TTL configured", func(t *testing.T) {
		cfg := mcpcache.Config{Scope: mcpcache.ScopePrivate}
		got := run(t, cfg, mcpcache.MethodListTools, &mcp.ListToolsResult{})
		if scope := got.(mcp.CacheableResult).GetCacheScope(); scope != mcpcache.ScopePrivate {
			t.Errorf("cacheScope = %q, want %q", scope, mcpcache.ScopePrivate)
		}
		if ttlOf(t, got) != 0 {
			t.Errorf("ttlMs = %d, want 0 (no TTL configured)", ttlOf(t, got))
		}
	})
}

// Config is caller-owned. Middleware snapshots it, so a later mutation of the
// caller's map must not affect in-flight requests.
func TestConfigIsSnapshotAtConstruction(t *testing.T) {
	ttls := map[string]time.Duration{mcpcache.MethodListTools: time.Minute}
	mw := mcpcache.Middleware(mcpcache.Config{TTLs: ttls})

	ttls[mcpcache.MethodListTools] = 99 * time.Hour
	delete(ttls, mcpcache.MethodListTools)

	got, err := mw(handlerReturning(&mcp.ListToolsResult{}, nil))(
		context.Background(), mcpcache.MethodListTools, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := 60_000; ttlOf(t, got) != want {
		t.Errorf("ttlMs = %d, want %d (snapshot of the original config)", ttlOf(t, got), want)
	}
}

// A zero Config must be a no-op rather than stamping zeros over the SDK's
// defaults.
func TestZeroConfigIsNoOp(t *testing.T) {
	in := &mcp.ListToolsResult{Cacheable: mcp.Cacheable{TTLMs: 7_000, CacheScope: mcpcache.ScopePublic}}

	got := run(t, mcpcache.Config{}, mcpcache.MethodListTools, in)

	if want := 7_000; ttlOf(t, got) != want {
		t.Errorf("ttlMs = %d, want %d (untouched)", ttlOf(t, got), want)
	}
	if scope := got.(mcp.CacheableResult).GetCacheScope(); scope != mcpcache.ScopePublic {
		t.Errorf("cacheScope = %q, want %q (untouched)", scope, mcpcache.ScopePublic)
	}
}
