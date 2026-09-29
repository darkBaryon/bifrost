# Offline HTTP route registrar probe

This standalone test module uses no repository packages, listener, service, or network download. It was run on Go 1.27.1 (darwin/arm64).

The tested router and HTTP implementation are the local cached sources of github.com/fasthttp/router v1.5.4 and github.com/valyala/fasthttp v1.71.0. Because their originally required transitive versions are not all present locally, go.mod explicitly replaces transitive modules with cached sources: brotli v1.2.2, compress v1.18.6, gotils v0.0.0-20240704082632-aef3928b8a38, x/net v0.58.0, x/crypto v0.55.0, x/sys v0.47.0, and x/text v0.41.0. bytebufferpool v1.0.0 is read directly from the existing module cache (that legacy source directory has no go.mod).

Run from this directory:

```sh
GOWORK=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOCACHE=/private/tmp/bifrost-route-registrar-probe.qn3MJS/go-build go test -mod=mod -v .
```

The successful output is preserved verbatim in result.txt.

Validated:
- A real Router satisfies the seven-method registration interface at compile time.
- Every decorator shortcut forwards explicitly to the decorator's Handle.
- Registration-time policy closures cover directly registered handlers and both optional route aliases.
- Unclassified routes cause explicit startup validation failure and are not installed.
- GET SPA wildcard and GET NotFound fallback differ for unknown API paths and method handling, while preserving a known API's 405 and a UI deep link.
- Lookup takes a request path, not a registration template identity: parameter/catchall and regex counterexamples are reproduced.

Limits:
- Credential checks and the SPA handler are deliberate stubs, not production EE authentication or UI.
- The reserved namespace list is illustrative, not a complete gateway surface policy.
- RequestCtx handlers are invoked in memory; there is no socket, server, or upstream Bootstrap.
- This does not validate production route inventory, middleware/tracing order, dynamic registration, conditional modules, static files, encoded paths, streaming, MCP, CORS, or WebSocket lifecycle.
- This is an architectural feasibility probe, not a production regression run.

## Archived evidence

This directory is an evidence snapshot, not a new production Go module. Copy `registrar_test.go.txt` and `go.mod.txt` to a temporary directory as `registrar_test.go` and `go.mod` to reproduce with the recorded local dependencies. The original absolute module-cache replacements describe the environment actually tested. The `.txt` suffix prevents accidental inclusion in repository builds.
