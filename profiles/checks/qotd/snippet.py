# profiles/checks/qotd/snippet.py
#
# Quote-of-the-Day check — fetches a random inspirational quote from a public
# internet API and emits it as JSON.
#
# Demonstrates:
#   - Open internet egress from a check Lambda (no VPC, no eBPF enforcement)
#   - Third-party dependency packaging (requests via requirements.txt)
#   - Plain-program contract: no Lambda handler signature, just print JSON to stdout
#
# Output schema:
#   {"quote": "...", "author": "...", "category": "..."}
#
# Usage:
#   km check deploy profiles/checks/qotd/snippet.py --name qotd
#   km check run qotd

import json
import sys

import requests  # installed via requirements.txt at km check deploy time

API_URL = "https://api.quotable.io/random"


def fetch_quote() -> dict:
    """Fetch a random quote from the Quotable public API."""
    response = requests.get(API_URL, timeout=10)
    response.raise_for_status()
    data = response.json()
    return {
        "quote": data.get("content", ""),
        "author": data.get("author", "Unknown"),
        "category": (data.get("tags") or ["general"])[0],
    }


if __name__ == "__main__":
    try:
        result = fetch_quote()
    except Exception as exc:  # noqa: BLE001
        # Emit a structured error so the bootstrap still captures JSON output.
        # The bootstrap will log check_output_not_json if stdout is non-JSON,
        # so we ensure JSON is always printed.
        result = {"quote": "", "author": "", "category": "", "error": str(exc)}

    print(json.dumps(result))
    sys.exit(0)
