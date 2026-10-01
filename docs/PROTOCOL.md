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

El ID procede del catálogo y no contiene la ruta física del servidor. El sufijo de nombre es metadato para conservar la extensión que necesita el emulador; `ps5rt` no lo usa para resolver el archivo en el servidor.

### Descubrimiento

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

Nunca devuelve la ruta física real.

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

The ID comes from the catalog and does not expose a physical server path. The trailing file name is metadata used to preserve the extension expected by emulator cores; `ps5rt` never uses it to resolve the host file.

### Discovery

~~~http
GET /api/v1/systems
GET /api/v1/games?system=ps1
~~~

Game entries expose only catalog metadata and never the host physical path.

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