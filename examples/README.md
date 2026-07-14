# profgate examples

Three self-contained pieces, all offline.

## make-demo-profiles

A small Go program that writes deterministic pprof profiles: a base/head
CPU pair where `demoapp/render.Table` regresses 10ms → 42ms, and a
base/head heap pair where `demoapp/cache.Fill` starts over-allocating.
Byte-stable across runs, so every command in the README reproduces exactly.

```bash
go run ./examples/make-demo-profiles /tmp/profgate-demo
profgate diff /tmp/profgate-demo/base.cpu.pb.gz /tmp/profgate-demo/head.cpu.pb.gz
```

## budgets.txt

A realistic budgets file for the demo app: a whole-profile growth cap,
absolute and growth limits on the render hot path, a cumulative budget on
the store layer, a percent-of-total cap on serialization, and a catch-all
rule. Annotated line by line; the full syntax lives in
[docs/budgets.md](../docs/budgets.md).

```bash
profgate check --budgets examples/budgets.txt \
  /tmp/profgate-demo/base.cpu.pb.gz /tmp/profgate-demo/head.cpu.pb.gz
```

## ci-gate.sh

The whole CI story in one script: build, fabricate stand-in profiles
(swap in your real artifacts), run `check --format markdown`, publish the
summary, and propagate the exit code so a breach fails the job.

```bash
bash examples/ci-gate.sh
```
