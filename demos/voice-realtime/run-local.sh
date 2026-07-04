#!/usr/bin/env bash
# Run the EXACT same voice service on your laptop that the km sandbox runs.
# Iterate here first (real mic, real APIs) before spending a sandbox on it.
#
#   cp .env.example .env && edit .env
#   ./run-local.sh
#
# Then open the printed http://127.0.0.1:8000 and click the mic.
set -euo pipefail
cd "$(dirname "$0")"

if [ -f .env ]; then set -a; . ./.env; set +a; fi

if [ ! -d .venv ]; then
  python3 -m venv .venv
  ./.venv/bin/pip install --upgrade pip
  ./.venv/bin/pip install -r requirements.txt
fi

exec ./.venv/bin/python app.py
