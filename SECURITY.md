# Security Policy

## Reporting

Report vulnerabilities through GitHub's private vulnerability reporting on this
repository. Please do not open a public issue for a security problem.

## Scope

This module stamps the SEP-2549 `ttlMs` and `cacheScope` cache hints onto MCP
results. It makes no network calls, reads no files, and holds no credentials.

The security-relevant surface is small but real: a TTL is an instruction to a
client to **stop re-fetching**. A wrongly long TTL on a result whose contents are
access-controlled, or which vary per user, can cause a client to serve one user's
cached result to another. Two guards matter:

- Set `Scope: ScopePrivate` on any server whose results are scoped to the
  requesting identity. The MCP default is `public`, which permits intermediary
  caching.
- Leave `Default` at zero and enumerate methods explicitly. A blanket default
  also covers `resources/read`, which returns live content rather than a
  slow-changing list.
