---
name: accounting
description: Record and query personal/family finances through the suanpan ledger (SQLite-backed). Use when the user books expenses/incomes/transfers, asks about balances, budgets, or wants a period report. Driven via the `suanpan` CLI.
---

# suanpan (算盘) accounting

Local-first double-entry-ish ledger. One SQLite file at `$SUANPAN_DB` (default `~/.suanpan/suanpan.db`).
Six entities: `family`, `person`, `account`, `category`, `txn`, `budget`.
Money is decimal in the UI (`"12.34"`), integer minor units in storage.

## How to drive it — `suanpan` CLI

**Always shell out to the `suanpan` CLI.** Pass `-json` on list/read commands so you can parse results structurally. There is also a `suanpan-mcp` server bundled with the project, but in this environment we drive the CLI; do not look for `mcp__suanpan__*` tools.

## First-run setup

Before any writes, ensure the DB is initialized:

```
suanpan init
```

Idempotent — seeds default Chinese categories: 餐饮/交通/购物/居住/娱乐/医疗/教育/通讯/工资/奖金/投资收益/转账…

## Recording the basics

### Expense
```
suanpan txn add -amount 28.50 -account 1 -kind expense -category-name 餐饮 -person 1 -payee 沙县小吃 -note 午饭
```

### Income
```
suanpan txn add -amount 12000 -account 1 -kind income -category-name 工资 -person 1 -date 2026-04-15
```

### Transfer between accounts
```
suanpan txn transfer -from 1 -to 3 -amount 500 -note 给家里
```

Transfers are their own command — don't model them as a paired expense+income.

## Resolving inputs before writing

### Default person / family — read your persona first

Your **deployment context (persona / system prompt / SOUL.md)** is the source of truth for which `person` and `family` you book to by default. If it names a person + id (e.g. "you serve noreen, person id=1, family id=1"), use those as the default `-person` / `-family` for `txn add` / `txn transfer` / `budget add` etc. Don't re-ask the user "who is this for" — they already told the operator when they set up your profile.

Override the default **only** when the user explicitly says "给 X 记一笔" / "this one is for X" / "家里共同的那笔" — then look up that other person/family before writing.

If no persona binding is given, fall back to asking once.

### Resolve everything else from names

The user almost always speaks in names, not ids. Resolve via `-json`:

1. `suanpan family list -json` → pick or create family (skip if persona pins one).
2. `suanpan person list -json` (optionally `-family <id>`) → resolve person id (skip if persona pins one).
3. `suanpan account list -json` (with `-owner person:N` or `-owner family:N`) → resolve account id.
4. `suanpan category list -json` (optionally `-kind expense`) → resolve category id. `txn add` also accepts `-category-name` and will auto-resolve.

If the referenced entity doesn't exist, ask the user once (single question bundling all missing facts) before creating it. Prefer `suanpan <entity> add` over guessing ids.

## Queries the user is likely to ask

| Ask                                              | Use                                                                  |
| ------------------------------------------------ | -------------------------------------------------------------------- |
| "我这个月花了多少钱"                              | `suanpan report` (defaults to current month)                         |
| "查 4 月 1 日到 4 月 30 日的支出"                   | `suanpan report -since 2026-04-01 -until 2026-04-30`                 |
| "我这个月餐饮花了多少"                            | `suanpan txn list -json -category <id> -since ... -until ...`, then sum |
| "我的账户余额"                                    | `suanpan account balance -owner person:N` (or `family:N`)            |
| "预算还剩多少"                                    | `suanpan budget status -owner person:N`                              |
| "最近 20 笔交易"                                   | `suanpan txn list -json -limit 20`                                   |
| "找一下周三在便利店那笔"                           | `suanpan txn list -json -search ... -since ... -until ...`           |

Run `suanpan <command> -h` whenever you're unsure of a flag — the help is the source of truth.

## Formatting results for the user

- Echo amounts as decimals with the currency (e.g. `28.50 CNY`). The CLI's `-json` output may give minor units — divide by 100 when showing to the user.
- Always include the date window when reporting totals, so the user can verify you interpreted their range correctly.
- For "did it book?" confirmations, include txn id + date + account + amount + (payee/category if present).

## Common pitfalls

- **Amount sign**: `-amount` is always positive. The sign comes from `-kind`. Don't ask the user to pass negative numbers.
- **Transfers are not expenses**: use `suanpan txn transfer`, not `txn add -kind expense`. Transfers don't appear in income/expense totals — they only rebalance accounts.
- **Owner scope**: accounts and budgets are owned by exactly one of `person:N` or `family:N`. When the user says "家里的储蓄卡", pick `family:*` owner; "我的工资卡" → `person:*`.
- **Categories can be global or scoped**. Default seeds are global. When `-category-name` lookup is ambiguous, the global one wins; create a scoped override only if the user asks.
- **Destructive ops (`suanpan * rm`)**: confirm with the user before running — there is no soft-delete and no undo. Archive accounts instead of deleting them when history must be preserved.

## Worked mini-flow

User: "给我张三的招行卡记一笔今天中午 45.8 元的餐饮，在麦当劳。"

1. `suanpan person list -json` → find 张三 → id=1.
2. `suanpan account list -json -owner person:1` → find 招行 → id=1.
3. `suanpan txn add -amount 45.80 -account 1 -kind expense -category-name 餐饮 -person 1 -payee 麦当劳 -date 2026-04-21`
4. Respond: `已记录 #7  张三 · 招行 · 餐饮 · 45.80 CNY · 麦当劳 · 2026-04-21`.
