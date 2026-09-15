#!/bin/bash
#
# Coturn Otomatik Kurulum Scripti
# Kullanım: sudo ./setup-coturn.sh turn.alanadiniz.com [email@adresiniz.com]
#
# Ubuntu 22.04/24.04 için test edilmiştir.

set -e

if [ "$EUID" -ne 0 ]; then
  echo "Bu script root olarak çalıştırılmalı. 'sudo ./setup-coturn.sh domain.com' şeklinde deneyin."
  exit 1
fi

DOMAIN="$1"
EMAIL="${2:-admin@$DOMAIN}"

if [ -z "$DOMAIN" ]; then
  echo "Kullanım: sudo ./setup-coturn.sh turn.alanadiniz.com [email@adresiniz.com]"
  exit 1
fi

echo "=================================================="
echo " Coturn kurulumu başlıyor: $DOMAIN"
echo "=================================================="

# --- DNS kontrolü ---
RESOLVED_IP=$(dig +short "$DOMAIN" | tail -n1)
SERVER_IP=$(curl -s ifconfig.me)

if [ "$RESOLVED_IP" != "$SERVER_IP" ]; then
  echo "UYARI: $DOMAIN adresi şu anda $RESOLVED_IP adresine çözülüyor,"
  echo "        ancak bu sunucunun IP'si $SERVER_IP."
  echo "        Devam etmeden önce DNS A kaydınızı kontrol edin."
  read -p "Yine de devam etmek istiyor musunuz? (e/h): " CONFIRM
  if [ "$CONFIRM" != "e" ]; then
    echo "İptal edildi."
    exit 1
  fi
fi

# --- 1. Sistem güncelleme ---
echo "[1/9] Sistem güncelleniyor..."
apt update -qq && apt upgrade -y -qq

# --- 2. Paket kurulumu ---
echo "[2/9] coturn, certbot, ufw kuruluyor..."
apt install -y -qq coturn certbot ufw dnsutils

# --- 3. Coturn'ü etkinleştir ---
echo "[3/9] Coturn sistem servisi olarak etkinleştiriliyor..."
sed -i 's/#TURNSERVER_ENABLED=1/TURNSERVER_ENABLED=1/' /etc/default/coturn

# --- 4. Firewall ---
echo "[4/9] Firewall kuralları ekleniyor..."
ufw allow 22/tcp comment 'SSH' > /dev/null
ufw allow 3478/tcp comment 'TURN standard' > /dev/null
ufw allow 3478/udp comment 'TURN standard' > /dev/null
ufw allow 5349/tcp comment 'TURN TLS' > /dev/null
ufw allow 5349/udp comment 'TURN TLS' > /dev/null
ufw allow 443/tcp comment 'TURN TLS alt' > /dev/null
ufw allow 443/udp comment 'TURN TLS alt' > /dev/null
ufw allow 49152:65535/udp comment 'TURN relay range' > /dev/null
ufw --force enable > /dev/null

# --- 5. SSL sertifikası ---
echo "[5/9] Let's Encrypt sertifikası alınıyor..."
systemctl stop coturn 2>/dev/null || true
if [ -d "/etc/letsencrypt/live/$DOMAIN" ]; then
  echo "        Sertifika zaten mevcut, yenileme deneniyor..."
  certbot renew --quiet || true
else
  certbot certonly --standalone -d "$DOMAIN" --non-interactive --agree-tos -m "$EMAIL"
fi
chmod -R 755 /etc/letsencrypt/live /etc/letsencrypt/archive

# --- 6. Secret üretimi ---
echo "[6/9] Güvenli auth secret üretiliyor..."
SECRET=$(openssl rand -hex 32)

# --- 7. turnserver.conf yazma ---
echo "[7/9] /etc/turnserver.conf yazılıyor..."
cp /etc/turnserver.conf /etc/turnserver.conf.bak.$(date +%s) 2>/dev/null || true

cat > /etc/turnserver.conf <<EOF
# ==== Coturn Yapılandırması ($DOMAIN) ====
# Otomatik oluşturulma tarihi: $(date)

listening-port=3478
tls-listening-port=5349
listening-ip=0.0.0.0
relay-ip=$SERVER_IP
external-ip=$SERVER_IP

min-port=49152
max-port=65535

# Kimlik doğrulama
use-auth-secret
static-auth-secret=$SECRET
realm=$DOMAIN

# TLS sertifikaları
cert=/etc/letsencrypt/live/$DOMAIN/fullchain.pem
pkey=/etc/letsencrypt/live/$DOMAIN/privkey.pem

# Güvenlik sertleştirmeleri
no-multicast-peers
no-cli
fingerprint
lt-cred-mech
stale-nonce=600
no-loopback-peers
no-tcp-relay

# Kötüye kullanım koruması (kişisel proje için makul limitler)
total-quota=100
max-bps=1000000

# Loglama
log-file=/var/log/turnserver.log
simple-log
EOF

# --- 8. Servisi başlat ---
echo "[8/9] Coturn servisi başlatılıyor..."
systemctl restart coturn
systemctl enable coturn > /dev/null 2>&1

# --- 9. Sertifika yenileme hook'u ---
echo "[9/9] Otomatik sertifika yenileme hook'u ekleniyor..."
mkdir -p /etc/letsencrypt/renewal-hooks/deploy
cat > /etc/letsencrypt/renewal-hooks/deploy/coturn-restart.sh <<'EOF'
#!/bin/bash
systemctl restart coturn
EOF
chmod +x /etc/letsencrypt/renewal-hooks/deploy/coturn-restart.sh

# --- Idle koruması ---
(crontab -l 2>/dev/null | grep -v "$DOMAIN"; echo "*/30 * * * * curl -s https://$DOMAIN:5349 > /dev/null 2>&1") | crontab -

sleep 2

echo ""
echo "=================================================="
echo " KURULUM TAMAMLANDI"
echo "=================================================="
echo ""
systemctl status coturn --no-pager -l | head -n 5
echo ""
echo "--------------------------------------------------"
echo " ÖNEMLİ - Bu bilgileri güvenli bir yere kaydedin:"
echo "--------------------------------------------------"
echo " Domain:       $DOMAIN"
echo " STUN URI:     stun:$DOMAIN:3478"
echo " TURN URI:     turn:$DOMAIN:3478"
echo " TURNS URI:    turns:$DOMAIN:5349"
echo " Auth Secret:  $SECRET"
echo "--------------------------------------------------"
echo ""
echo "Test etmek için:"
echo "  https://webrtc.github.io/samples/src/content/peerconnection/trickle-ice/"
echo ""
echo "Credential üretmek için generate-credentials.js dosyasını kullanın."
echo "=================================================="
