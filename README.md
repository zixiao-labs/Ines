# Ines

Ines is the language daemon that powers **Stage 2** of the Logos Editor
roadmap: intelligent code completion, navigation, refactoring and diagnostics
for C, C++, Java, JavaScript / TypeScript, Swift, Go and Rust. It exposes a
JetBrains-style **Program Structure Interface (PSI)** to the editor and is
meant to be downloaded on demand from Logos rather than bundled into the main
installer.

> Naming origin — Ines is the codename of an Arknights operator. The hardest
> things in computer science are still cache invalidation and naming, so we
> picked one and moved on.

## Architecture

```
┌──────────────────┐  length-prefixed JSON over stdio  ┌──────────────────┐
│      Logos       │ ──────────────────────────────▶  │       Ines       │
│  (Electron app)  │ ◀──────────────────────────────  │  (Go daemon)     │
└──────────────────┘                                   └──────────────────┘
                                                              │
                                                              ▼
                                          ┌────────────────────────────────┐
                                          │ language adapters (Go / TS /   │
                                          │ Rust / Java / Swift / C++ ...) │
                                          └────────────────────────────────┘
                                                              │
                                                              ▼
                                          ┌────────────────────────────────┐
                                          │ PSI (psi.Element / psi.File)   │
                                          └────────────────────────────────┘
```

Source files travel through three layers:

1. **Lexical analysis** turns the bytes into tokens.
2. **Parsing** builds an AST. The bootstrap implementation uses a
   line-oriented regex parser; the next milestone replaces it with a
   tree-sitter wrapper that lives behind the same `parser.Parser`
   interface.
3. **PSI wrapping** lifts AST nodes into `psi.Element` values that carry
   behavioural capabilities: navigation (Parent/Children), querying
   (`FindByKind`, `FindByName`), and structural metadata used by completion,
   refactoring and diagnostics.

`PsiFile → PsiClass → PsiMethod → PsiParameter → PsiExpression` is the
canonical hierarchy; helpers in `internal/psi/treeutil.go` expose the
JetBrains-style traversal utilities.

## Wire protocol

The daemon listens on stdin and writes to stdout. Frames are encoded as a
4-byte big-endian length followed by a JSON payload (see
`internal/ipc/codec.go`). The bootstrap message set lives in
`internal/ipc/messages.go`:

| Direction | Method                  | Purpose                                      |
|-----------|-------------------------|----------------------------------------------|
| C → S     | `initialize`            | Negotiate protocol/version, share workspace. |
| S → C     | `initialize/status`     | Splash text rendered by Logos.               |
| C → S     | `index/workspace`       | Walk and parse the workspace.                |
| S → C     | `index/progress`        | Per-file progress used for the progress bar. |
| C → S     | `index/lookup`          | Outline view for a single file.              |
| C → S     | `metrics/snapshot`      | One-shot resource report.                    |
| S → C     | `metrics/heartbeat`     | Periodic resource report (every 5s).         |
| C → S     | `shutdown`              | Cancel in-flight indexing and exit.          |

JSON is the **bootstrap codec**. The contract was designed to be portable to
protobuf without changing the framing — every message has a stable shape and
field tags can be added at the next iteration.

## Building

```bash
make build           # local binary at ./bin/ines
make test            # unit tests
make dist            # cross-compiled binaries under ./dist
go run ./cmd/ines    # run interactively (close stdin to exit)
```

The daemon ships as a single static binary; there is no required runtime
dependency.

## Releases

Tagged builds are produced by `.github/workflows/release.yml`. Each release
publishes one tarball per `os/arch` combination under the well-known path
`https://github.com/zixiao-labs/Ines/releases/download/<tag>/ines-<os>-<arch>.tar.gz`
(plus `.exe.zip` on Windows). The Logos settings page downloads from this
location when the user enables the **Download Enhanced Language Capabilities**
option, so the URL layout is part of the public contract.

## Development plan

Stage 2 is delivered iteratively:

- **M1 — Foundation (this milestone).** PSI core, bootstrap parsers, IPC,
  metrics, indexer, CI/CD, Logos integration scaffolding.
- **M2 — Tree-sitter adoption.** Replace the bootstrap regex parsers with
  tree-sitter grammars (Go and TypeScript first), switch the codec to
  Protobuf, expose richer symbol information.
- **M3 — IDE features.** Code completion, diagnostics, safe refactoring and
  navigation built on top of PSI.
