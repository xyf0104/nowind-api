# XIASS Codex Helper

XIASS Codex Helper is a small local configurator for the Codex desktop app on
macOS and Windows. It binds to a random `127.0.0.1` port and writes the selected
Responses API provider, API key, default model, model catalog, context window,
and automatic compaction limit to the user's Codex configuration.

The helper supports two configuration paths:

- Connect to the user's XIASS API website and select one of that user's keys.
- Enter any compatible HTTPS API, or a loopback HTTP API, directly in the local
  page.

Website-selected keys return through a URL fragment. Manually entered keys are
sent only to the loopback helper. The helper never logs keys and never includes
keys or provider URLs in the generated model catalog.

## Configuration flow

Each apply operation performs only the following work:

1. Validate the requested provider, key, model, and context values.
2. Read and validate the existing `config.toml` so unrelated MCP, plugin,
   project, desktop, and reasoning settings can be preserved.
3. Ask Codex to exit cleanly. If its exit cannot be confirmed, stop without
   changing the configuration.
4. Atomically write the new configuration and optional local model catalog,
   then read both back and validate them. A failed write is rolled back from the
   bytes held in memory during this operation.
5. Start Codex again and verify that its process appears.

The helper does not enumerate, scan, copy, back up, migrate, or rewrite regular
sessions, archived sessions, `session_index.jsonl`, SQLite databases, or WAL
files. It creates no persistent configuration backup, history backup, restore
point, or operation record. Codex reopens the existing conversation data through
its normal startup path.

Website-assisted setup always selects the stable `codex_local_access` provider,
even if another relay was previously active. Valid unrelated configuration and
inactive provider definitions remain in `config.toml`. Manual compatible API
setup remains separate and does not add XIASS-only models unless the configured
API returns them.

## Models and context

The helper can read a compatible API's `/v1/models` response. XIASS returns its
Codex manifest, while ordinary compatible APIs remain authoritative for their
own model list. Complete, validated descriptors can be written to a local
`model_catalog_json`; when complete metadata is unavailable, the helper leaves
Codex's native model catalog untouched.

The local page provides 235K, 372K, 512K, and 1M context presets. Every preset
uses a 90% automatic compaction threshold, and custom context values derive the
same 90% default until the user edits the threshold explicitly. The helper does
not set reasoning effort or `review_model`.

## Verification

```bash
GOCACHE=/tmp/xiass-go-build-cache GOSUMDB=off go test ./...
```

## Build

```bash
CGO_ENABLED=0 go build -trimpath -o xiass-codex-helper .
```

Release builds are published independently by
`.github/workflows/codex-helper-release.yml` as:

- `xiass-codex-helper-macos-universal.zip`
- `xiass-codex-helper-windows-x64.exe`

Both assets are replaced in the fixed `codex-helper-latest` prerelease. Updating
the helper does not change the XIASS API server version or Docker image.
