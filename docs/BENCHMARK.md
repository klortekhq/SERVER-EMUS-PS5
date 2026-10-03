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
  -local /mnt/smb/PS1/Game.chd \
  -json
```

The current first gate is intended for a trusted benchmark instance without a
bearer token. Authentication support can be added without changing the read
plan or result schema.

Reported fields include:

- request count;
- bytes transferred;
- total elapsed time;
- MiB/s;
- p50 latency;
- p95 latency;
- maximum latency.

The HTTP side probes the file with `HEAD`, requires `Accept-Ranges: bytes`
and an ETag, then sends each read with both `Range` and `If-Match`. That
prevents a benchmark from silently continuing against a file that changed
during the sample.

The same seed, file size, read size and sample count produce the same offset
plan. Keep those values fixed when comparing EMUS against an OS-mounted SMB
path.

## Recommended benchmark record

Record at least:

- SERVER-EMUS-PS5 commit;
- client/PS5 runtime commit when applicable;
- server OS and storage type;
- network link speed and topology;
- file format and size;
- read size, sample count and seed;
- whether the OS-mounted baseline is SMB/NFS/local;
- raw JSON output.

Physical PS5 measurements remain a separate validation gate.
