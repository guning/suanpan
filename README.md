# suanpan 算盘

Local-first 记账软件, written in Go. Single SQLite file, two frontends:

- **CLI** (`suanpan`) — day-to-day entry and reports from a shell.
- **MCP server** (`suanpan-mcp`) — expose accounting tools to Claude Code / any MCP client.
- **Skill** (bundled, canonical copy at `internal/skill/SKILL.md`) — teaches Claude how to drive either. Install with `suanpan install-skill`.

No UI. No sync. Data lives in `~/.suanpan/suanpan.db` (override with `$SUANPAN_DB`).

## Install

```sh
make install                 # → ~/.local/bin/{suanpan,suanpan-mcp}
suanpan init                 # create schema + default categories
suanpan install-skill        # drop SKILL.md into ~/.claude/skills/accounting/
                             #   and print the `claude mcp add` command
```

### install-skill flags

```
suanpan install-skill [-scope user|project] [-name accounting]
                      [-skill-only] [-mcp-add] [-mcp-bin PATH]
                      [-db PATH] [-force] [-dry-run]
```

- Default writes `~/.claude/skills/accounting/SKILL.md` and prints the
  `claude mcp add` command for you to run.
- `-scope project` targets `./.claude/skills/` + `--scope project` for the MCP
  add command (so the skill and server register only for the current repo).
- `-mcp-add` also executes the printed command (requires the `claude` CLI).
- `-skill-only` skips the MCP block entirely.

## Entities

| Entity    | Notes                                                        |
| --------- | ------------------------------------------------------------ |
| `family`  | Household. Has its own currency default and shared ledger.   |
| `person`  | Can stand alone or belong to a family.                       |
| `category`| Income/expense. Global (default seeds) or scoped to an owner. |
| `txn`     | Ledger entry, owned by exactly one of `person` or `family`.  |
| `budget`  | Weekly/monthly/yearly cap per owner, optionally per category.|

Money is stored as **integer minor units** (fen/cents). The CLI and MCP both accept/emit decimal strings (e.g. `"12.34"`).

There is no `account` layer and no `transfer` kind — we record flow + owner, not source-of-funds. Legacy v0 databases are migrated automatically on the next `suanpan init`.

## Family scoping for reads

`report`, `txn list`, `budget list`, and `budget status` are scoped by caller identity. Either pass `-as-person <id>` or export `$SUANPAN_AS_PERSON=<id>`. Results then include only:

- The caller's own txns/budgets,
- Siblings sharing the same `family_id`,
- Txns/budgets tagged with the caller's family.

Other families are invisible — this is enforced in the SQL layer, not just the UI.

## CLI quick tour

```sh
suanpan family add -name "张家"
suanpan person add -name "张三" -family 1
suanpan txn add -amount 28.50 -kind expense -category-name 餐饮 -person 1 -payee 沙县小吃
suanpan txn add -amount 800 -kind expense -category-name 居住 -family 1 -payee 物业费
suanpan budget add -name 月度 -period monthly -amount 3000 -owner person:1 -start 2026-01-01
suanpan report     -as-person 1 -since 2026-04-01 -until 2026-04-30
suanpan budget status -as-person 1
suanpan txn list   -as-person 1 -limit 20 -json
```

Every list command supports `-json` for machine output.

## MCP server

Two transports — pick based on how many clients you want to attach.

### stdio (default) — single client, Claude Code spawns it

```json
{
  "mcpServers": {
    "suanpan": {
      "command": "/Users/you/.local/bin/suanpan-mcp",
      "env": {
        "SUANPAN_DB": "/Users/you/.suanpan/suanpan.db",
        "SUANPAN_AS_PERSON": "1"
      }
    }
  }
}
```

`SUANPAN_AS_PERSON` is the caller-identity fallback used by scoped tools when the caller omits `as_person_id`.

### Streamable HTTP — many clients, one long-running server

Run the server:

```sh
SUANPAN_DB=~/.suanpan/suanpan.db suanpan-mcp -listen localhost:7777
# or expose to LAN (REQUIRES a token):
SUANPAN_TOKEN=$(openssl rand -hex 16) suanpan-mcp -listen :7777
```

Register with Claude Code:

```sh
claude mcp add --transport http --scope user suanpan http://localhost:7777/mcp
# with bearer auth:
claude mcp add --transport http --scope user suanpan http://host:7777/mcp \
  --header "Authorization: Bearer $SUANPAN_TOKEN"
```

Endpoints: `POST /mcp` (JSON-RPC), `GET /mcp` (SSE stream for future
notifications, currently emits only keep-alives), `DELETE /mcp` (close),
`GET /healthz` (unauthenticated liveness).

Concurrency: multiple MCP clients (Claude Code / Desktop / Cursor / your phone)
can connect to the same server simultaneously. Writes serialize at the SQLite
layer; tool dispatch is guarded by a mutex so read-then-write tools see a
consistent snapshot.

Security:

- Without `$SUANPAN_TOKEN`, the server rejects Host headers that don't resolve
  to loopback (defends against DNS rebinding from a browser tab).
- With `$SUANPAN_TOKEN`, every request must include
  `Authorization: Bearer <token>`.
- Binding to a non-loopback interface without a token logs a warning and
  proceeds — don't do this unless you trust the network.

### Tools (19)

`init_db`, `create_family`, `list_families`, `delete_family`,
`create_person`, `list_persons`, `delete_person`,
`create_category`, `list_categories`, `delete_category`,
`add_expense`, `add_income`, `list_transactions`, `delete_transaction`,
`create_budget`, `list_budgets`, `budget_status`, `delete_budget`,
`summarize`.

Read tools (`list_transactions`, `summarize`, `list_budgets`, `budget_status`) accept `as_person_id` for family scoping, falling back to `$SUANPAN_AS_PERSON`.

## Skill

The canonical skill file is `internal/skill/SKILL.md`; it is embedded into the
`suanpan` binary via `go:embed` and installed with `suanpan install-skill`. It
teaches Claude how to book expenses/incomes, query reports, and read budgets
using either the CLI or the MCP tools.

## Maintaining

### Tests + quick demo

```sh
make test          # go vet + go test ./...
make demo          # wipe a throwaway DB and walk through init/add/report
```

The demo is the smoke test for the read-scope contract — if `report` ever
forgets to enforce `-as-person`, demo will start showing every family's
ledger and you'll notice.

### Updating the skill

`internal/skill/SKILL.md` is embedded into the `suanpan` binary. To roll an
update out to existing installs:

1. Edit `internal/skill/SKILL.md`.
2. Bump the `version: N` line in the frontmatter.
3. Rebuild + redistribute the binary.

On the next `suanpan install-skill` (no `-force` needed), installs with an
older version auto-upgrade; same-version installs are skipped. Hand-edited
copies stay put unless the user asks for `-force`. See
`cmd/suanpan/install_skill.go:writeSkill` for the comparison logic and
`TestCLI_InstallSkill_AutoUpgrade` for the contract.

### Adding a new DB migration

Schema version lives in `schema_meta(key='version')`. The current shipped
version is **v1**. To add a v1 → v2 upgrade:

1. Edit `internal/store/schema.sql` to the new target shape (this is the
   schema applied to fresh DBs).
2. Add `internal/store/migrate_v2.sql` containing the v1 → v2 statements,
   ending with `INSERT OR REPLACE INTO schema_meta(key, value) VALUES ('version', '2');`
3. In `internal/store/store.go`:
   - Add `//go:embed migrate_v2.sql` and a `migrateV2SQL` variable.
   - Extend `migrate()` to check `schema_meta.version` and dispatch to the
     right migration script. The current `migrate()` uses presence of the
     `account` table as a v0 detector — v2+ should read the version row.
4. Add a `TestMigrateV1ToV2` mirroring `TestMigrateV0ToV1` in
   `internal/store/store_test.go`: build a v1 fixture inline, seed
   representative rows for every behavioural branch, run `Init()`, assert
   the post-conditions one column at a time.

The FK-off / transaction / FK-on bracketing in `migrate()` is required —
`ALTER TABLE` rewrites with FK on will reject the intermediate state.

### Cross-compiling for a remote Linux host

The codebase is pure Go with one Cgo-free SQLite driver (`modernc.org/sqlite`),
so cross-compilation needs no toolchain — just env flags:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-s -w" -o /tmp/suanpan     ./cmd/suanpan
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-s -w" -o /tmp/suanpan-mcp ./cmd/suanpan-mcp
```

`-s -w` strips debug info (binary shrinks ~30%). `CGO_ENABLED=0` produces a
fully static binary that runs on any glibc/musl Linux.

Note `go.mod` declares `go 1.25` — if your remote host's Go is older, the
remote can still **run** these binaries (Go compatibility is forward, the
runtime is bundled), but you can't `go build` on the remote. Always
cross-compile from a host with a matching-or-newer toolchain.
