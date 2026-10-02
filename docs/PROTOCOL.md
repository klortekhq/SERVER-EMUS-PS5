# Protocolo EMUS v1 / EMUS Protocol v1

[Español](#español) · [English](#english)

---

## Español

El protocolo de transporte de SERVER-EMUS-PS5 es HTTP/1.1 deliberadamente
simple. Los clientes nativos pueden representar un archivo remoto con este URI:

~~~text
emus://192.168.1.50:8787/<file-id>/Juego.chd
~~~

ps5rt lo traduce internamente a:

~~~text
http://192.168.1.50:8787/api/v1/files/<file-id>
~~~

El ID procede del catálogo y no contiene la ruta física del servidor. El sufijo es una **ruta virtual relativa** anclada al directorio de ese ID. `ps5rt` la codifica al transportar la petición y el servidor la resuelve únicamente dentro de la misma biblioteca configurada. Esto permite que CUE/CCD/TOC/M3U abran BIN/IMG/SUB/discos relativos sin SMB ni rutas físicas del NAS.

### Descubrimiento

`GET /api/v1/health` devuelve `service`, `api` y un bloque
`capabilities`. En v1 se anuncian explícitamente `byte_ranges`,
`multi_ranges`, `max_range_parts`, `max_range_header_bytes`, `max_virtual_path_bytes`,
`anchored_virtual_sidecars`,
`catalog_discovery`, `catalog_etag`,
`catalog_rebuild`, `transport_metrics`, `web_admin` y, cuando hay token,
`transport_metrics_reset`.

`catalog_rebuild` solo es `true` cuando hay bearer token configurado.
`library_editing` requiere además un archivo de configuración gestionable. El
cliente PS5 debe negociar estas capacidades en lugar de asumir extensiones por
el puerto o el nombre del servicio.


~~~http
GET /api/v1/systems
GET /api/v1/games?system=ps1
~~~

El segundo endpoint devuelve entradas con:

- id
- library
- system
- name
- relative_path
- size
- modified_at
- etag

Nunca devuelve la ruta física real. Las extensiones configuradas determinan qué archivos aparecen como juegos lanzables; los sidecars referenciados por un descriptor no necesitan aparecer en el catálogo.

#### Metadatos opcionales del juego

Junto a un archivo lanzable, el administrador puede crear un sidecar
`<archivo-lanzable>.emus.json` (por ejemplo, `Juego.iso.emus.json`). Es
opcional y permanece en la biblioteca del usuario; nunca se copia al
repositorio ni se añade a una release. El endpoint de juegos lo incluye como
`metadata`. Puede contener título, región, desarrollador/editor, géneros,
referencias HTTPS, identificadores específicos de esa consola, medios y
manifiestos de cheats. Un identificador PS3 puede declarar su clase como
`disc_serial`; otros sistemas pueden usar el tipo que requiera su formato
(por ejemplo CRC o product code). Los valores se conservan tal como se
proporcionan y no se infiere equivalencia entre plataformas.

```json
{
  "title": "Juego de ejemplo",
  "identifiers": [
    {"kind": "disc_serial", "value": "BLES12345", "region": "Europe"}
  ],
  "references": [
    {"label": "Referencia", "url": "https://example.org/game"}
  ],
  "artwork": [
    {
      "kind": "box_front",
      "path": "art/front.png",
      "sha256": "0000000000000000000000000000000000000000000000000000000000000000",
      "license": "user-provided",
      "attribution": "aportado por el usuario"
    }
  ],
  "cheats": [
    {
      "format": "libretro-cht",
      "path": "cheats/game.cht",
      "game_version": "1.00",
      "emulator": "core compatible",
      "license": "CC-BY-SA-4.0",
      "attribution": "autor o fuente original"
    }
  ]
}
```

Arte y cheats locales se indican como rutas virtuales relativas al directorio
del juego y pueden leerse solo bajo el mismo ID ancla mediante el endpoint de
archivo existente; el cliente debe codificar la ruta como query `path`. El
servidor no sigue enlaces fuera de la biblioteca. También se admiten URLs
HTTPS para recursos externos, pero el servidor solo las publica: no las
descarga ni las ejecuta. La interfaz del emulador debe pedir al usuario
seleccionar los cheats y comprobar que el formato y la versión de juego/core
coinciden antes de aplicarlos.

Los sidecars se limitan a 64 KiB, validan cantidades y tamaños de campos,
requieren URL HTTPS para referencias externas y forman parte del ETag del
catálogo. Un JSON incorrecto o un recurso con ruta no relativa hace que el
rescan falle de forma explícita; tras corregir el sidecar se puede reconstruir
el catálogo.

### Rutas virtuales y sidecars

Para un CUE catalogado como `Juego.cue`, el arranque puede usar:

~~~text
emus://192.168.1.50:8787/<cue-id>/Juego.cue
~~~

Si el CUE abre `tracks/track01.bin`, el VFS conserva el mismo ID ancla y solicita virtualmente `tracks/track01.bin`. El servidor resuelve esa ruta desde el directorio del CUE y rechaza cualquier resolución que salga de la biblioteca, también a través de symlinks. El mismo mecanismo cubre listas M3U y formatos con archivos compañeros.

### WebUI administrativa

~~~http
GET /admin/
~~~

La WebUI es una shell HTML/JS estática integrada en el binario. No recibe ni
inyecta el token del servidor y no contiene rutas físicas. Puede cargarse sin
Authorization incluso cuando la API está protegida.

Si el servidor requiere bearer token, el administrador lo introduce en la
página. El navegador lo conserva únicamente en `sessionStorage` de la pestaña
y lo añade como cabecera `Authorization` a las llamadas `/api/`. Cerrar la
pestaña elimina ese almacenamiento de sesión.

La disponibilidad pública del HTML **no** relaja la autenticación de la API:
`/api/` sigue pasando por el mismo middleware bearer. La respuesta añade CSP,
`Cache-Control: no-store`, `X-Content-Type-Options: nosniff` y
`Referrer-Policy: no-referrer`.

### Administración de bibliotecas

Cuando `capabilities.library_editing` es `true`, una herramienta de
administración autenticada puede reemplazar la lista completa:

~~~http
PUT /api/v1/admin/libraries
Authorization: Bearer <token>
Content-Type: application/json

{
  "libraries": [
    {
      "name": "PS1",
      "system": "ps1",
      "path": "/mnt/user/Multimedia/RomsRVK/PS1",
      "recursive": true,
      "extensions": [".cue", ".chd", ".m3u"]
    }
  ]
}
~~~

Las rutas físicas solo existen en la petición administrativa y en el
`config.json` local. La respuesta contiene únicamente estadísticas seguras
de bibliotecas/sistemas y el nuevo `catalog_etag`.

La operación es de sustitución completa: todas las carpetas se validan y
preindexan antes de reemplazar el catálogo vivo. Una petición inválida no
cambia ni el catálogo activo ni la configuración persistida.

### Apertura

El cliente obtiene tamaño y ETag con:

~~~http
HEAD /api/v1/files/<id>
~~~

Respuesta esperada:

~~~text
200 OK
Content-Length: ...
Accept-Ranges: bytes
ETag: "..."
~~~

### Lectura aleatoria

~~~http
GET /api/v1/files/<id>
Range: bytes=1048576-1572863
If-Range: "<etag>"
~~~

Respuesta normal:

~~~text
206 Partial Content
Content-Range: bytes 1048576-1572863/TOTAL
Content-Length: 524288
~~~

Para una sesión de emulación que exige que la imagen remota no cambie durante
la ejecución, el cliente puede usar `If-Match: "<etag>"` junto con `Range`.
Si el ETag ya no coincide, el servidor responde `412 Precondition Failed`
sin convertir la petición en una descarga completa. Esto evita el fallback
estándar de `If-Range`, que ante un validador obsoleto puede responder `200`
con el archivo entero. La capacidad se anuncia como
`strict_etag_preconditions` en `/api/v1/health`.

### Lectura vectorizada con multi-range

EMUS v1 también acepta el mecanismo HTTP estándar de rangos múltiples:

~~~http
GET /api/v1/files/<id>
Range: bytes=0-65535,1048576-1114111
If-Range: "<etag>"
~~~

Cuando ambos rangos son válidos, la respuesta es `206 Partial Content` con
`Content-Type: multipart/byteranges`. Esto permite que un cliente agrupe
lecturas discontiguas/prefetch en un solo round-trip sin introducir un segundo
protocolo binario propietario.

El servidor limita cada petición a **16 rangos** y el campo `Range` a **8192 bytes** para acotar memoria/CPU de parsing. Un campo `Range` que exceda ese límite recibe `431 Request Header Fields Too Large` antes de procesar rangos.
La capacidad se anuncia como `multi_ranges: true` y el límite actual como
`max_range_parts: 16`. Un cliente debe seguir aceptando el camino normal de
un solo Range y no asumir que multi-range existe sin negociar `/health`.

Un cliente no debe asumir que toda lectura devuelve el tamaño solicitado:
EOF, cambios en el archivo o un error de red pueden producir una lectura más corta.

### Métricas de transporte

~~~http
GET /api/v1/metrics
~~~

Devuelve contadores acumulados desde el arranque. `range_requests` cuenta intentos Range del cliente; `full_get_requests` cuenta respuestas GET completas `200 OK`. Una petición Range con `If-Range` obsoleto puede incrementar ambos contadores porque el servidor responde con el archivo completo.


- `uptime_seconds`;
- `file_get_requests`;
- `file_head_requests`;
- `range_requests` (intentos Range del cliente);
- `partial_content_responses` (respuestas 206 realmente emitidas);
- `multi_range_requests`;
- `range_header_rejections`;
- `full_get_requests`;
- `sidecar_requests`;
- `bytes_served`;
- `range_bytes_served`;
- `full_get_bytes_served`;
- `file_request_duration_us_total`;
- `file_request_duration_us_max`;
- `not_found`;
- `errors`.

La latencia media de archivo para una muestra se calcula como
`file_request_duration_us_total / (file_get_requests + file_head_requests)`
cuando el denominador es distinto de cero.

No incluye IDs de juegos, nombres ni rutas físicas. Su objetivo es comparar
patrones y coste de transporte antes/después de read-ahead o caché y durante
benchmarks EMUS vs SMB.

Con autenticación habilitada:

~~~http
POST /api/v1/admin/metrics/reset
Authorization: Bearer <token>
~~~

pone a cero todos los contadores de transporte para iniciar una nueva muestra.
Sin token configurado, el reset administrativo permanece deshabilitado.

### Autenticación

Cuando el servidor tiene token:

~~~http
Authorization: Bearer <token>
~~~

El token no debe formar parte del URI, del catálogo ni del nombre del archivo.

### Caché

El MVP usa ETag derivado de tamaño + fecha de modificación. Una futura caché de
bloques del cliente debe invalidarse si el ETag cambia.

### Seguridad

El servidor solo sirve IDs presentes en el catálogo. No existe un endpoint que
acepte una ruta física arbitraria.

---

## English

SERVER-EMUS-PS5 deliberately uses a small HTTP/1.1 transport. Native clients
may represent a remote file with:

~~~text
emus://192.168.1.50:8787/<file-id>/Juego.chd
~~~

ps5rt translates it internally to:

~~~text
http://192.168.1.50:8787/api/v1/files/<file-id>
~~~

The ID comes from the catalog and does not expose a physical server path. The trailing suffix is a **virtual relative path** anchored at that catalog entry's directory. `ps5rt` transports it encoded and the server resolves it only inside the same configured library. This allows CUE/CCD/TOC/M3U content to open relative BIN/IMG/SUB/disc sidecars without SMB or NAS host paths.

### Discovery

`GET /api/v1/health` returns `service`, `api`, and a
`capabilities` object. v1 explicitly advertises `byte_ranges`,
`anchored_virtual_sidecars`, `catalog_discovery`, `catalog_etag`,
`catalog_rebuild`, `transport_metrics`, `web_admin`, and, when a token is
configured, `transport_metrics_reset`.

`catalog_rebuild` is true only when a bearer token is configured.
`library_editing` additionally requires a managed configuration path. PS5
clients should negotiate these capabilities instead of assuming optional extensions
from the port or service name.


~~~http
GET /api/v1/systems
GET /api/v1/games?system=ps1
~~~

Game entries expose only catalog metadata and never the host physical path. Configured extensions control launchable catalog entries; descriptor sidecars do not need to be listed as launchable extensions.

### Virtual paths and sidecars

A descriptor keeps one opaque anchor ID. Relative files requested by the emulator are forwarded as virtual paths under that anchor. The server resolves them from the descriptor directory and rejects targets that escape the configured library, including symlink escapes. This supports multi-file and multi-disc layouts without adding sidecar files to the games catalog.

### Administration WebUI

~~~http
GET /admin/
~~~

The WebUI is a dependency-free static HTML/JS shell embedded in the server
binary. It receives no server-side token and contains no physical host paths.
The HTML itself remains loadable when API authentication is enabled.

When a bearer token is configured, the administrator enters it in the page.
The browser keeps it only in the tab's `sessionStorage` and attaches it as the
normal `Authorization` header to `/api/` requests. Closing the tab drops that
session storage.

Serving the shell publicly does **not** relax API authentication: every
`/api/` route still passes through the same bearer middleware. The shell is
served with CSP, `Cache-Control: no-store`, `nosniff` and a no-referrer
policy.

### Library administration

When `capabilities.library_editing` is `true`, an authenticated management
client may replace the complete library set with
`PUT /api/v1/admin/libraries`.

Physical paths exist only in that authenticated request and in the local
`config.json`. Responses contain only path-free library/system statistics and
the new `catalog_etag`.

The operation is a complete replacement: every folder is validated and a full
replacement catalog is prepared before the live catalog changes. Invalid
requests leave both the active catalog and persisted configuration unchanged.

### Open

Clients obtain size and ETag with:

~~~http
HEAD /api/v1/files/<id>
~~~

Expected response:

~~~text
200 OK
Content-Length: ...
Accept-Ranges: bytes
ETag: "..."
~~~

### Random access

~~~http
GET /api/v1/files/<id>
Range: bytes=1048576-1572863
If-Range: "<etag>"
~~~

Normal response:

~~~text
206 Partial Content
Content-Range: bytes 1048576-1572863/TOTAL
Content-Length: 524288
~~~

For an emulation session that requires the remote image to remain unchanged,
the client can send `If-Match: "<etag>"` together with `Range`. A stale ETag
returns `412 Precondition Failed` without falling back to a complete file
transfer. This avoids the standard `If-Range` fallback where a stale validator
may produce a full `200` response. Clients can negotiate this behavior through
the `strict_etag_preconditions` health capability.

Clients must allow short reads at EOF and treat an ETag change as file
invalidation.

### Transport metrics

~~~http
GET /api/v1/metrics
~~~

The file endpoint limits a request to 16 byte ranges and limits the `Range` header field to 8192 bytes; larger values receive `431 Request Header Fields Too Large` before range parsing.

The endpoint exposes cumulative counters since process start: uptime, file
GET/HEAD requests, Range attempts, actual 206 Partial Content responses, successful full 200 GET responses, sidecar requests, total bytes,
Range/full-GET bytes, Range-header rejections, cumulative/max file-request latency in microseconds, 404s
and errors. A Range request with a stale `If-Range` validator may increment both the Range-attempt counter and the full-GET counter because the emitted response is a complete `200 OK`. It contains no game IDs, names or physical paths.

Average file-request latency for a sample is
`file_request_duration_us_total / (file_get_requests + file_head_requests)`
when the denominator is non-zero.

These counters are intended to quantify request/byte behavior before and after
read-ahead/cache changes and during EMUS-vs-SMB benchmarks.

With authentication enabled,
`POST /api/v1/admin/metrics/reset` clears all transport counters so each
benchmark can start from zero. The reset endpoint stays disabled when no bearer
token is configured.

### Authentication

When configured, requests carry:

~~~http
Authorization: Bearer <token>
~~~

Tokens never belong in URIs, catalogs or game manifests.

### Cache

The MVP ETag is derived from size + modification time. A future client-side
block cache must invalidate blocks when the ETag changes.

### Security

Only catalog IDs can be opened. There is no arbitrary host-path endpoint.
