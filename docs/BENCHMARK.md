# Benchmarking EMUS transport

This repository includes a small benchmark client at `cmd/bench`.

It deliberately compares the same deterministic random-read plan against:

1. the SERVER-EMUS-PS5 HTTP Range endpoint; and
2. an optional file already mounted by the host OS (for example SMB).

It does **not** claim that EMUS is inherently faster than SMB. The tool exists
to gather comparable measurements before making that claim.

## Example

```bash
go run ./cmd/bench \
  -server http://192.168.1.50:8787 \
  -file-id <catalog-file-id> \
  -read-size 65536 \
  -samples 512 \
  -seed 42 \
  -pattern random \
  -local /mnt/smb/PS1/Game.chd \
  -json
```

Bearer-authenticated servers are supported without putting the token in
process arguments or benchmark output. By default the client reads the token
from the `SERVER_EMUS_TOKEN` environment variable. Use `-token-env NAME` to
select another environment variable, or `-token-env ""` for a trusted
unauthenticated benchmark instance.

Example:

```bash
export SERVER_EMUS_TOKEN='<server bearer token>'
go run ./cmd/bench \
  -server http://192.168.1.50:8787 \
  -file-id <catalog-file-id> \
  -samples 512 \
  -json
```

Do not place the bearer token directly on the command line: shell history and
process listings can expose command-line arguments.

Reported fields include:

- request count;
- bytes transferred;
- total elapsed time;
- MiB/s;
- p50 latency;
- p95 latency;
- maximum latency.

The HTTP side probes the file with `HEAD`, requires `Accept-Ranges: bytes`
and an ETag, then sends each read with both `Range` and `If-Match`. Every 206
response must return the same ETag and a `Content-Range` matching the exact
offset/length requested; malformed, shifted or truncated range metadata aborts
the run. That prevents a benchmark from silently timing the wrong bytes or
continuing against a file that changed during the sample.

The same seed, file size, read size, sample count and pattern produce the same
offset plan. Keep those values fixed when comparing EMUS against an OS-mounted
SMB path.

Three deterministic access patterns are available:

- `random` — independent offsets; preserves the original benchmark behavior;
- `sequential` — contiguous block-sized reads, wrapping only at the file end;
- `clustered` — deterministic groups of eight contiguous reads whose starting
  points are pseudo-random.

The clustered pattern is intended to exercise emulator read-ahead without
pretending that one synthetic workload represents every core. Record the
pattern with benchmark results.

## Recommended benchmark record

Record at least:

- SERVER-EMUS-PS5 commit;
- client/PS5 runtime commit when applicable;
- server OS and storage type;
- network link speed and topology;
- file format and size;
- read size, sample count, seed and access pattern;
- whether the OS-mounted baseline is SMB/NFS/local;
- raw JSON output.

Physical PS5 measurements remain a separate validation gate.
