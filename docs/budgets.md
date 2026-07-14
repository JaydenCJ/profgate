# Budgets file reference

A budgets file turns a profile diff into a CI verdict. It is plain text,
line-based, and lives in your repo next to the code it protects
(conventionally `budgets.txt` or `.profgate`).

## Grammar

```
line     := pattern limit+ | comment | blank
pattern  := glob | "@total"
limit    := key "=" amount
comment  := "#" …        # full-line or trailing
```

Fields are separated by any amount of whitespace. Everything after `#`
on a line is ignored.

## Patterns

Patterns are **anchored globs** matched against the full function name
as it appears in the profile (e.g. `github.com/acme/api/render.Table`).

| Glob | Meaning |
|---|---|
| `*` | any run of characters, including none |
| `?` | exactly one character |
| anything else | literal, case-sensitive (`.` and `/` have no special meaning) |

`app/render.*` matches `app/render.Table` but not `app/renderer.T` —
the match is anchored at both ends, so add `*` where you mean "anything".
The special pattern `@total` budgets the whole profile instead of one
function. Every rule whose pattern matches a function applies to it; a
function can breach several rules at once.

## Limit keys

| Key | Applies to | Breaches when |
|---|---|---|
| `max-flat` | function | head flat value > amount |
| `max-cum` | function | head cumulative value > amount |
| `max-flat-growth` | function | head − base flat > allowance |
| `max-cum-growth` | function | head − base cumulative > allowance |
| `max` | `@total` | head profile total > amount |
| `max-growth` | `@total` | head − base total > allowance |

*Flat* is time/bytes attributed to the function itself (it was the leaf
frame); *cumulative* includes everything it calls. All comparisons are
strict: a value exactly at the limit passes.

## Amounts

| Form | Examples | Interpretation |
|---|---|---|
| duration | `250ns`, `3us`, `25ms`, `1.5s` | for time-unit profiles (CPU) |
| size | `512B`, `4kB` (=4000), `4KiB` (=4096), `2MiB`, `1GiB` | for byte-unit profiles (heap) |
| percent | `10%`, `12.5%` | value limits: share of the **head total**; growth limits: relative to the **base value** |
| bare number | `100`, `1500` | raw profile units (`count` types like `samples` or `alloc_objects`) |

Units are checked against the profile: `max-flat=10ms` against an
`alloc_space/bytes` profile is a configuration error (exit 2), not a
silent pass. Percent and bare-number amounts work with any unit.

## Growth semantics worth knowing

- **New functions breach percent growth caps.** With base = 0, any
  nonzero head value is infinite relative growth, so
  `max-flat-growth=50%` fails a function that did not exist in the base
  profile. Use an absolute allowance (`max-flat-growth=2ms`) where new
  code is expected.
- **Improvements never breach growth caps**, even `max-flat-growth=0ns`
  (which otherwise blocks any increase at all).
- **`max-*-growth=0%` of a zero base is zero allowance** — same effect
  as `0ns`.

## Worked example

```
# no PR may make the service 10% slower overall
@total              max-growth=10%

# the render hot path: absolute ceiling and per-PR creep guard
api/render.*        max-flat=40ms  max-flat-growth=25%

# GC pressure: the cache may not grow its allocations past 8MiB
api/cache.*         max-cum=8MiB

# nothing may silently triple, wherever it lives
*                   max-flat-growth=200%
```

Rules can also be passed inline without a file:
`profgate check --budget 'api/render.* max-flat=40ms' base.pb.gz head.pb.gz`
(repeatable; `--budgets FILE` and `--budget` combine).
