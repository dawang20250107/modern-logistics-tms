#!/usr/bin/env bash
# 本地预演用的自签证书。生产请换成云厂商证书或 certbot 签的 Let's Encrypt。
#
#   bash deploy/gen-dev-cert.sh [域名，默认 localhost]
#
# 浏览器会对自签证书报警告，这是正常的——它证明 TLS 链路本身通了，
# 只是这张证书没有公共 CA 背书。
set -euo pipefail
DOMAIN="${1:-localhost}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/certs"
mkdir -p "$DIR"
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout "$DIR/privkey.pem" -out "$DIR/fullchain.pem" \
  -subj "/CN=$DOMAIN" \
  -addext "subjectAltName=DNS:$DOMAIN,DNS:localhost,IP:127.0.0.1" 2>/dev/null
chmod 600 "$DIR/privkey.pem"
echo "自签证书已生成：$DIR/{fullchain,privkey}.pem（有效期 365 天，CN=$DOMAIN）"
