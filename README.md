# Documentation

## Put this in ~/.tmux.conf to allow shift+enter new lines
set -s extended-keys always
set -as terminal-features 'xterm*:extkeys'
bind-key -n S-Enter send-keys Escape "[13;2u"

## test in local with a mobile phone
cloudflared tunnel --url http://localhost:8080

## deploy
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tclaw .
rsync -avz ./tclaw ./web USER@SERVER:/home/tclaw/tclaw/
```

`/etc/systemd/system/tclaw.service`
```ini
Use `tclaw.service.example`
```

`/etc/caddy/Caddyfile`
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
