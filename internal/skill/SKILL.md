---
name: accounting
description: Record and query personal/family finances through the suanpan ledger (SQLite-backed). Use when the user books expenses/incomes/transfers, asks about balances, budgets, or wants a period report. Supports two frontends — the `suanpan` CLI and the `suanpan-mcp` MCP tools — both backed by the same local DB.
---

# suanpan (算盘) accounting

Local-first double-entry-ish ledger. One SQLite file at `$SUANPAN_DB` (default `~/.suanpan/suanpan.db`).
Six entities: `family`, `person`, `account`, `category`, `txn`, `budget`.
Money is decimal in the UI (`"12.34"`), integer minor units in storage.

## Which interface to use

- If MCP tools with prefix `mcp__suanpan__` are available, **prefer them** — the JSON in/out is unambiguous. Key tools: `add_expense`, `add_income`, `add_transfer`, `list_transactions`, `account_balances`, `summarize`, `budget_status`, and the CRUD counterparts for every entity.
- Otherwise shell out to `suanpan` — pass `-json` on list/read commands so you can parse results.

Both speak to the same DB. Never mix by calling one to write and the other mid-transaction.

## First-run setup

Before any writes, ensure the DB is initialized:

```
suanpan init
```

or MCP: `init_db` (idempotent — seeds default Chinese categories: 餐饮/交通/购物/居住/娱乐/医疗/教育/通讯/工资/奖金/投资收益/转账…).

## Recording the basics

### Expense
MCP:
```json
{"name":"add_expense","arguments":{"amount":"28.50","account_id":1,"category_name":"餐饮","person_id":1,"payee":"沙县小吃","note":"午饭"}}
```
CLI:
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
MCP: `add_transfer` with `from_account_id`/`to_account_id`.

## Resolving inputs before writing

The user almost always speaks in names, not ids. Resolve first:

1. `list_families` / `suanpan family list -json` → pick or create family.
2. `list_persons` (optionally `family_id`) → resolve person id.
3. `list_accounts` (with `owner_kind`+`owner_id`) → resolve account id.
4. `list_categories` (optionally `kind=expense`) → resolve category id. `add_expense`/`add_income` also accept `category_name` and will auto-resolve.

If the referenced entity doesn't exist, ask the user once (single question bundling all missing facts) before creating it. Prefer `create_*` tools / `suanpan <entity> add` over guessing ids.

## Queries the user is likely to ask

| Ask                                              | Use                                                           |
| ------------------------------------------------ | ------------------------------------------------------------- |
| "我这个月花了多少钱"                              | `summarize` with default period (or `suanpan report`)         |
| "查 4 月 1 日到 4 月 30 日的支出"                   | `summarize` / `suanpan report -since ... -until ...`          |
| "我这个月餐饮花了多少"                            | `list_transactions` with `category_id` + date range, then sum |
| "我的账户余额"                                    | `account_balances` / `suanpan account balance`                |
| "预算还剩多少"                                    | `budget_status`                                               |
| "最近 20 笔交易"                                   | `list_transactions` with `limit=20`                           |
| "找一下周三在便利店那笔"                           | `list_transactions` with `search`/`since`/`until`             |

## Formatting results for the user

- Echo amounts as decimals with the currency (e.g. `28.50 CNY`). MCP returns `amount` in minor units — divide by 100 when showing to the user.
- Always include the date window when reporting totals, so the user can verify you interpreted their range correctly.
- For "did it book?" confirmations, include txn id + date + account + amount + (payee/category if present).

## Common pitfalls

- **Amount sign**: `amount` in storage is always positive. The sign comes from `kind`. Don't ask the user to pass negative numbers.
- **Transfers are not expenses**: use `add_transfer` / `suanpan txn transfer`, not `add_expense`. Transfers don't appear in income/expense totals — they only rebalance accounts.
- **Owner scope**: accounts and budgets are owned by exactly one of `person:N` or `family:N`. When the user says "家里的储蓄卡", pick `family:*` owner; "我的工资卡" → `person:*`.
- **Categories can be global or scoped**. Default seeds are global. When `category_name` lookup is ambiguous, the global one wins; create a scoped override only if the user asks.
- **Destructive ops (`delete_*`, `suanpan * rm`)**: confirm with the user before running — there is no soft-delete and no undo. Archive accounts instead of deleting them when history must be preserved.

## Worked mini-flow

User: "给我张三的招行卡记一笔今天中午 45.8 元的餐饮，在麦当劳。"

1. `list_persons` → find 张三 → id=1.
2. `list_accounts owner_kind=person owner_id=1` → find 招行 → id=1.
3. `add_expense`:
   ```json
   {"amount":"45.80","account_id":1,"category_name":"餐饮","person_id":1,"payee":"麦当劳","date":"2026-04-21"}
   ```
4. Respond: `已记录 #7  张三 · 招行 · 餐饮 · 45.80 CNY · 麦当劳 · 2026-04-21`.
