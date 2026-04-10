# tclaw — Terminal proxy para Claude Code (y Codex)

## Qué es

Servidor Go que usa **tmux** como proxy de texto para controlar **Claude Code** o **Codex** desde cualquier dispositivo (Mac, Windows, iPhone) vía browser. Sin API keys extra para el harness, sin SDK — usa tu suscripción Claude Max / Codex directamente.

## Por qué tmux y no `-p`

- `-p` (modo no interactivo) recorta la experiencia del harness: pierde prompts de permisos, trust folder, menús interactivos, flechas, etc.
- tmux captura la **UI completa** del CLI tal como la verías en una terminal real.
- La sesión tmux **sobrevive** si tclaw se cae — solo reconectas.
- `-p` sí es útil para automatización futura (crons, queues) donde no se necesita interactividad.

## Arquitectura

```
Browser (Vue) ←WebSocket→ tclaw (Go) ←tmux CLI→ tmux sessions ←PTY→ claude / codex
```

- **tclaw** ejecuta `tmux send-keys` / `paste-buffer` para enviar input y `tmux capture-pane` para leer output.
- Cada **200ms** compara las últimas 30 líneas del pane; si cambiaron, manda el snapshot completo al browser como reemplazo (no es un diff incremental, es replace).
- Cuando detecta que el harness terminó (patrón `❯`, `>` o `? for shortcuts`), baja el polling a **2s**.
- Múltiples clientes pueden conectarse a la **misma sesión** simultáneamente vía un Hub por sesión.
- Al arrancar, **adopta sesiones tmux existentes** automáticamente leyendo `tmux list-sessions`.

## Estructura del proyecto

```
/Users/lgm/Sites/tclaw/
├── main.go           — HTTP server, rutas, CORS, sirve frontend, carga .env y config
├── session.go        — wrapper tmux: crear/adoptar/matar sesiones, send/paste/capture
├── hub.go            — hub por sesión: poll loop adaptativo, broadcast a clientes
├── client.go         — conexión WebSocket individual: read/write pumps, mensajes in/out
├── diff.go           — comparación de las últimas 30 líneas, detección de "ready"
├── api.go            — endpoints REST + WebSocket handler + uploads + sanitización
├── config.go         — config persistente (config.json) con harness seleccionado
├── transcription.go  — cliente HTTP a OpenAI Whisper para transcribir audio subido
├── web/
│   └── index.html    — frontend Vue 3 (sin build, importmap desde unpkg CDN)
├── config.example.json
├── config.json       — gitignored, persiste el harness elegido
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
DELETE /api/sessions/:name                  → cerrar sesión (mata tmux)
POST   /api/sessions/:name/uploads          → subir imagen o audio (multipart, campo "file")
                                              audio se transcribe automáticamente vía OpenAI

GET    /api/config                          → { "harness": "claude" | "codex" }
PUT    /api/config                          → cambiar harness, persiste en config.json
```

### WebSocket

```
WS /ws/:session_name
```

**Browser → tclaw:**
```json
{ "type": "text",  "text": "h" }                          // texto literal (tmux -l), por carácter desde el teclado
{ "type": "input", "text": "hola", "submit_key": "Enter" } // pega texto vía buffer + submit (Enter por defecto)
{ "type": "key",   "text": "Up" }                          // tecla tmux (Up, Down, Enter, Escape, C-c, etc.)
```

**tclaw → Browser:**
```json
{ "type": "snapshot", "text": "..." }                     // snapshot inicial al conectar
{ "type": "update",   "text": "...", "ready": true }      // reemplazo completo del pane
```

`text` es una sustitución completa del contenido del pane, no un diff. El frontend simplemente reemplaza el `<div>` de output.

## Cómo correr

```bash
cd /Users/lgm/Sites/tclaw
go build -o tclaw .
./tclaw
# Abre http://localhost:8080
```

Para exponerlo a un teléfono en pruebas:

```bash
cloudflared tunnel --url http://localhost:8080
```

Recomendado en `~/.tmux.conf` para que Shift+Enter funcione como nueva línea dentro del prompt:

```
set -s extended-keys always
set -as terminal-features 'xterm*:extkeys'
bind-key -n S-Enter send-keys Escape "[13;2u"
```

## Frontend (web/index.html)

Vue 3 sin build, todo en un único `index.html` (~1100 líneas, importmap desde unpkg CDN). Optimizado para móvil.

Funcionalidades:
- **Selector de harness** (claude / codex) en el header — persiste vía `/api/config`.
- **Barra de sesiones** con botón `+ new` y `×` para cerrar.
- **Modal de nueva sesión** que pide el path del directorio.
- **Output pane** con linkificación de URLs, scroll automático y `tabindex` para captar teclado.
- **Captura global de teclado**: cuando el output tiene foco, cada tecla se manda como `text` (caracteres) o `key` (combos / especiales). `toTmuxKey` convierte combinaciones a notación tmux (`C-c`, `M-x`, etc.).
- **Composer móvil** (textarea + Send) que se muestra/oculta con un botón ⌨ para teclados táctiles.
- **Action set "main"**: Clear, Img (subir imagen), Mic (grabar audio), ⌨ (composer).
- **Action set "nav"**: arrow pad ↑↓←→, Enter, C-c. Se alternan con un botón ⇄.
- **Subida de imágenes**: tras subirla manda al harness `@<full_path> ` (literal, sin Enter, para que el usuario complete el prompt).
- **Grabación de voz**: usa `MediaRecorder` (webm/opus), sube el blob, espera la transcripción. Si llega `transcript`, lo manda como `<transcript>...</transcript>` con Enter; si falla la transcripción, muestra el error.
- **Indicador de estado**: ready / busy / disconnected.

## Dependencias

Go (`go.mod`):
- `github.com/gorilla/websocket`
- `github.com/joho/godotenv`
- `golang.org/x/text`

Sistema:
- `tmux` instalado y en PATH.
- `claude` (Claude Code CLI) y/o `codex` instalados y autenticados — el harness se elige desde el frontend.
- `OPENAI_API_KEY` en `.env` si se quiere usar transcripción de audio (opcional). Modelo por defecto: `gpt-4o-mini-transcribe`, override con `TCLAW_TRANSCRIBE_MODEL`.

## Detalles técnicos

### Comandos del harness
`harnessCommandParts()` en `session.go` define cómo se lanza cada CLI:
- `claude` → `claude --dangerously-skip-permissions`
- `codex`  → `codex --dangerously-bypass-approvals-and-sandbox`

Los flags hacen que tclaw no tenga que mediar en cada prompt de permisos. Asume que ya confías en el directorio.

### Sanitización de nombres de sesión
`sanitizeName()` convierte el dir en un nombre tmux seguro: NFD normalize, strip combining marks (acentos/tildes), reemplaza `/`, `\`, espacios por `-`, deja solo `[a-z0-9-]`, colapsa hyphens, lowercase. Si queda vacío → `"session"`. El frontend duplica esta lógica en JS (`sanitizeSessionName`) para resolver colisiones 409.

### Directorios
`resolveDir()` strip leading slashes (`/Users/private` → `Users/private`), resuelve relativo al CWD de tclaw, y crea la carpeta con `MkdirAll` si no existe.

### Envío de texto a tmux
- **`SendText`** (`tmux send-keys -l`): texto literal, carácter a carácter — se usa para los keystrokes individuales del teclado del browser.
- **`PasteText`** (`tmux set-buffer` + `paste-buffer`): pega bloques largos vía un buffer llamado `tclaw-input`. Más rápido y seguro para texto multilínea o con caracteres especiales.
- **`SendInput`**: pega con `PasteText`, espera 300ms y envía la tecla submit (Enter por defecto). Usado por el composer móvil, transcripciones, `/clear`, etc.
- **`SendKey`** (`tmux send-keys`): teclas tmux nombradas (`Up`, `Down`, `Enter`, `Escape`, `C-c`, `BSpace`...).

### Detección de "harness listo"
`isReady()` en `diff.go` busca en la última línea no vacía del capture: `❯`, `>`, o un substring `? for shortcuts`. Cuando encuentra una de esas señales el polling baja a 2s.

### Captura del pane
`tmux capture-pane -t <name> -p -S -1000` — captura las últimas 1000 líneas del scrollback. El frontend recibe el bloque entero y lo reemplaza.

### Adopción de sesiones existentes
Al arrancar (`adoptExistingSessions`), tclaw lee `tmux list-sessions -F "#{session_name}:#{pane_current_path}"` y registra cada sesión. **No** lanza un nuevo harness en ellas — asume que ya tienen lo que tengan corriendo dentro.

### Trust folder
Cuando el harness arranca en una carpeta nueva por primera vez puede mostrar "Do you trust this folder?". Hay que usar el action set "nav" (↑↓ Enter) o el teclado del browser para aceptar.

### Uploads y transcripción
- Los uploads se guardan en `<session.Dir>/uploads/<filename>`. El nombre se sanitiza y se hace único añadiendo `-2`, `-3`, etc.
- `isAudioUpload()` detecta audio por content-type (`audio/...`) o extensión (`.webm .ogg .oga .mp3 .m4a .wav .mp4 .mpeg .mpga`).
- Si es audio, se llama a OpenAI `audio/transcriptions` con el modelo configurado y se devuelve el texto en `transcript`. El frontend lo manda como `<transcript>...</transcript>`.
- Si no hay `OPENAI_API_KEY`, el upload sigue funcionando para imágenes; para audio devuelve `transcription_error` pero el archivo queda en disco.

## Pendientes / ideas futuras (de TODOS.md y CLAUDE.md anterior)

- Auth (token simple para proteger acceso público vía cloudflared).
- Confirmación / edición de transcripciones antes de mandarlas al agente, especialmente para SKUs / números / códigos donde STT suele fallar (`8.22.49` vs `82249`).
- Instrucción en el `CLAUDE.md` / `AGENTS.md` del proyecto destino para tratar bloques `<transcript>` como STT potencialmente imperfecto.
- Crons y queues usando `claude -p` / `codex` headless para tareas en background.
- Persistir sesiones tclaw a disco (hoy solo se adoptan las que ya estén en tmux al arrancar).
- Mejor reconexión WebSocket automática en el frontend.
- Mover keyboard language y otras settings a una vista de configuración dedicada (hoy hay un `keyboard_language` en `config.json` que no está expuesto).

## Contexto del proyecto

Proyecto personal para controlar Claude Code / Codex desde el iPhone u otra máquina sin tener que dejar una terminal abierta. La prueba de concepto se validó en 5 minutos con 3 comandos manuales de tmux antes de escribir código. Hoy el frontend ya tiene grabación de voz con transcripción, subida de imágenes y soporte dual claude/codex.
