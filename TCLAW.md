# Documentation

## test in local with a mobile phone
cloudflared tunnel --url http://localhost:8080

## deploy

Método principal — `./deploy.sh` (cross-compila, sube binario + `web/index.html`,
hace backup en `tclaw.bak` y reinicia el servicio):

```bash
./deploy.sh
```

Requiere el alias SSH `openclaw` en `~/.ssh/config`. Target en el server:
`/home/openclaw/tclaw/` (binario `tclaw`, dir `web/`, servicio `tclaw.service`).

Setup inicial del servidor:

`/etc/systemd/system/tclaw.service` → usar `tclaw.service.example`

`/etc/caddy/Caddyfile`:
```caddy
your-domain.com {
	reverse_proxy localhost:8080
}
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable tclaw
sudo systemctl start tclaw
sudo systemctl reload caddy
```

## operar el servicio en prod

El servicio corre como systemd unit `tclaw` en el server `openclaw`
(usuario `openclaw`, dir `/home/openclaw/tclaw`).

```bash
# Reiniciar el servicio
ssh openclaw 'sudo systemctl restart tclaw'

# Estado
ssh openclaw 'systemctl status tclaw --no-pager'

# Logs (en vivo / últimas 50 líneas)
ssh openclaw 'journalctl -u tclaw -f'
ssh openclaw 'journalctl -u tclaw --no-pager -n 50'

# Parar / arrancar
ssh openclaw 'sudo systemctl stop tclaw'
ssh openclaw 'sudo systemctl start tclaw'
```

**Importante:** con la arquitectura PTY directo, reiniciar/parar el servicio
**mata todas las sesiones activas** (los procesos `claude`/`codex` son hijos de
tclaw). El transcript de cada conversación igual queda en disco, recuperable
con `claude --resume` al recrear la sesión.

### rollback

`deploy.sh` deja la versión anterior en `tclaw.bak`. Para volver atrás:

```bash
ssh openclaw 'cd /home/openclaw/tclaw && cp tclaw.bak tclaw && sudo systemctl restart tclaw'
```
