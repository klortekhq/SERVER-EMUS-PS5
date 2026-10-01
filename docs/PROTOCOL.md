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
`anchored_virtual_sidecars`, `catalog_discovery`, `catalog_etag` y
`catalog_rebuild`.

`catalog_rebuild` solo es `true` cuando hay bearer token configurado. El
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

### Rutas virtuales y sidecars

Para un CUE catalogado como `Juego.cue`, el arranque puede usar:

~~~text
emus://192.168.1.50:8787/<cue-id>/Juego.cue
~~~

Si el CUE abre `tracks/track01.bin`, el VFS conserva el mismo ID ancla y solicita virtualmente `tracks/track01.bin`. El servidor resuelve esa ruta desde el directorio del CUE y rechaza cualquier resolución que salga de la biblioteca, también a través de symlinks. El mismo mecanismo cubre listas M3U y formatos con archivos compañeros.

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

Un cliente no debe asumir que toda lectura devuelve el tamaño solicitado:
EOF, cambios en el archivo o un error de red pueden producir una lectura más corta.

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
`anchored_virtual_sidecars`, `catalog_discovery`, `catalog_etag`, and
`catalog_rebuild`.

`catalog_rebuild` is true only when a bearer token is configured. PS5 clients
should negotiate these capabilities instead of assuming optional extensions
from the port or service name.


~~~http
GET /api/v1/systems
GET /api/v1/games?system=ps1
~~~

Game entries expose only catalog metadata and never the host physical path. Configured extensions control launchable catalog entries; descriptor sidecars do not need to be listed as launchable extensions.

### Virtual paths and sidecars

A descriptor keeps one opaque anchor ID. Relative files requested by the emulator are forwarded as virtual paths under that anchor. The server resolves them from the descriptor directory and rejects targets that escape the configured library, including symlink escapes. This supports multi-file and multi-disc layouts without adding sidecar files to the games catalog.

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

Clients must allow short reads at EOF and treat an ETag change as file
invalidation.

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