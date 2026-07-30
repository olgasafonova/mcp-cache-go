# mcp-cache-go

[![CI](https://github.com/olgasafonova/mcp-cache-go/actions/workflows/ci.yml/badge.svg)](https://github.com/olgasafonova/mcp-cache-go/actions/workflows/ci.yml)

Receiving middleware that stamps the SEP-2549 `ttlMs` cache hint onto MCP results,
for Go MCP servers built on the official [go-sdk](https://github.com/modelcontextprotocol/go-sdk).

## Why this exists

The MCP `2026-07-28` revision added `ttlMs` and `cacheScope` to cacheable results.
The SDK sets `cacheScope` to `"public"` and leaves `ttlMs` at **0**, which the spec
defines as *immediately stale*. A compliant client therefore re-fetches the tool
list on every turn, and there is no `ServerOptions` knob to change it.

Stamping it yourself is more awkward than it looks. `mcp.Cacheable` exposes
`GetTTLMs()` and `GetCacheScope()` on a value receiver, the `CacheableResult`
interface is getters-only, and there is no setter. Reaching the field means a type
switch over every concrete result type that embeds `Cacheable` — six of them as of
go-sdk v1.7.0. This module owns that switch so it lives in one place instead of
being copied into every server.

## Install

```bash
go get github.com/olgasafonova/mcp-cache-go
```

## Usage

```go
import (
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/olgasafonova/mcp-cache-go/mcpcache"
)

server := mcp.NewServer(impl, opts)

server.AddReceivingMiddleware(mcpcache.Middleware(mcpcache.Config{
    TTLs: map[string]time.Duration{
        mcpcache.MethodListTools: time.Hour,
        mcpcache.MethodDiscover:  time.Hour,
    },
}))
```

## Configuration

| Field | Effect |
|---|---|
| `TTLs` | Per-method TTL, keyed by the `Method*` constants. A non-positive value opts that method out of `Default`. |
| `Default` | Applies to any cacheable result with no `TTLs` entry. Zero leaves results untouched. |
| `Scope` | Overrides `cacheScope`. Empty keeps the SDK's `"public"`. |
| `Overwrite` | When true, replaces a non-zero `ttlMs` a handler already set. Default false, so the handler wins. |

A zero `Config` is a no-op.

### Choosing TTLs

A server's tool list changes only when a release ships, so `tools/list` and
`server/discover` tolerate long TTLs. Pick the rest by measured change rate.

Two cautions:

- **`resources/read` returns live content**, not a slow-changing list. Prefer
  leaving `Default` at zero and enumerating methods, rather than setting a blanket
  default that catches it.
- **`Scope` defaults to `public`**, which permits intermediary caching. Set
  `ScopePrivate` on any server whose results are scoped to the requesting identity.

## Covered methods

Every result type embedding `mcp.Cacheable` in go-sdk v1.7.0:

| Constant | Method |
|---|---|
| `MethodDiscover` | `server/discover` |
| `MethodListTools` | `tools/list` |
| `MethodListPrompts` | `prompts/list` |
| `MethodListResources` | `resources/list` |
| `MethodListResourceTemplates` | `resources/templates/list` |
| `MethodReadResource` | `resources/read` |

`TestMiddlewareStampsEveryCacheableResultType` is the guard: it fails if the SDK
adds a seventh cacheable result and the type switch is not extended.

## Ordering

The middleware runs after the SDK's own `setDefaultCacheableValues`, so `Config`
wins on `ttlMs`. Results carrying no cache hint, and errored calls, pass through
untouched.

When composing with other receiving middleware, order matters: this one mutates
the result on the way out, so observability middleware should see the stamped
value. Register it before the observer.

## Relation to mcp-otel-go

Sibling module, same shape and same `Middleware(Config) mcp.Middleware`
constructor. Two deliberate differences in CI: this repo carries the
`gleif-mcp-server` supply-chain steps (`go mod verify`, a `go.sum` drift check,
`govulncheck`) because every server imports it, and it pins golangci-lint to
`latest` rather than a version, after a pinned v2.7.2 produced six `SA5011` false
positives elsewhere in the portfolio. It has no codecov upload.

## License

MIT
