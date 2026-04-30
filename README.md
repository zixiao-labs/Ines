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
2. **Parsing** builds an AST. Go is parsed by `go/parser` and TypeScript /
   JavaScript by a bracket-aware scanner; both adapters sit behind the
   `internal/lang/treesitter` `Backend` interface so a real CGO-backed
   tree-sitter grammar can drop in later without touching call sites. The
   line-oriented regex parser is still kept in
   `internal/lang/regexparser` as the bootstrap fallback for languages whose
   M2 adapters have not landed yet (Rust, Java, Swift, C/C++).
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
| C → S     | `ide/completion`        | Workspace symbol completions for a prefix.   |
| C → S     | `ide/definition`        | Locate every declaration of a symbol.        |
| C → S     | `ide/references`        | Find every reference to a symbol.            |
| C → S     | `ide/rename`            | Compute text edits for a workspace rename.   |
| C → S     | `ide/diagnostics`       | Pull cached parse diagnostics.               |
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

- **M1 — Foundation.** PSI core, bootstrap parsers, IPC, metrics, indexer,
  CI/CD, Logos integration scaffolding. *Done.*
- **M2 — Tree-sitter adoption.** Bootstrap regex parsers replaced with
  full-grammar adapters (Go via the standard `go/parser`, TypeScript via a
  bracket-aware scanner) behind the new `internal/lang/treesitter` Backend
  interface. Symbols carry nested children, signatures and parameters; the
  IPC `SymbolOutput` struct surfaces the tree. The Backend shape mirrors
  tree-sitter's vocabulary so the eventual swap to a CGO-backed grammar is a
  drop-in. The wire codec keeps JSON for now and reserves stable shapes that
  port to Protobuf without breaking existing clients. *Done.*
- **M3 — IDE features.** Code completion, diagnostics, navigation
  (go-to-definition, find references) and safe rename refactoring, all
  layered on top of the indexed PSI snapshot in `internal/feature`. Each
  feature is exposed as a dedicated IPC method (`ide/completion`,
  `ide/definition`, `ide/references`, `ide/rename`, `ide/diagnostics`) so
  the renderer can wire them independently. *Done.*
