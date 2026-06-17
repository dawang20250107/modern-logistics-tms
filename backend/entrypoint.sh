#!/usr/bin/env sh
set -e

ROLE="${1:-web}"

case "$ROLE" in
  web)
    echo "[entrypoint] applying migrations..."
    python manage.py migrate --noinput
    echo "[entrypoint] collecting static (admin)..."
    python manage.py collectstatic --noinput || true
    echo "[entrypoint] starting ASGI server (uvicorn)..."
    exec uvicorn config.asgi:application --host 0.0.0.0 --port 8000 --reload
    ;;
  web-prod)
    echo "[entrypoint] applying migrations..."
    python manage.py migrate --noinput
    echo "[entrypoint] collecting static..."
    python manage.py collectstatic --noinput || true
    echo "[entrypoint] starting gunicorn (uvicorn workers)..."
    exec gunicorn config.asgi:application \
      -k uvicorn.workers.UvicornWorker \
      -w "${WEB_CONCURRENCY:-4}" \
      -b 0.0.0.0:8000 \
      --access-logfile - --error-logfile -
    ;;
  worker)
    exec celery -A config.celery:app worker -l info --concurrency "${CELERY_CONCURRENCY:-4}"
    ;;
  beat)
    exec celery -A config.celery:app beat -l info
    ;;
  *)
    exec "$@"
    ;;
esac
