#!/bin/bash
# tclaw — setup en el server monet.
# Correr en monet como root:   sudo bash ~/monet-setup.sh
set -e

echo ">> instalando servicio tclaw..."
install -m644 /home/ploi/tclaw/tclaw.service /etc/systemd/system/tclaw.service
systemctl daemon-reload
systemctl enable --now tclaw

echo ">> apagando caddy (evita conflicto con nginx)..."
systemctl disable --now caddy 2>/dev/null || true

echo ">> instalando vhost nginx para monet.tclaw.sh..."
install -m644 /home/ploi/tclaw/monet.tclaw.sh.nginx /etc/nginx/sites-available/monet.tclaw.sh
ln -sfn /etc/nginx/sites-available/monet.tclaw.sh /etc/nginx/sites-enabled/monet.tclaw.sh
nginx -t
systemctl reload nginx

echo ">> emitiendo certificado HTTPS (Let's Encrypt)..."
certbot --nginx -d monet.tclaw.sh --redirect --non-interactive --agree-tos -m luisgmore@gmail.com

echo ""
echo ">>> LISTO — tclaw en https://monet.tclaw.sh"
systemctl --no-pager status tclaw | head -6
