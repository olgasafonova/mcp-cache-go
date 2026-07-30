// Package mcpcache provides receiving middleware that stamps the SEP-2549
// ttlMs cache hint onto MCP results for Go MCP servers built on the official
// go-sdk.
//
// The MCP 2026-07-28 revision added ttlMs and cacheScope to cacheable results.
// The SDK sets cacheScope to "public" but leaves ttlMs at 0, which the spec
// defines as "immediately stale", so a compliant client re-fetches the tool list
// on every turn. There is no ServerOptions knob for TTL, so a server that wants
// the caching win has to stamp it in middleware.
package mcpcache

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP method names whose results carry a cache hint. The go-sdk keeps its own
// copies of these strings unexported, so they are re-declared here for callers
// building a Config.
const (
	MethodDiscover              = "server/discover"
	MethodListTools             = "tools/list"
	MethodListPrompts           = "prompts/list"
	MethodListResources         = "resources/list"
	MethodListResourceTemplates = "resources/templates/list"
	MethodReadResource          = "resources/read"
)

// Cache scope values, analogous to HTTP Cache-Control public vs private.
const (
	ScopePublic  = "public"
	ScopePrivate = "private"
)

// Config controls which results receive a TTL hint and how long it is.
type Config struct {
	// TTLs maps an MCP method name to the TTL advertised for that method's
	// result. Use the Method* constants as keys. A method absent from the map
	// falls back to Default. A non-positive value disables stamping for that
	// method, which is how you opt one method out of a Default.
	TTLs map[string]time.Duration

	// Default applies to any cacheable result whose method has no TTLs entry.
	// Zero, the zero value, leaves such results untouched at the SDK's ttlMs:0.
	//
	// Prefer leaving this zero and listing methods explicitly. A blanket
	// default also covers resources/read, which returns live content rather
	// than a slow-changing list.
	Default time.Duration

	// Scope overrides cacheScope on every cacheable result. Empty leaves the
	// SDK default of "public". Use ScopePrivate for a server whose results are
	// scoped to the requesting user.
	Scope string

	// Overwrite makes the middleware replace a non-zero ttlMs that the handler
	// already set. False, the zero value, means a handler that stamped its own
	// TTL wins over this Config.
	Overwrite bool
}

// resolved is the immutable, pre-computed form of a Config. Snapshotting at
// construction means a caller mutating their Config map afterwards cannot race
// with in-flight requests.
type resolved struct {
	ttls      map[string]time.Duration
	def       time.Duration
	scope     string
	overwrite bool
}

func resolve(cfg Config) resolved {
	r := resolved{
		def:       cfg.Default,
		scope:     cfg.Scope,
		overwrite: cfg.Overwrite,
	}
	if len(cfg.TTLs) > 0 {
		r.ttls = make(map[string]time.Duration, len(cfg.TTLs))
		for method, ttl := range cfg.TTLs {
			r.ttls[method] = ttl
		}
	}
	return r
}

// ttlFor reports the TTL to advertise for method, and whether to stamp at all.
func (r resolved) ttlFor(method string) (time.Duration, bool) {
	if ttl, ok := r.ttls[method]; ok {
		// An explicit non-positive entry opts this method out of Default.
		return ttl, ttl > 0
	}
	return r.def, r.def > 0
}

// cacheableOf returns a pointer to the embedded Cacheable of any result type
// that carries one, or nil for results that do not.
//
// The go-sdk exposes GetTTLMs and GetCacheScope on a value receiver and offers
// no setter and no settable interface, so reaching the field requires a type
// switch over the concrete result types. Keeping that switch in one place is
// the reason this package exists: six cases copied into every server would
// drift the first time the SDK adds a seventh cacheable result.
func cacheableOf(res mcp.Result) *mcp.Cacheable {
	switch r := res.(type) {
	case *mcp.DiscoverResult:
		return &r.Cacheable
	case *mcp.ListToolsResult:
		return &r.Cacheable
	case *mcp.ListPromptsResult:
		return &r.Cacheable
	case *mcp.ListResourcesResult:
		return &r.Cacheable
	case *mcp.ListResourceTemplatesResult:
		return &r.Cacheable
	case *mcp.ReadResourceResult:
		return &r.Cacheable
	default:
		return nil
	}
}

// Middleware returns receiving middleware that stamps ttlMs, and optionally
// cacheScope, on cacheable results.
//
// It runs after the SDK's own setDefaultCacheableValues, so the Config wins on
// ttlMs. Results that carry no cache hint, and errored calls, pass through
// untouched.
//
// Usage:
//
//	server := mcp.NewServer(impl, opts)
//	server.AddReceivingMiddleware(mcpcache.Middleware(mcpcache.Config{
//	    TTLs: map[string]time.Duration{
//	        mcpcache.MethodListTools: time.Hour,
//	        mcpcache.MethodDiscover:  time.Hour,
//	    },
//	}))
func Middleware(cfg Config) mcp.Middleware {
	r := resolve(cfg)

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || res == nil {
				return res, err
			}

			ttl, stampTTL := r.ttlFor(method)
			if !stampTTL && r.scope == "" {
				return res, nil
			}

			c := cacheableOf(res)
			if c == nil {
				return res, nil
			}

			if stampTTL && (r.overwrite || c.TTLMs == 0) {
				c.TTLMs = int(ttl.Milliseconds())
			}
			if r.scope != "" {
				c.CacheScope = r.scope
			}

			return res, nil
		}
	}
}
