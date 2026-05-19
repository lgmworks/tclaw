# tclaw — Terminal proxy para Claude Code (y Codex)

## Qué es

Servidor Go que lanza **Claude Code** o **Codex** dentro de una **sesión tmux** y los expone como una terminal en el browser, controlable desde cualquier dispositivo (Mac, Windows, iPhone). Sin API keys extra para el harness, sin SDK — usa tu suscripción Claude Max / Codex directamente.

## Arquitectura (tmux)

```
Browser (Vue + ansi_up) ←WebSocket JSON→ tclaw (Go) ←tmux send-keys / capture-pane→ claude / codex
```

- Cada sesión es una **sesión de tmux** (`tmux new-session -d`) con el harness corriendo dentro.
- tclaw **no se adjunta** a tmux: lo usa como proxy de texto. Hay un `Hub` por sesión (`hub.go`) con una goroutine que hace **polling de `tmux capture-pane`** cada 200ms (rápido) / 2s (idle); cuando el snapshot cambia, lo difunde a los clientes WebSocket como JSON.
- El frontend usa **ansi_up**: convierte el snapshot (con códigos ANSI de color) a HTML y lo pinta en un `<div>` normal. El scroll es el **scroll nativo del navegador** — suave en móvil.
- El input del browser viaja como **JSON de control** y tclaw lo traduce a `tmux send-keys` / `paste-buffer`.
- Múltiples clientes pueden conectarse a la **misma sesión** simultáneamente (comparten el `Hub`).

### Por qué tmux y no PTY directo

Hubo una etapa con **PTY directo + xterm.js** (`pty.Start()`, stream de bytes crudos, render en canvas). Daba render ANSI perfecto y resize real, pero **el scroll de xterm.js en móvil era insoportable** (viewport propio en un canvas, sin scroll nativo). Se volvió a tmux para recuperar el render con ansi_up en un `<div>` con scroll nativo.

`ansi_up` no emula una terminal: solo colorea texto. Necesita un **snapshot plano de la pantalla**, que es justo lo que da `tmux capture-pane` (tmux hace de emulador). Por eso tmux y ansi_up van en paquete: no se pueden separar.

**Ventaja recuperada — persistencia:** las sesiones viven en el servidor tmux, no como hijos de tclaw. Si tclaw se reinicia o cae (deploy, `systemctl restart`, crash), **las sesiones siguen vivas**; `adoptExistingSessions()` las re-adopta al arrancar. Una desconexión del WebSocket tampoco mata nada.

**Trade-off aceptado:** sin emulador de terminal en el cliente, lo que se ve es lo que `capture-pane` aplana — el polling añade ~200ms de latencia y el diff de cambios solo compara las últimas 30 líneas.

## Estructura del proyecto

```
/Users/lgm/Sites/tclaw/
├── main.go           — HTTP server, rutas, CORS, sirve frontend, carga .env y config, adopta sesiones
├── session.go        — sesión = sesión tmux: crear, send-keys/paste, capture, resize, matar
├── hub.go            — un Hub por sesión: clientes WebSocket + loop de polling capture-pane (con pausa)
├── diff.go           — hasChanged (diff de las últimas 30 líneas) e isReady (¿harness esperando input?)
├── client.go         — conexión WebSocket individual: read/write pumps, mensajes JSON
├── api.go            — endpoints REST + WebSocket handler + uploads + sanitización
├── config.go         — config persistente (config.json): harness, default_session, exclude_dirs
├── transcription.go  — cliente HTTP a OpenAI Whisper para transcribir audio subido
├── auth.go           — middleware de auth opcional por token (AUTH_TOKEN)
├── web/
│   └── index.html    — frontend Vue 3 (sin build) + ansi_up (CDN)
├── pty-experiment/   — PoC histórico de la etapa PTY (módulo Go aparte)
├── deploy.sh         — cross-compila, sube binario + web/ al server openclaw, reinicia el servicio
├── build.sh          — ./build.sh [local|prod]
├── monet-setup.sh    — setup de una 2da instancia
├── config.example.json
├── config.json       — gitignored, persiste la config
├── .env              — gitignored, contiene OPENAI_API_KEY para transcripción
├── go.mod / go.sum
└── .gitignore
```

## API

### REST

```
GET    /api/sessions                        → lista sesiones activas
POST   /api/sessions                        → crear sesión { "dir": "mi-proyecto" }
                                              dir es relativo al CWD de tclaw, crea la carpeta si no existe
DELETE /api/sessions/:name                  → cerrar sesión (tmux kill-session)
POST   /api/sessions/:name/uploads          → subir imagen o audio (multipart, campo "file")
                                              audio se transcribe automáticamente vía OpenAI

GET    /api/config                          → { "harness", "default_session", "exclude_dirs" }
PUT    /api/config                          → cambiar harness, persiste en config.json
GET    /api/dirs                            → lista de carpetas-proyecto bajo el CWD de tclaw
```

### WebSocket

```
WS /ws/:session_name
```

**Browser → tclaw** (mensajes JSON de texto):

```json
{ "type": "input",  "text": "...", "submit_key": "Enter" }   paste de texto + tecla de envío
{ "type": "text",   "text": "..." }                          texto literal (tmux send-keys -l)
{ "type": "key",    "text": "C-c" }                           una tecla tmux (tmux send-keys)
{ "type": "resize", "cols": 120, "rows": 40 }                 resize del window de tmux
{ "type": "pause" }  /  { "type": "resume" }                  pausa / reanuda el polling
```

`submit_key: "none"` pega el texto **sin enviar** (lo usa el botón NL).

**tclaw → Browser** (mensajes JSON):

```json
{ "type": "snapshot", "text": "..." }                  pantalla completa (al conectar / al reanudar)
{ "type": "update",   "text": "...", "ready": true }   pantalla completa, cuando cambió
{ "type": "sync",     "paused": true }                 estado de la pausa del polling
```

## Cómo correr

```bash
cd /Users/lgm/Sites/tclaw
./build.sh local      # → binario ./tclaw
./tclaw
# Abre http://localhost:8080
```

Necesita **`tmux` instalado**. Opcional: configurar `~/.tmux.conf` para shift+enter (ver `README.md`).

Para exponerlo a un teléfono en pruebas:

```bash
cloudflared tunnel --url http://localhost:8080
```

## Frontend (web/index.html)

Vue 3 sin build (importmap desde unpkg) + **ansi_up** (CDN jsdelivr). Optimizado para móvil.

Funcionalidades:
- **Terminal ansi_up**: pinta el snapshot de `capture-pane` como HTML en un `<div>` con scroll nativo.
- **Vista congelada al leer scrollback**: si subes a leer (o seleccionas texto), la vista no se repinta — el snapshot nuevo se guarda en buffer y se aplica al volver al fondo. Evita que un update te tire al bottom mientras lees.
- **Botón pausar/reanudar sync**: detiene/reanuda el polling de `capture-pane` en el server.
- **Botón resize (Fit)**: manda `resize-window` de tmux al ancho real de la pantalla. También se dispara solo al conectar y al rotar el móvil.
- **Selector de harness** (claude / codex) en el header — persiste vía `/api/config`.
- **Barra de carpetas** con estados de color (parada / corriendo / activa) y routing por path (`/proyecto`).
- **Modal de nueva sesión** y **modal de token** (auth).
- **Composer móvil** (textarea + Send) para teclados táctiles.
- **Action set "main"**: Sync, Fit, Copy, Clear, Img, Mic. **Action set "nav"**: arrow pad, Enter, Tab, Esc, ⌫, Del, Ctrl+C/B/D/L/U/W. Se alternan con ⇄.
- **Subida de imágenes** (`@<full_path> `), **grabación de voz** (`MediaRecorder` → transcripción → `<transcript>...</transcript>`).
- **Reconexión WebSocket** automática con backoff exponencial + reintento en `visibilitychange` / `online`.

## Dependencias

Go (`go.mod`):
- `github.com/gorilla/websocket`
- `github.com/joho/godotenv`
- `golang.org/x/text`

Sistema:
- **`tmux`** — tclaw lanza y consulta cada sesión vía tmux.
- `claude` (Claude Code CLI) y/o `codex` instalados y autenticados — el harness se elige desde el frontend.
- `OPENAI_API_KEY` en `.env` si se quiere usar transcripción de audio (opcional). Modelo por defecto: `gpt-4o-mini-transcribe`, override con `TCLAW_TRANSCRIBE_MODEL`.

## Detalles técnicos

### Comandos del harness
`harnessCommandParts()` en `session.go` define cómo se lanza cada CLI:
- `claude` → `claude --dangerously-skip-permissions`
- `codex`  → `codex --dangerously-bypass-approvals-and-sandbox`

Los flags hacen que tclaw no tenga que mediar en cada prompt de permisos. El harness se arranca con `send-keys` dentro del shell de tmux, así que hereda el PATH/entorno del usuario.

### Ciclo de vida de una sesión (`session.go` + `hub.go`)
- `createSession` resuelve el dir, hace `tmux new-session -d`, `set-option window-size manual` (para que el resize aguante) y `send-keys` del comando del harness + `Enter`.
- Al conectar el primer cliente, `getOrCreateHub` crea el `Hub` y arranca `pollLoop`.
- `deleteSession` hace `tmux kill-session`; `removeHub` para el `pollLoop`.
- `adoptExistingSessions()` corre al arrancar tclaw: lista las sesiones tmux vivas y las registra (persistencia ante reinicios).

### Hub y polling (`hub.go`)
- `pollLoop` hace `capture-pane` cada `pollFast` (200ms) cuando hay actividad y `pollSlow` (2s) cuando el harness está idle. `inputCh` lo acelera tras un input.
- `captureAndBroadcast` compara con el snapshot previo (`diff.go`) y, si cambió, difunde un `update`.
- **Pausa**: `pauseCh` + `paused atomic.Bool`. Pausado, el loop no hace `capture-pane`. Al reanudar manda un `snapshot` fresco. El estado se difunde como `{type:"sync"}`.

### diff.go
- `hasChanged` compara las **últimas 30 líneas** de dos snapshots (barato; cambios típicos del harness son al final).
- `isReady` heurística: detecta si el harness está esperando input (prompt `❯`/`>`, "? for shortcuts").

### Clientes WebSocket (`client.go`)
- `readPump` parsea JSON y traduce: `input`→`SendInput`, `text`→`SendText`, `key`→`SendKey`, `resize`→`Resize`, `pause`/`resume`→`Hub.setPaused`.
- `writePump` drena el canal `send` y manda JSON de texto.

### Render del frontend (subtleza importante)
`ansi_up` arrastra estado de color SGR **entre llamadas** (está hecho para un stream). Como le pasamos un snapshot completo cada vez, se crea **una instancia nueva de `AnsiUp` por render** (`renderAnsi`) — si no, el color del final de un snapshot se filtra al inicio del siguiente.

El repintado reemplaza todo el `innerHTML` del `<div>`, lo que rompería scroll y selección. Por eso `applyOutput` **congela la vista** (guarda el snapshot en `pendingText`) mientras el usuario está leyendo scrollback o tiene texto seleccionado, y lo pinta (`flushPending`) al volver al fondo.

### Resize
`Session.Resize` hace `tmux resize-window -x cols -y rows`. El TUI del harness recibe SIGWINCH y reenvuelve a ese ancho; `capture-pane` lo refleja. `window-size manual` (seteado al crear) evita que tmux re-ajuste el window.

### Sanitización de nombres de sesión
`sanitizeName()` convierte el dir en un nombre seguro: NFD normalize, strip combining marks (acentos/tildes), reemplaza `/`, `\`, espacios por `-`, deja solo `[a-z0-9-]`, colapsa hyphens, lowercase. Si queda vacío → `"session"`. El frontend duplica esta lógica en JS (`sanitizeSessionName`) para resolver colisiones 409.

### Directorios
`resolveDir()` strip leading slashes (`/Users/private` → `Users/private`), resuelve relativo al CWD de tclaw, y crea la carpeta con `MkdirAll` si no existe. `/api/dirs` lista las carpetas-proyecto bajo el CWD, saltando ocultas y las de `exclude_dirs`.

### Input: teclas y paste
- Los botones de la barra "nav" mandan nombres de tecla de tmux (`Up`, `Enter`, `C-c`, `BSpace`, …) vía `send-keys`.
- El composer y los comandos (`/clear`, transcripciones) usan `paste-buffer` (`SendInput`): pega el texto y manda la tecla de envío. `submit_key: "none"` pega sin enviar (botón NL).
- `capture-pane` se invoca con `-e -J -S -1000`: colores ANSI, líneas unidas y 1000 líneas de scrollback.

### Uploads y transcripción
- Los uploads se guardan en `<session.Dir>/uploads/<filename>`. El nombre se sanitiza y se hace único añadiendo `-2`, `-3`, etc.
- `isAudioUpload()` detecta audio por content-type (`audio/...`) o extensión (`.webm .ogg .oga .mp3 .m4a .wav .mp4 .mpeg .mpga`).
- Si es audio, se llama a OpenAI `audio/transcriptions` y se devuelve el texto en `transcript`. El frontend lo manda como `<transcript>...</transcript>`.
- Si no hay `OPENAI_API_KEY`, el upload sigue funcionando para imágenes; para audio devuelve `transcription_error` pero el archivo queda en disco.

### Auth
`auth.go` — si `AUTH_TOKEN` está seteado, `/api/*` y `/ws/*` exigen el token (header `Authorization: Bearer`, header `X-Auth-Token` o query `?token=`). Comparación constant-time. Si no hay token configurado, todo es público.

## Pendientes / ideas futuras

- Confirmación / edición de transcripciones antes de mandarlas al agente, especialmente para SKUs / números / códigos donde STT suele fallar (`8.22.49` vs `82249`).
- Instrucción en el `CLAUDE.md` / `AGENTS.md` del proyecto destino para tratar bloques `<transcript>` como STT potencialmente imperfecto.
- Aviso visual ("↓ nuevo") cuando hay salida en buffer mientras la vista está congelada.
- Crons y queues usando `claude -p` / `codex` headless para tareas en background.
- Mover keyboard language y otras settings a una vista de configuración dedicada.

## Contexto del proyecto

Proyecto personal para controlar Claude Code / Codex desde el iPhone u otra máquina sin tener que dejar una terminal abierta. Empezó usando tmux como proxy de texto (validado en 5 minutos con 3 comandos manuales antes de escribir código), luego se migró a PTY directo + xterm.js a partir del PoC en `pty-experiment/`, y finalmente **se volvió a tmux** porque el scroll de xterm.js en móvil — que es donde se usa — era insoportable; se conservaron las features añadidas en la etapa PTY (barra de carpetas, routing por URL). El frontend tiene grabación de voz con transcripción, subida de imágenes y soporte dual claude/codex.
