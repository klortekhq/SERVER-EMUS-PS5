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
  -batch 1 \
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

The HTTP benchmark can group 1 to 16 logical ranges into each request with
`-batch`. A value above 1 exercises the protocol's standard
`multipart/byteranges` path. Every returned part is checked against the exact
requested `Content-Range` and length. The mounted-file baseline still performs
the same logical read plan directly.

Reported fields include:

- logical read count;
- HTTP/file operation request count;
- bytes transferred;
- total elapsed time;
- MiB/s;
- p50 latency;
- p95 latency;
- maximum latency;
- fresh TCP connections observed by the HTTP transport;
- reused persistent connections observed by the HTTP transport;
- connection-reuse percentage for the timed HTTP requests.

The HTTP side probes the file with `HEAD`, requires `Accept-Ranges: bytes`
and an ETag, then sends each read with both `Range` and `If-Match`. Probe,
timed and verification requests explicitly request `Accept-Encoding: identity`;
any transformed `Content-Encoding` is rejected so byte counts and sampled
identity checks always describe the original object bytes. Every 206 response
must return the same ETag and a `Content-Range` matching the exact
offset/length requested; malformed, shifted or truncated range metadata aborts
the run. That prevents a benchmark from silently timing the wrong bytes or
continuing against a file that changed during the sample.

The same seed, file size, read size, sample count and pattern produce the same
offset plan. Keep those values fixed when comparing EMUS against an OS-mounted
SMB path. When `-local` is supplied, the benchmark now requires the mounted
baseline file size to match the server object's probed size exactly; a
different-size file is rejected before timing so a superficially valid read
plan cannot benchmark a different object by accident.

After both timed measurements, `-local` also performs distributed byte
comparisons across the deterministic plan (8 samples by default). This catches
a same-size but different mounted object without warming either side before
the timed pass. Use `-verify-baseline-samples N` to change the count or
`-verify-baseline-samples 0` to disable it.

This remains a bounded sampled identity check, not a cryptographic proof that
every byte of a multi-gigabyte image is identical. The server ETag binds the
HTTP side to the object probed at the start of the run; the sampled comparison
is an additional guard against accidentally benchmarking a different same-size
mounted file.

Three deterministic access patterns are available:

- `random` — independent offsets; preserves the original benchmark behavior;
- `sequential` — contiguous block-sized reads, wrapping only at the file end;
- `clustered` — deterministic groups of eight contiguous reads whose starting
  points are pseudo-random.

The clustered pattern is intended to exercise emulator read-ahead without
pretending that one synthetic workload represents every core. Record the
pattern with benchmark results.

For before/after comparisons across server/client builds, freeze the exact
logical read plan:

```bash
go run ./cmd/bench ... -pattern clustered -plan-out ps1-clustered.json
go run ./cmd/bench ... -plan-in ps1-clustered.json
```

The saved plan is strict, versioned JSON containing the probed file size,
ETag, read size, seed, pattern and every logical range. Current schema 2 replay
requires both the recorded size and ETag to match the probed server object, so
same-size content replacement cannot silently reuse an old plan. Loading also
rejects unknown fields, additional/trailing JSON or garbage, and files larger
than 16 MiB. Legacy schema 1 plans remain readable as size-bound inputs and emit
a warning; combining `-plan-in` with `-plan-out` normalizes them to schema 2
with the current ETag.

## Recommended benchmark record

Record at least:

- SERVER-EMUS-PS5 commit;
- client/PS5 runtime commit when applicable;
- server OS and storage type;
- network link speed and topology;
- file format and size;
- read size, sample count, seed, access pattern and HTTP batch size;
- whether the OS-mounted baseline is SMB/NFS/local;
- raw JSON output.

Physical PS5 measurements remain a separate validation gate.


## Connection reuse evidence

The HTTP result records `fresh_connections`, `reused_connections` and
`connection_reuse_percent` using Go's client transport trace. Plain-text
output prints the same evidence when an HTTP connection sample exists. This
makes the persistent-connection assumption
measurable instead of implicit: a benchmark that unexpectedly reconnects for
every Range request is visible in the JSON result and should not be compared as
if it were using the intended steady-state transport.

These counters describe the benchmark client's HTTP connection behavior only.
They do not prove lower latency than SMB and do not replace physical-PS5
transport measurements.
