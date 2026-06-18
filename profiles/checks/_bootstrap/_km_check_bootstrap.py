# _km_check_bootstrap.py — Lambda handler for every km check Lambda.
#
# Authors write plain Python programs (no Lambda handler signature). This bootstrap
# is the actual Lambda entrypoint, shipped in every check zip alongside the author's
# snippet as "snippet.py".
#
# Per invocation:
#   1. Build env: static (Lambda env) → SSM secrets → per-run event['env']  (LATER WINS)
#   2. subprocess-exec snippet.py capturing stdout
#   3. Write stdout VERBATIM to s3://{bucket}/check-runs/<name>/<ts>/output.json
#   4. If stdout is JSON + KM_CHECK_TRIGGER has when_py → eval predicate
#   5. On truthy → emit ONE CheckDispatch event to km.sandbox bus
#
# Canonical KM_CHECK_TRIGGER schema: KM_CHECK_TRIGGER.schema.md (Phase 116 Plan 04)
# CheckDispatch Detail contract: same file — do NOT change key names without updating
# Plan 116-06 (ttl-handler consumer).

import json
import os
import subprocess
import sys
import textwrap
import time

import boto3

# ---------------------------------------------------------------------------
# Module-level lazy clients (allows unit tests to patch boto3.client)
# ---------------------------------------------------------------------------

def _s3_client():
    return boto3.client("s3")

def _ssm_client():
    return boto3.client("ssm")

def _events_client():
    event_bus_name = os.environ.get("KM_EVENT_BUS", "default")
    return boto3.client("events"), event_bus_name


# ---------------------------------------------------------------------------
# Prompt template expansion
# ---------------------------------------------------------------------------

def _expand_prompt(template: str, out: dict, reason: str) -> str:
    """Replace {{reason}} and {{out.<field>}} tokens in the prompt template."""
    result = template.replace("{{reason}}", reason)
    for key, value in out.items():
        result = result.replace("{{out." + key + "}}", str(value))
    return result


# ---------------------------------------------------------------------------
# when_py predicate evaluation
# ---------------------------------------------------------------------------

def _eval_predicate(when_py: str, out: dict):
    """
    Wrap when_py body as:
        def _pred(out):
            <body indented 4 spaces>
    exec into an isolated globals dict; call _pred(out).

    Returns (triggered: bool, reason: str).
    Raises nothing — exceptions are caught by the caller (fail-closed).
    """
    indented_body = textwrap.indent(when_py, "    ")
    pred_src = "def _pred(out):\n" + indented_body
    globs = {}
    exec(pred_src, globs)  # noqa: S102 — operator-trusted config
    raw = globs["_pred"](out)
    if isinstance(raw, tuple):
        triggered, reason = raw[0], str(raw[1]) if len(raw) > 1 else ""
    else:
        triggered, reason = bool(raw), ""
    return bool(triggered), reason


# ---------------------------------------------------------------------------
# Main Lambda handler
# ---------------------------------------------------------------------------

def handler(event, context):
    """
    Lambda entrypoint.

    Required env vars:
        KM_CHECK_NAME           — check name (logged + used in S3 key)
        KM_ARTIFACTS_BUCKET     — S3 bucket for output capture (REQUIRED)

    Optional env vars:
        KM_CHECK_TRIGGER        — JSON trigger config (default: {})
        KM_CHECK_SECRET_PATHS   — JSON list of SSM paths (default: [])
        KM_EVENT_BUS            — EventBridge bus name (default: "default")

    Event payload (optional):
        event['env']            — dict of per-run env overrides (later wins)
    """
    name = os.environ.get("KM_CHECK_NAME", "unknown")
    bucket = os.environ["KM_ARTIFACTS_BUCKET"]  # hard required — let KeyError propagate
    trigger = json.loads(os.environ.get("KM_CHECK_TRIGGER", "{}"))
    secret_paths = json.loads(os.environ.get("KM_CHECK_SECRET_PATHS", "[]"))

    # ------------------------------------------------------------------
    # Step 1: Build env for the subprocess (LATER WINS)
    # ------------------------------------------------------------------
    env = dict(os.environ)

    # Layer 2: SSM secrets
    if secret_paths:
        ssm = _ssm_client()
        for ssm_path in secret_paths:
            try:
                resp = ssm.get_parameter(Name=ssm_path, WithDecryption=True)
                key = ssm_path.split("/")[-1].upper()
                env[key] = resp["Parameter"]["Value"]
            except Exception as exc:  # noqa: BLE001
                print(f"check_ssm_fetch_error: {ssm_path} — {exc}")

    # Layer 3: per-run overrides from invoke payload
    if isinstance(event.get("env"), dict):
        env.update(event["env"])

    # ------------------------------------------------------------------
    # Step 2: subprocess-exec snippet.py capturing stdout
    # ------------------------------------------------------------------
    remaining_ms = context.get_remaining_time_in_millis()
    timeout_s = max(remaining_ms / 1000 - 5, 1)

    proc = subprocess.run(
        [sys.executable, "snippet.py"],
        env=env,
        capture_output=True,
        text=True,
        timeout=timeout_s,
    )
    stdout = proc.stdout

    if proc.returncode != 0:
        print(f"check_snippet_nonzero_exit: {name} rc={proc.returncode}")
        if proc.stderr:
            print(f"check_snippet_stderr: {proc.stderr[:2000]}")

    # ------------------------------------------------------------------
    # Step 3: Write stdout VERBATIM to S3 (always — even on non-zero exit)
    # ------------------------------------------------------------------
    ts = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
    output_key = f"check-runs/{name}/{ts}/output.json"
    _s3_client().put_object(
        Bucket=bucket,
        Key=output_key,
        Body=stdout.encode("utf-8"),
        ContentType="application/json",
    )

    # ------------------------------------------------------------------
    # Step 4: Evaluate trigger (only if when_py is present)
    # ------------------------------------------------------------------
    when_py = trigger.get("when_py", "").strip()
    if not when_py:
        return {"triggered": False, "reason": "no trigger configured", "output_key": output_key}

    # Non-JSON stdout → captured (done above) but NEVER triggers
    try:
        out = json.loads(stdout)
    except json.JSONDecodeError:
        print(f"check_output_not_json: {name}")
        return {"triggered": False, "reason": "non-JSON output", "output_key": output_key}

    # Evaluate the predicate (fail-closed on exception)
    try:
        triggered, reason = _eval_predicate(when_py, out)
    except Exception as exc:  # noqa: BLE001
        print(f"check_predicate_error: {name} — {exc}")
        return {"triggered": False, "reason": f"predicate error: {exc}", "output_key": output_key}

    if not triggered:
        return {"triggered": False, "reason": reason, "output_key": output_key}

    # ------------------------------------------------------------------
    # Step 5: Emit ONE CheckDispatch event
    # ------------------------------------------------------------------
    alias = trigger.get("alias", "")
    profile_name = trigger.get("profile", "")
    on_absent = trigger.get("on_absent", "cold-create")
    cooldown_seconds = int(trigger.get("cooldown_seconds", 0))

    prompt_template = trigger.get("prompt", "")
    prompt = _expand_prompt(prompt_template, out, reason)

    # Authoritative CheckDispatch Detail — keys must match Plan 116-06 exactly.
    # See KM_CHECK_TRIGGER.schema.md for the full contract.
    detail = {
        "event_type":       "check-dispatch",
        "check_name":       name,
        "alias":            alias,
        "prompt":           prompt,
        "profile_name":     profile_name,
        "on_absent":        on_absent,
        "reason":           reason,
        "cooldown_seconds": cooldown_seconds,
        "auto_start":       True,
    }

    events_client, event_bus_name = _events_client()
    entry = {
        "Source":     "km.sandbox",
        "DetailType": "CheckDispatch",
        "Detail":     json.dumps(detail),
    }
    # EventBusName "default" is implied when omitted, but be explicit for non-default buses.
    if event_bus_name and event_bus_name != "default":
        entry["EventBusName"] = event_bus_name

    events_client.put_events(Entries=[entry])

    return {"triggered": True, "reason": reason, "output_key": output_key}
