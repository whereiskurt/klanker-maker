# profiles/checks/wiz-intel/snippet.py
#
# Wiz Threat Intel check — emits a SIMULATED Wiz Threat Intelligence payload.
#
# In a real deployment, replace the SIMULATED_ADVISORIES block with a live
# call to the Wiz GraphQL API (https://api.us1.app.wiz.io/graphql) using a
# WIZ_CLIENT_ID + WIZ_CLIENT_SECRET injected via km check deploy --secret.
# This version is entirely local (no external calls) so it works on fresh
# installs and can be used to validate the trigger path end-to-end.
#
# Demonstrates:
#   - Simulated advisory data with per-advisory affected-system counts
#   - max_affected computed field that a when_py predicate can threshold on
#   - Zero-dependency (stdlib-only) snippet
#
# Output schema:
#   {
#     "advisories": [
#       {"id": "WIZ-2026-0042", "severity": "CRITICAL", "title": "...", "affected": 153},
#       ...
#     ],
#     "total_advisories": N,
#     "max_affected": M
#   }
#
# Pair with checks.triggers.example.yaml to fire a sandbox when max_affected > 100.
#
# Usage:
#   km check deploy profiles/checks/wiz-intel/snippet.py --name wiz-intel
#   km check run wiz-intel

import json
import sys

# ---------------------------------------------------------------------------
# Simulated advisory data (replace with live Wiz API call in production)
# ---------------------------------------------------------------------------

SIMULATED_ADVISORIES = [
    {
        "id": "WIZ-2026-0042",
        "severity": "CRITICAL",
        "title": "Log4Shell variant in containerized workloads",
        "affected": 153,
    },
    {
        "id": "WIZ-2026-0038",
        "severity": "HIGH",
        "title": "Exposed S3 bucket with public read ACL",
        "affected": 27,
    },
    {
        "id": "WIZ-2026-0031",
        "severity": "MEDIUM",
        "title": "ECS task role with overly broad IAM permissions",
        "affected": 64,
    },
    {
        "id": "WIZ-2026-0019",
        "severity": "LOW",
        "title": "Missing CloudTrail in secondary region",
        "affected": 8,
    },
]


def build_report(advisories: list) -> dict:
    """Aggregate advisories into a report dict."""
    max_affected = max((a["affected"] for a in advisories), default=0)
    return {
        "advisories": advisories,
        "total_advisories": len(advisories),
        "max_affected": max_affected,
    }


if __name__ == "__main__":
    report = build_report(SIMULATED_ADVISORIES)
    print(json.dumps(report))
    sys.exit(0)
