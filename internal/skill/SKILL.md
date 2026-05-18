---
name: accounting
version: 2
description: THE ONLY way to record or query personal/family finances on this system. Use whenever the user mentions money in/out — "记一笔", "花了 XX", "买了 XX", balances, budgets, monthly reports, etc. Backed by a SQLite ledger driven via the `suanpan` CLI. Do NOT invent your own JSON/CSV/Python expense files — there is no fallback, this is canonical.
---

# suanpan (算盘) accounting

Local-first ledger. One SQLite file at `$SUANPAN_DB` (default `~/.suanpan/suanpan.db`).
Five entities: `family`, `person`, `category`, `txn`, `budget`.
Money is decimal in the UI (`"12.34"`), integer minor units in storage.

## ⚠️ Hard rules — read before doing anything

1. **`suanpan` CLI is the only valid sink for financial records.** Never write your own `expenses.json`, `ledger.csv`, Python dicts, or any other ad-hoc store — that data won't survive, won't show up in reports, and won't reconcile with the rest of the household ledger.
2. **Never wrap suanpan in a Python sandbox script when a single shell call works.** Use the terminal tool to run `suanpan ...` directly. Sandbox scripts are for things suanpan can't do (e.g. OCR pre-processing of a receipt before calling `suanpan txn add`).
3. If `suanpan` isn't on PATH or the DB is missing, stop and report it — don't substitute another mechanism.

## How to drive it — `suanpan` CLI

Shell out to the `suanpan` CLI. Pass `-json` on list/read commands so you can parse results structurally. There is also a `suanpan-mcp` server bundled with the project, but in this environment we drive the CLI; do not look for `mcp__suanpan__*` tools.

## Database is already set up — don't initialize it

The DB at `$SUANPAN_DB` (default `~/.suanpan/suanpan.db`) is **pre-initialized by the operator** during deployment. **Never run `suanpan init` as part of normal bookkeeping** — it confuses the user (looks like "loading…" delays in chat) and is unnecessary. If `suanpan` reports "no such table" or similar schema errors, stop and tell the user; that's an operator issue, not something to self-heal.

Default Chinese categories are already seeded: 餐饮/交通/购物/居住/娱乐/医疗/教育/通讯/工资/奖金/投资收益…

## Recording the basics

Every txn needs an owner — either `-person <id>` (preferred, who actually spent the money) or `-family <id>` (shared household expense). There is no `account` concept; we record who, not where.

### Expense
```
suanpan txn add -amount 28.50 -kind expense -category-name 餐饮 -person 1 -payee 沙县小吃 -note 午饭
```

### Income
```
suanpan txn add -amount 12000 -kind income -category-name 工资 -person 1 -date 2026-04-15
```

### Shared household spending
```
suanpan txn add -amount 800 -kind expense -category-name 居住 -family 1 -payee 物业费
```

## Reads are family-scoped — pass `-as-person`

`report`, `txn list`, `budget list`, and `budget status` require a caller identity so the binary can restrict results to your family (your own + siblings sharing the same `family_id` + family-tagged txns). **Other families are invisible** — this is a hard security gate, not a hint.

Pass it either way:
- `-as-person <id>` on each command, OR
- export `SUANPAN_AS_PERSON=<id>` once per shell.

Your **deployment context (persona / SOUL.md)** pins which person id you are — use that as `-as-person` and as the default `-person` for writes. Don't re-ask the user "who is this for" — they already told the operator when they set up your profile.

Override the write default **only** when the user explicitly says "给 X 记一笔" / "this one is for X" / "家里共同的那笔" — then look up that other person/family before writing.

If no persona binding is given, fall back to asking once.

## Resolving inputs before writing

The user almost always speaks in names, not ids. Resolve via `-json`:

1. `suanpan family list -json` → pick or create family (skip if persona pins one).
2. `suanpan person list -json` (optionally `-family <id>`) → resolve person id (skip if persona pins one).
3. `suanpan category list -json` (optionally `-kind expense`) → resolve category id. `txn add` also accepts `-category-name` and will auto-resolve.

If the referenced entity doesn't exist, ask the user once (single question bundling all missing facts) before creating it. Prefer `suanpan <entity> add` over guessing ids.

## Queries the user is likely to ask

| Ask                                              | Use                                                                  |
| ------------------------------------------------ | -------------------------------------------------------------------- |
| "我这个月花了多少钱"                              | `suanpan report -as-person N` (defaults to current month)            |
| "查 4 月 1 日到 4 月 30 日的支出"                   | `suanpan report -as-person N -since 2026-04-01 -until 2026-04-30`    |
| "我这个月餐饮花了多少"                            | `suanpan txn list -as-person N -json -category <id> -since ... -until ...`, then sum |
| "预算还剩多少"                                    | `suanpan budget status -as-person N`                                 |
| "最近 20 笔交易"                                   | `suanpan txn list -as-person N -json -limit 20`                      |
| "找一下周三在便利店那笔"                           | `suanpan txn list -as-person N -json -search ... -since ... -until ...` |

Run `suanpan <command> -h` whenever you're unsure of a flag — the help is the source of truth.

## Formatting results for the user

- Echo amounts as decimals with the currency (e.g. `28.50 CNY`). The CLI's `-json` output may give minor units — divide by 100 when showing to the user.
- Always include the date window when reporting totals, so the user can verify you interpreted their range correctly.
- For "did it book?" confirmations, include txn id + date + person/family + amount + (payee/category if present).

## Common pitfalls

- **Amount sign**: `-amount` is always positive. The sign comes from `-kind`. Don't ask the user to pass negative numbers.
- **No account / no transfer**: spending accounts and inter-account transfers were removed. We only track money flowing in or out, owned by a person or a family. Don't try to model "moved money from card A to card B".
- **Owner scope on budgets**: budgets are owned by exactly one of `person:N` or `family:N`. A `family:N` budget sums BOTH `txn.family_id=N` and any `txn.person_id` whose person belongs to family N.
- **Categories can be global or scoped**. Default seeds are global. When `-category-name` lookup is ambiguous, the global one wins; create a scoped override only if the user asks.
- **Destructive ops (`suanpan * rm`)**: confirm with the user before running — there is no soft-delete and no undo.

## Worked mini-flow

User: "给我张三记一笔今天中午 45.8 元的餐饮，在麦当劳。" (persona pinned to person:1)

1. `suanpan person list -json` → confirm 张三 is person:1.
2. `suanpan txn add -amount 45.80 -kind expense -category-name 餐饮 -person 1 -payee 麦当劳 -date 2026-04-21`
3. Respond: `已记录 #7  张三 · 餐饮 · 45.80 CNY · 麦当劳 · 2026-04-21`.
