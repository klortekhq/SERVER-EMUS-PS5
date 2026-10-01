# SERVER-EMUS-PS5

Portable library and random-access streaming server for the native PS5 emulator family.

> **Estado / Status: MVP funcional · 55%**

[Español](#español) · [English](#english)

---

## Español

SERVER-EMUS-PS5 es el servidor compañero de
[NATIVE-EMUS-PS5](https://github.com/klortekhq/NATIVE-EMUS-PS5).

Su objetivo es sustituir, cuando convenga, el montaje SMB tradicional por un
protocolo mucho más pequeño y específico para emulación:

~~~text
carpeta local/NAS
      ↓
SERVER-EMUS-PS5
      ↓
catálogo JSON + HTTP Range
      ↓
ps5rt::RandomAccessReader
      ↓
emulador nativo PS5
~~~

No obliga a copiar una imagen completa a la consola. El emulador puede pedir
rangos de bytes concretos, igual que haría con un archivo local.

### Objetivos

- un solo binario portable;
- Linux / Unraid / NAS x86-64 y ARM64;
- Windows x86-64/ARM64;
- macOS Intel/Apple Silicon;
- varias carpetas por sistema;
- una carpeta puede estar en disco local, array, USB o almacenamiento montado por el SO;
- catálogo común para todas las consolas;
- sidecars relativos de CUE/CCD/TOC/M3U sin mostrarlos como juegos;
- acceso aleatorio con peticiones HTTP Range;
- HEAD para tamaño/ETag sin transferir datos;
- rutas físicas nunca expuestas al cliente;
- token opcional;
- sin BIOS, ROMs o juegos incluidos;
- preparado para que ps5rt lo consuma sin depender de SMB.

### ¿Será más rápido que SMB?

No se promete que HTTP sea mágicamente más rápido en todas las redes. La
ventaja es que este protocolo está pensado exclusivamente para lo que necesitan
los emuladores:

- sin negociación de shares;
- sin semántica de filesystem remoto que no usamos;
- IDs de archivo ya resueltos;
- lecturas directas por rango;
- conexiones persistentes;
- posibilidad de prefetch y caché específicos por emulador.

En una LAN rápida puede reducir bastante el overhead y, sobre todo, hace el
cliente PS5 mucho más simple. El rendimiento real se medirá y se comparará
contra SMB antes de declararlo superior.

### Configuración

config.example.json:

~~~json
{
  "listen": "0.0.0.0:8787",
  "token": "",
  "libraries": [
    {
      "name": "PS1",
      "system": "ps1",
      "path": "/mnt/user/Multimedia/RomsRVK/PS1",
      "recursive": true
    }
  ]
}
~~~

En Windows el mismo campo puede ser, por ejemplo:

~~~json
"path": "D:\\Roms\\PS1"
~~~

### API v1

- GET /api/v1/health
- GET /api/v1/systems
- GET /api/v1/libraries
- GET /api/v1/games?system=ps1
- GET /api/v1/files/{id}
- HEAD /api/v1/files/{id}
- Range: bytes=start-end soportado por el endpoint de archivo

El ID es un hash estable derivado de la biblioteca y la ruta relativa. El sufijo
opcional de `emus://host/id/ruta` se usa como ruta virtual relativa para
sidecars del descriptor. El servidor la resuelve dentro de la misma biblioteca
y rechaza escapes, incluidos los producidos por symlinks. El cliente nunca
puede solicitar una ruta física arbitraria del host.

Especificación completa: [docs/PROTOCOL.md](docs/PROTOCOL.md).

### Ejecutar

~~~bash
go run ./cmd/server -config ./config.json
~~~

Build portable:

~~~bash
go build -trimpath -ldflags="-s -w" -o server-emus ./cmd/server
~~~

---

## English

SERVER-EMUS-PS5 is the companion server for
[NATIVE-EMUS-PS5](https://github.com/klortekhq/NATIVE-EMUS-PS5).

Its goal is to replace traditional SMB mounts where useful with a much smaller
emulation-specific protocol:

~~~text
local/NAS folder
      ↓
SERVER-EMUS-PS5
      ↓
JSON catalog + HTTP Range
      ↓
ps5rt::RandomAccessReader
      ↓
native PS5 emulator
~~~

The console does not need to download a complete image first. Emulator clients
can request exact byte ranges just like a local random-access file.

### Goals

- one portable binary;
- Linux / Unraid / NAS on x86-64 and ARM64;
- Windows x86-64/ARM64;
- macOS Intel/Apple Silicon;
- multiple folders per system;
- common catalog for every console/system;
- relative CUE/CCD/TOC/M3U sidecars without listing them as games;
- random access using HTTP Range;
- HEAD for size/ETag without transferring content;
- never expose physical host paths to clients;
- optional bearer token;
- no BIOS, ROMs or games included;
- designed for direct ps5rt integration without SMB.

### Will it be faster than SMB?

HTTP is not assumed to be magically faster on every network. The advantage is
that this protocol is deliberately smaller than a remote filesystem:

- no share negotiation;
- no unused remote-filesystem semantics;
- pre-resolved file IDs;
- direct byte-range reads;
- persistent connections;
- emulator-specific prefetch and cache can be added later.

It should reduce protocol overhead and greatly simplify the PS5 client. Actual
throughput and random-read latency will be benchmarked against SMB before any
performance claim is made.

### Configuration

See config.example.json. Paths use the host operating system's native path syntax.

### API v1

- GET /api/v1/health
- GET /api/v1/systems
- GET /api/v1/libraries
- GET /api/v1/games?system=ps1
- GET /api/v1/files/{id}
- HEAD /api/v1/files/{id}
- file endpoint supports standard Range: bytes=start-end

File IDs are stable hashes derived from the library and relative path. The
optional `emus://host/id/path` suffix is an anchored virtual path used for
descriptor sidecars. The server resolves it only inside the same configured
library and rejects path/symlink escapes. Clients cannot request arbitrary host
paths.

Full specification: [docs/PROTOCOL.md](docs/PROTOCOL.md).

### Run

~~~bash
go run ./cmd/server -config ./config.json
~~~

Portable build:

~~~bash
go build -trimpath -ldflags="-s -w" -o server-emus ./cmd/server
~~~

## Repository policy

Only main is used for project development. Configuration files containing
private paths/tokens and copyrighted content must never be committed.

---

## Estado verificado / Verified state

GitHub Actions run **36839534929** passes tests and builds on Linux, Windows and
macOS.

Portable-build run **36839534917** produces six standalone artifacts:

- Linux amd64
- Linux arm64
- Windows amd64
- Windows arm64
- macOS amd64
- macOS arm64

The read-only, path-free library metadata API is implemented. The remaining
MVP management work is the authenticated library-editing UI/API, physical-PS5
`emus://` validation, reconnect/prefetch tuning and real LAN benchmarks
against SMB. Anchored descriptor sidecars are now implemented so CUE/CCD/TOC/M3U
layouts can stay portable without polluting the games catalog.
