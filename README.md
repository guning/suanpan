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

| Entity    | Notes                                                         |
| --------- | ------------------------------------------------------------- |
| `family`  | Household. Has its own currency default and shared ledger.    |
| `person`  | Can stand alone or belong to a family.                        |
| `account` | Cash/bank/credit/investment/virtual, owned by person OR family. |
| `category`| Income/expense/transfer. Global (default seeds) or scoped.    |
| `txn`     | Ledger entry. Transfers link two accounts.                    |
| `budget`  | Weekly/monthly/yearly cap per owner, optionally per category. |

Money is stored as **integer minor units** (fen/cents). The CLI and MCP both accept/emit decimal strings (e.g. `"12.34"`).

## CLI quick tour

```sh
suanpan family add -name "张家"
suanpan person add -name "张三" -family 1
suanpan account add -name 招行储蓄 -type bank -owner person:1 -initial 5000
suanpan txn add -amount 28.50 -account 1 -kind expense -category-name 餐饮 -person 1 -payee 沙县小吃
suanpan txn transfer -from 1 -to 3 -amount 500
suanpan account balance -owner person:1
suanpan report -since 2026-04-01 -until 2026-04-30
suanpan budget add -name 月度 -period monthly -amount 3000 -owner person:1 -start 2026-01-01
suanpan budget status -owner person:1
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
      "env": { "SUANPAN_DB": "/Users/you/.suanpan/suanpan.db" }
    }
  }
}
```

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

### Tools (24)

`init_db`, `create_family`, `list_families`, `delete_family`,
`create_person`, `list_persons`, `delete_person`,
`create_account`, `list_accounts`, `archive_account`, `account_balances`,
`create_category`, `list_categories`, `delete_category`,
`add_expense`, `add_income`, `add_transfer`, `list_transactions`, `delete_transaction`,
`create_budget`, `list_budgets`, `budget_status`, `delete_budget`,
`summarize`.

## Skill

The canonical skill file is `internal/skill/SKILL.md`; it is embedded into the
`suanpan` binary via `go:embed` and installed with `suanpan install-skill`. It
teaches Claude how to book expenses/incomes, query balances, and produce
reports using either the CLI or the MCP tools.
