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

Setup inicial del servidor (requiere **`tmux`** instalado — `apt install tmux`):

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

**Nota:** las sesiones viven en el servidor tmux, no como hijos de tclaw.
Reiniciar/parar el servicio **no las mata** — al arrancar, tclaw re-adopta
(`adoptExistingSessions`) las sesiones tmux que sigan vivas. Sí las mata
`tmux kill-server` o reiniciar la máquina.

### rollback

`deploy.sh` deja la versión anterior en `tclaw.bak`. Para volver atrás:

```bash
ssh openclaw 'cd /home/openclaw/tclaw && cp tclaw.bak tclaw && sudo systemctl restart tclaw'
```
