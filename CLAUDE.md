# tclaw — Terminal proxy para Claude Code (y Codex)

## Qué es

Servidor Go que lanza **Claude Code** o **Codex** dentro de un **PTY** y los expone como una terminal real en el browser, controlable desde cualquier dispositivo (Mac, Windows, iPhone). Sin API keys extra para el harness, sin SDK — usa tu suscripción Claude Max / Codex directamente.

## Arquitectura (PTY directo)

```
Browser (Vue + xterm.js) ←WebSocket binario→ tclaw (Go) ←PTY (creack/pty)→ claude / codex
```

- Cada sesión es un proceso `claude`/`codex` lanzado **directamente** con `pty.Start()` — sin tmux ni multiplexor en medio.
- Una goroutine lectora drena el PTY y **hace broadcast de los bytes crudos** a todos los clientes WebSocket conectados a esa sesión, en cuanto el harness produce salida (sin polling).
- El frontend usa **xterm.js**: recibe los bytes crudos y los renderiza como una terminal real (colores ANSI, cursor, alt-screen, todo nativo).
- Cada sesión mantiene un **ring buffer de 256KB** con la salida reciente; al conectar (o reconectar) un cliente, se le manda ese buffer para que xterm.js repinte el estado actual.
- Múltiples clientes pueden conectarse a la **misma sesión** simultáneamente.
- El input del browser (teclado, botones) viaja como **frames binarios** = bytes crudos que se escriben directo al PTY. El `resize` viaja como un frame de texto JSON.

### Historia: por qué se migró desde tmux

La versión anterior usaba `tmux` como proxy de texto (`send-keys` / `capture-pane`, polling cada 200ms, snapshots completos). Funcionaba, pero el browser no interpretaba bien las secuencias ANSI y no había resize real. Se migró a PTY directo + xterm.js: render nativo de la terminal, resize dinámico y menor latencia (sin polling).

**Trade-off aceptado:** con PTY directo el harness es proceso hijo de tclaw. Si tclaw se reinicia o cae (deploy, `systemctl restart`, crash), **todas las sesiones mueren**. Una desconexión del WebSocket (cerrar el browser, perder señal) **no** mata la sesión — el harness sigue vivo y el ring buffer cubre la reconexión. El transcript de Claude Code igual queda en disco (`~/.claude/projects/...`), recuperable con `claude --resume`.

## Estructura del proyecto

```
/Users/lgm/Sites/tclaw/
├── main.go           — HTTP server, rutas, CORS, sirve frontend, carga .env y config
├── session.go        — sesión = proceso harness en un PTY: crear, leer, escribir, resize, matar
├── client.go         — conexión WebSocket individual: read/write pumps, frames binarios + control JSON
├── api.go            — endpoints REST + WebSocket handler + uploads + sanitización
├── config.go         — config persistente (config.json) con harness seleccionado
├── transcription.go  — cliente HTTP a OpenAI Whisper para transcribir audio subido
├── auth.go           — middleware de auth opcional por token (AUTH_TOKEN)
├── web/
│   └── index.html    — frontend Vue 3 (sin build) + xterm.js (CDN)
├── pty-experiment/   — PoC original de la migración a PTY (módulo Go aparte, histórico)
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
DELETE /api/sessions/:name                  → cerrar sesión (mata el proceso harness)
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
- **Frame binario** → bytes crudos que se escriben directo al PTY (keystrokes, secuencias ANSI, paste).
- **Frame de texto JSON** → mensaje de control:
  ```json
  { "type": "resize", "cols": 120, "rows": 40 }
  ```

**tclaw → Browser:**
- **Frame binario** → bytes crudos de salida del PTY. Al conectar, el primer frame es el ring buffer de replay. xterm.js los escribe tal cual.

## Cómo correr

```bash
cd /Users/lgm/Sites/tclaw
./build.sh local      # → binario ./tclaw
./tclaw
# Abre http://localhost:8080
```

Para exponerlo a un teléfono en pruebas:

```bash
cloudflared tunnel --url http://localhost:8080
```

Ya **no** se necesita configurar `~/.tmux.conf` — no hay tmux.

## Frontend (web/index.html)

Vue 3 sin build (importmap desde unpkg) + **xterm.js** cargado por `<script>` desde CDN (jsdelivr). Optimizado para móvil.

Funcionalidades:
- **Terminal xterm.js**: render real de la salida del harness, con `FitAddon` (ajusta cols/rows al tamaño del contenedor y reporta el resize al backend) y `WebLinksAddon` (URLs clicables).
- **Selector de harness** (claude / codex) en el header — persiste vía `/api/config`.
- **Barra de sesiones** con botón `+ new` y `×` para cerrar.
- **Modal de nueva sesión** que pide el path del directorio.
- **Modal de token** para auth (si el server tiene `AUTH_TOKEN`).
- **Input de escritorio**: `term.onData` manda cada keystroke (y el paste de xterm) como frame binario al PTY.
- **Composer móvil** (textarea + Send) para teclados táctiles. Manda el texto vía bracketed paste.
- **Action set "main"**: Clear, Img (subir imagen), Mic (grabar audio).
- **Action set "nav"**: arrow pad ↑↓←→, Enter, Tab, Esc, ⌫, Del, Ctrl+C/B/D/L/U/W. Se alternan con un botón ⇄.
- **Subida de imágenes**: tras subirla manda al harness `@<full_path> ` (sin Enter, para que el usuario complete el prompt).
- **Grabación de voz**: `MediaRecorder` (webm/opus), sube el blob, espera la transcripción y la manda como `<transcript>...</transcript>`.
- **Indicador de estado**: connected / disconnected.
- **Reconexión WebSocket** automática con backoff exponencial + reintento inmediato en `visibilitychange` / `online`.

## Dependencias

Go (`go.mod`):
- `github.com/creack/pty` — manejo del PTY
- `github.com/gorilla/websocket`
- `github.com/joho/godotenv`
- `golang.org/x/text`

Sistema:
- `claude` (Claude Code CLI) y/o `codex` instalados y autenticados — el harness se elige desde el frontend.
- `OPENAI_API_KEY` en `.env` si se quiere usar transcripción de audio (opcional). Modelo por defecto: `gpt-4o-mini-transcribe`, override con `TCLAW_TRANSCRIBE_MODEL`.
- **Ya no se necesita tmux.**

## Detalles técnicos

### Comandos del harness
`harnessCommandParts()` en `session.go` define cómo se lanza cada CLI:
- `claude` → `claude --dangerously-skip-permissions`
- `codex`  → `codex --dangerously-bypass-approvals-and-sandbox`

Los flags hacen que tclaw no tenga que mediar en cada prompt de permisos. Asume que ya confías en el directorio.

### Ciclo de vida de una sesión (`session.go`)
- `createSession` resuelve el dir, lanza `pty.Start(cmd)` con `TERM=xterm-256color`, registra la sesión y arranca dos goroutines:
  - `readLoop` — lee el PTY en chunks, lo mete al ring buffer y hace broadcast a los clientes.
  - `waitLoop` — `cmd.Wait()`; al morir el proceso, marca la sesión cerrada, cierra los clientes y la borra del mapa.
- `Write` escribe bytes crudos al PTY. `Resize` aplica `pty.Setsize`. `Close` mata el proceso (waitLoop hace el resto).

### Clientes WebSocket (`client.go`)
- `readPump`: frame binario → `session.Write`; frame de texto JSON `{type:"resize"}` → `session.Resize`.
- `writePump`: drena el canal `send` y escribe frames binarios al socket.
- `close()` está guardado con `sync.Once`: se llama desde `readPump` (desconexión) o desde `waitLoop` (muerte del proceso) sin riesgo de doble cierre.

### Sanitización de nombres de sesión
`sanitizeName()` convierte el dir en un nombre seguro: NFD normalize, strip combining marks (acentos/tildes), reemplaza `/`, `\`, espacios por `-`, deja solo `[a-z0-9-]`, colapsa hyphens, lowercase. Si queda vacío → `"session"`. El frontend duplica esta lógica en JS (`sanitizeSessionName`) para resolver colisiones 409.

### Directorios
`resolveDir()` strip leading slashes (`/Users/private` → `Users/private`), resuelve relativo al CWD de tclaw, y crea la carpeta con `MkdirAll` si no existe.

### Input: teclas y paste
- Los keystrokes de xterm.js (escritorio) van como bytes crudos directo al PTY.
- Los botones de la barra "nav" mandan secuencias ANSI crudas (`Up`→`\x1b[A`, `C-c`→`\x03`, etc., mapa `KEY_SEQ` en el frontend).
- El composer y los comandos (`/clear`, transcripciones) usan **bracketed paste** (`\x1b[200~ ... \x1b[201~`) para que los saltos de línea queden literales en el input box del harness, seguido de `\r` para enviar. El botón **NL** manda un bracketed paste de un solo `\n` sin enviar.

### Replay buffer
Cada sesión guarda los últimos 256KB de salida del PTY. Al conectar un cliente se le manda ese buffer; xterm.js procesa las secuencias y termina en el estado actual de pantalla. Esto cubre la reconexión sin necesidad de tmux.

### Uploads y transcripción
- Los uploads se guardan en `<session.Dir>/uploads/<filename>`. El nombre se sanitiza y se hace único añadiendo `-2`, `-3`, etc.
- `isAudioUpload()` detecta audio por content-type (`audio/...`) o extensión (`.webm .ogg .oga .mp3 .m4a .wav .mp4 .mpeg .mpga`).
- Si es audio, se llama a OpenAI `audio/transcriptions` y se devuelve el texto en `transcript`. El frontend lo manda como `<transcript>...</transcript>`.
- Si no hay `OPENAI_API_KEY`, el upload sigue funcionando para imágenes; para audio devuelve `transcription_error` pero el archivo queda en disco.

### Auth
`auth.go` — si `AUTH_TOKEN` está seteado, `/api/*` y `/ws/*` exigen el token (header `Authorization: Bearer`, header `X-Auth-Token` o query `?token=`). Comparación constant-time. Si no hay token configurado, todo es público.

## Pendientes / ideas futuras

- **PoC con dtach**: hacer una prueba de concepto lanzando el harness bajo `dtach` (un socket por sesión) para recuperar la persistencia ante reinicios de tclaw y la adopción de sesiones al arrancar, sin volver a tmux. tclaw seguiría usando `creack/pty` igual, solo cambiaría el comando que lanza (`dtach -A <sock> <harness>`).
- Confirmación / edición de transcripciones antes de mandarlas al agente, especialmente para SKUs / números / códigos donde STT suele fallar (`8.22.49` vs `82249`).
- Instrucción en el `CLAUDE.md` / `AGENTS.md` del proyecto destino para tratar bloques `<transcript>` como STT potencialmente imperfecto.
- Crons y queues usando `claude -p` / `codex` headless para tareas en background.
- Mover keyboard language y otras settings a una vista de configuración dedicada.

## Contexto del proyecto

Proyecto personal para controlar Claude Code / Codex desde el iPhone u otra máquina sin tener que dejar una terminal abierta. Empezó usando tmux como proxy de texto (validado en 5 minutos con 3 comandos manuales antes de escribir código) y luego se migró a PTY directo + xterm.js a partir del PoC en `pty-experiment/`. El frontend tiene grabación de voz con transcripción, subida de imágenes y soporte dual claude/codex.
