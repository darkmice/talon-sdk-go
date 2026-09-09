# talon-sdk-go

Go bindings for the [talon](https://github.com/darkmice/talon-sdk) embedded database engine.

## Why this repo

The upstream `talon-sdk` repository keeps Go bindings in a `go/` subdirectory and the
prebuilt native libraries in sibling `include/` and `lib/` directories. Because
`#cgo` directives use `${SRCDIR}/../include` and `${SRCDIR}/../lib`, the source
falls outside the Go module's import root, so `go get github.com/darkmice/talon-sdk/go`
downloads the Go files but not the headers and prebuilt libs.

This repo bundles everything inside one module so `go get` works end to end:

```
github.com/darkmice/talon-sdk-go
├── *.go                  # Go bindings (talon, talon_ai, talon_fts, ...)
├── cgo_<os>_<arch>.go    # CGO link directives, one per supported target
├── include/talon.h       # C header
└── lib/<os>_<arch>/      # Prebuilt libtalon.{a,so,dylib}
```

## Supported targets

| OS / Arch          | Linked library         |
| ------------------ | ---------------------- |
| `darwin/arm64`     | `lib/darwin_arm64/`    |
| `linux/amd64`      | `lib/linux_amd64/`     |

Other architectures from upstream are not bundled here yet. PRs welcome.

## Usage

```go
import "github.com/darkmice/talon-sdk-go"

db, err := talon.Open("path/to/db")
```

On `linux/amd64`, the linker uses `libtalon.so` from `lib/linux_amd64/`. Make sure
that directory is in `LD_LIBRARY_PATH` at runtime:

```bash
export LD_LIBRARY_PATH=$(go list -f '{{.Dir}}' github.com/darkmice/talon-sdk-go)/lib/linux_amd64:$LD_LIBRARY_PATH
```

On `darwin/arm64` the binding embeds `-Wl,-rpath,${SRCDIR}/lib/darwin_arm64`, so
no runtime configuration is needed.

## License

See `LICENSE`.

## Parameterized SQL

Use `ExecParams` and `QueryParams` for positional `?` parameters. Values are
encoded through the native `talon_execute` JSON `bind` path; SQL text is never
constructed by interpolation. `Query` and `QueryParams` reject unknown or
malformed Talon Value tags, while the original `SQL` method remains available
for callers that need the legacy raw result shape.

The exact bundled native artifacts, platforms, ABI header hash and provenance
boundary are recorded in [native-manifest.json](native-manifest.json).

General conditional KV transactions are intentionally not emulated in this
SDK. `KvSetNX` retains its existing single-key semantics; it is not a substitute
for multi-key compare-and-swap. Consumers that need conditional transactions
must wait for a clean pinned Core revision and complete native matrix.
