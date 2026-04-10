# tclaw — Terminal proxy para Claude Code

## Qué es

Un servidor Go que usa **tmux** como proxy de texto para controlar **Claude Code** desde cualquier dispositivo (Mac, Windows, iPhone) vía browser. Sin API keys extra, sin SDK — usa tu suscripción Claude Max directamente.

## Por qué tmux y no `-p`

- `-p` (modo no interactivo) recorta la experiencia de Claude Code: pierde prompts de permisos, trust folder, menús interactivos, flechas, etc.
- tmux captura la **UI completa** de Claude Code tal como la verías en una terminal
- La sesión tmux **sobrevive** si tclaw se cae — solo reconectas
- `-p` sí es útil para automatización futura (crons, queues) donde no se necesita interactividad

## Arquitectura

```
Browser (Vue) ←WebSocket→ tclaw (Go) ←tmux CLI→ tmux sessions ←PTY→ claude code
```

- **tclaw** ejecuta `tmux send-keys` para enviar input y `tmux capture-pane` para leer output
- Cada **200ms** compara las últimas 30 líneas del pane; si cambiaron, manda el snapshot completo al browser como reemplazo
- Cuando detecta que Claude terminó (patrón `❯`), baja el polling a **2s**
- Múltiples clientes pueden conectarse a la **misma sesión** simultáneamente
- Al arrancar, **adopta sesiones tmux existentes** automáticamente

## Estructura del proyecto

```
/Users/lgm/Sites/tclaw/
├── main.go        — HTTP server, rutas, CORS, sirve frontend
├── session.go     — wrapper tmux: crear, enviar, capturar, matar, adoptar sesiones
├── hub.go         — hub WebSocket: poll loop, broadcast a múltiples clientes
├── client.go      — conexión WebSocket individual, mensajes in/out
├── diff.go        — comparación de capturas (últimas 30 líneas), detección de "ready"
├── api.go         — endpoints REST + handler WebSocket
├── web/
│   └── index.html — frontend Vue 3 (sin build, importmap desde CDN)
├── go.mod
├── go.sum
└── .gitignore
```

## API

### REST

```
GET    /api/sessions          → lista sesiones activas
POST   /api/sessions          → crear sesión { "dir": "mi-proyecto" }
                                 dir es relativo al CWD de tclaw, crea la carpeta si no existe
DELETE /api/sessions/:name    → cerrar sesión (mata tmux)
```

### WebSocket

```
WS /ws/:session_name
```

**Browser → tclaw:**
```json
{ "type": "input", "text": "hola claude" }   // texto + Enter
{ "type": "key", "text": "Up" }              // tecla especial (Up, Down, Enter, Escape, y, n, Tab)
```

**tclaw → Browser:**
```json
{ "type": "snapshot", "text": "..." }                    // al conectar
{ "type": "update", "text": "...", "ready": true }       // reemplazo completo del pane
{ "type": "status", "ready": true }                      // Claude listo para input
```

## Cómo correr

```bash
cd /Users/lgm/Sites/tclaw
go build -o tclaw .
./tclaw
# Abre http://localhost:8080
```

## Frontend

- Vue 3 sin build (importmap desde unpkg CDN)
- Barra de sesiones con botón `+ new` y `×` para cerrar
- Barra de teclas: ↑ ↓ Enter Esc y n Tab (para prompts interactivos de Claude Code)
- Campo de texto + Send para mensajes normales
- Indicador de estado: ready / busy / disconnected

## Dependencias

- Go (stdlib + `github.com/gorilla/websocket` + `golang.org/x/text`)
- tmux (instalado en el sistema)
- Claude Code CLI (instalado y autenticado)

## Detalles técnicos

### Sanitización de nombres
El `dir` que manda el usuario se convierte en nombre de sesión tmux: strip tildes/acentos, reemplaza `/` y espacios por `-`, solo alfanumérico.

### Directorios relativos
El dir siempre es relativo al CWD de tclaw. Si mandas `/Users/private`, se transforma a `./Users/private` relativo a donde corre tclaw. Si la carpeta no existe, se crea.

### Trust folder
Cuando Claude Code arranca en una carpeta nueva, muestra "Do you trust this folder?" — hay que usar los botones ↑↓ Enter del frontend para aceptar.

### Detección de "Claude listo"
Busca el patrón `❯` o `? for shortcuts` en las últimas líneas del capture-pane.

## Pendientes / ideas futuras

- Auth (token simple para proteger acceso)
- Crons y queues usando `claude -p` para tareas automatizadas en background
- Persistir sesiones a disco (por ahora solo adopta las de tmux al arrancar)
- Mejor manejo de reconexión WebSocket en el frontend
- Keyboard shortcuts en el browser (flechas del teclado → tmux)

## Contexto del proyecto

Este proyecto nació de la conversación donde también exploramos:
- **OpenClaw** (`/Users/lgm/Sites/openclaw-clone`): framework de agente AI que Luis usa en un servidor para gestionar inventario de colgafas.com vía Telegram
- La diferencia entre Claude Code `-p` (headless), Agent SDK (requiere API key aparte), y remote-control (requiere terminal abierta)
- tmux como "proxy de texto" probado y robusto — la prueba de concepto se validó en 5 minutos con 3 comandos manuales antes de escribir código
