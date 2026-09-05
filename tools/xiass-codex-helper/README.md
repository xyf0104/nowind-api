# XIASS Codex Helper

XIASS Codex Helper is a portable local configurator for macOS and Windows. It
binds only to a random `127.0.0.1` port. Users can connect to their own XIASS
API website and select one of their own keys, or manually enter any compatible
Responses API Base URL, API key, and default session model. The default XIASS site is
`https://api.xiass.com`. Website-selected keys are returned through a URL
fragment; manually entered keys are posted only to the loopback helper.

For a compatible API, the local page can read that provider's model catalog
using the Base URL and key entered on the page. XIASS returns its live Codex
manifest (`models[].slug`) so newly enabled, account-specific models such as
`gpt-6-astra` can be selected; the helper also supplies this model when the
configured XIASS deployment is an older catalog that has not listed it yet.
Ordinary compatible APIs remain authoritative for their own `data[].id` list.
After a configuration is applied, the helper writes a sanitized local model
catalog containing only model metadata and IDs, then points Codex at it with
`model_catalog_json`. API keys, tokens, provider URLs, and account data are
never written to that catalog. Providers without a model catalog can still use
manually entered model names. The catalog remains on the local machine and is
included in the configuration backup.

The existing helper compatibility behavior is retained: `web_search = "live"`
is written for every managed provider configuration, known GPT/Codex models
keep their image input and built-in search capabilities, and the built-in
`gpt-image-2` model keeps its image capability metadata. Custom API providers
are not given XIASS-only models unless that model is actually returned by the
configured API.

The helper configures only the default session model. It deliberately omits
`review_model`, including when an older website or config sends that field, so
Codex keeps its official default review behavior.

Website-selected XIASS keys are rechecked locally when the callback arrives.
This protects older website versions that either omit the model or send an old
default: the helper shows the model chooser before applying the configuration.

Before applying a configuration, the helper:

1. Locates the user-level Codex `config.toml` and supports validated manual App selection or a pasted App path when automatic discovery is unavailable.
2. Validates the existing TOML.
3. Stops Codex cleanly before changing configuration or conversation metadata.
4. Creates a byte-for-byte configuration backup with a SHA-256 manifest.
5. Discovers both legacy `~/.codex/state_5.sqlite` and current
   `~/.codex/sqlite/*` conversation databases.
6. Creates coherent SHA-256-verified SQLite snapshots that include committed WAL data, and also
   backs up session metadata, `session_index.jsonl`, Codex desktop state, and
   workspace mappings before repairing history visibility.
7. Website-assisted XIASS connections always switch the active provider to the
   stable `codex_local_access` XIASS provider, even when Codex was previously
   configured for another relay or a custom provider. The original file is
   backed up byte-for-byte first; unrelated settings and inactive foreign
   provider definitions are retained when their TOML is valid. Manual custom
   API configuration remains separate and does not perform this takeover.
   Provider metadata in normal and archived rollouts plus compatible
   `threads.model_provider` columns is synchronized to the active provider so
   every conversation remains visible after switching providers.
8. Writes and verifies a local Codex model catalog for the models returned by
the selected API, with a local compatibility entry for XIASS `gpt-6-astra`.
The catalog is backed up and rolled back together with `config.toml`, and it
never contains the API key.
9. When the configured default model changes, compatible `threads.model`
columns are synchronized so existing regular conversations can use newly
enabled models such as `gpt-6-astra`; `codex-auto-review` threads, rollout
messages, and provider-bound continuation IDs are left untouched. A
same-provider model change uses a database-only fast path with the same
coherent snapshot, transaction verification, and rollback guarantees. Older
Codex databases without a thread model column remain supported and are
reported without being modified.
10. The explicit history-repair action also removes only incompatible internal
   Responses continuation records (encrypted reasoning/compaction entries and
   invalid message IDs) when the active provider is external. It keeps visible
   user and assistant messages, attachments, tool calls, and tool output; the
   first-party `openai` provider is left untouched.
11. Validates project paths and `rootPaths`, repairs malformed macOS workspace
   mappings that can hide intact conversations from the sidebar, and leaves
   valid Windows paths unchanged.
12. Preserves unrelated MCP, plugin, project, desktop, and reasoning settings.
13. Uses atomic file replacement and SQLite transactions, then verifies database
   integrity, provider consistency, the exact thread-ID sets, the rollout file
   set, and workspace mappings.
14. Records durable repair states, recovers interrupted operations on the next
    run, rolls back configuration/history on failure, and starts Codex only
    after every verification succeeds.
15. On Windows, Microsoft Store/WindowsApps installations are launched through
    their registered `shell:AppsFolder` target instead of executing the
    protected package binary directly. Optional SQLite files that cannot be
    confirmed as thread-provider databases are skipped; `state_*` databases
    remain strictly validated.
16. Windows process polling uses native Toolhelp APIs. Remaining PowerShell and
    task-control commands run with no-window flags, preventing repeated console
    flashes during shutdown and launch verification.
17. SQLite file URIs normalize Windows drive letters and percent-encode Unicode
    profile paths, including Codex homes under non-ASCII Windows user names.
18. Windows discovery prioritizes the registered `OpenAI.Codex` AppX package
    (whose current desktop process is `ChatGPT.exe`) and rejects Antigravity,
    editor-extension, desktop-managed CLI, npm, Cargo, Scoop, and Chocolatey
    `codex.exe` paths as desktop App candidates.
19. The local page supports compatible, balanced, and 1M context profiles, plus
    validated custom values for `model_context_window` and
    `model_auto_compact_token_limit`. The selected values are carried through
    the XIASS key-selection redirect and are written with the same atomic
    backup, read-back verification, and rollback guarantees as the provider
    configuration.

Restore operations validate the selected backup and create another safety
backup before replacing the current configuration. A same-provider context or
key update leaves conversation history and its indexes untouched; a
same-provider model update synchronizes only the thread-model database column,
so these common operations avoid the expensive full history scan. Changing the
model provider, restoring a legacy XIASS provider configuration, and the
explicit history-repair action still take verified history snapshots and run
the full repair/rollback path. Compatibility-repair backups are listed
separately and can be restored only while no newer local conversation data has
been written. Both configuration and completed history backups can be deleted
from the local page without restarting Codex.

The repair behavior was cross-checked against public provider-sync approaches
and the [Codex cross-provider history issue](https://github.com/openai/codex/issues/15494).
The XIASS implementation is written
independently and adds stop-before-write, atomic rollout replacement, full
database rollback, and thread-count verification.

## Local verification

```bash
GOCACHE=/tmp/xiass-go-build-cache GOSUMDB=off go test ./...
```

## Build

```bash
CGO_ENABLED=0 go build -trimpath -o xiass-codex-helper .
```

Release builds are produced independently by
`.github/workflows/codex-helper-release.yml` as:

- `xiass-codex-helper-macos-universal.zip`
- `xiass-codex-helper-windows-x64.exe`

Both files are replaced in the `codex-helper-latest` prerelease so their public
download URLs remain stable without changing the XIASS API release version.
