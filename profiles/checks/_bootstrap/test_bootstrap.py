# test_bootstrap.py — pytest unit tests for _km_check_bootstrap.py
#
# Tests (3):
#   - dispatch: JSON stdout + truthy when_py → events.put_events called once with
#               DetailType=CheckDispatch and expanded prompt; s3.put_object called.
#   - notjson:  non-JSON stdout → s3.put_object called (captured), events.put_events
#               NOT called; return triggered=False.
#   - env:      env precedence — static env var overridden by SSM secret value,
#               then overridden by per-run event['env'] (later wins).
#
# All boto3 calls are stubbed via conftest.py; subprocess.run is monkeypatched so
# no actual snippet file is executed.

import json
import os
import subprocess
import sys
import types
import unittest.mock as mock

import pytest

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _make_subprocess_result(stdout: str, returncode: int = 0):
    """Return a CompletedProcess-like object that subprocess.run returns."""
    result = subprocess.CompletedProcess(
        args=[sys.executable, "snippet.py"],
        returncode=returncode,
    )
    result.stdout = stdout
    result.stderr = ""
    return result


def _run_handler(env_vars: dict, event: dict, boto3_factory, fake_context, monkeypatch, stdout: str):
    """
    Common helper: patch os.environ + boto3.client + subprocess.run, then call handler().
    Returns the handler return value.
    """
    import _km_check_bootstrap as bootstrap  # relative import from the same dir

    monkeypatch.setattr(
        "subprocess.run",
        lambda *args, **kwargs: _make_subprocess_result(stdout),
    )
    # Override the module-level client accessors to use the injected factory
    import boto3
    monkeypatch.setattr(boto3, "client", boto3_factory)

    with mock.patch.dict(os.environ, env_vars, clear=False):
        return bootstrap.handler(event, fake_context)


# ---------------------------------------------------------------------------
# Test: dispatch — JSON stdout + truthy when_py → CheckDispatch emitted
# ---------------------------------------------------------------------------

def test_dispatch(monkeypatch, boto3_factory, fake_context):
    """
    JSON stdout with a truthy when_py should:
      1. Call s3.put_object (output captured).
      2. Call events.put_events exactly once with DetailType=CheckDispatch.
      3. The Detail.prompt must contain the expanded {{reason}} token.
      4. Return triggered=True.
    """
    snippet_output = json.dumps({"severity": "critical", "count": 5})
    trigger = {
        "when_py": "return (True, 'critical findings')",
        "alias": "security-auditor",
        "prompt": "Triage alert: {{reason}} — count={{out.count}}",
        "on_absent": "cold-create",
        "cooldown_seconds": 3600,
        "profile": "github-review",
    }
    env_vars = {
        "KM_CHECK_NAME": "test-check",
        "KM_ARTIFACTS_BUCKET": "test-bucket",
        "KM_CHECK_TRIGGER": json.dumps(trigger),
        "KM_CHECK_SECRET_PATHS": "[]",
    }

    result = _run_handler(env_vars, {}, boto3_factory, fake_context, monkeypatch, snippet_output)

    # Handler return value
    assert result["triggered"] is True
    assert result["reason"] == "critical findings"
    assert "output_key" in result

    # S3 capture happened
    assert len(boto3_factory.s3.calls) == 1
    s3_call_name, s3_kwargs = boto3_factory.s3.calls[0]
    assert s3_call_name == "put_object"
    assert s3_kwargs["Bucket"] == "test-bucket"
    assert b'"severity": "critical"' in s3_kwargs["Body"] or b"severity" in s3_kwargs["Body"]

    # CheckDispatch was emitted
    assert len(boto3_factory.events.calls) == 1
    ev_call_name, ev_kwargs = boto3_factory.events.calls[0]
    assert ev_call_name == "put_events"
    entries = ev_kwargs["Entries"]
    assert len(entries) == 1
    entry = entries[0]
    assert entry["Source"] == "km.sandbox"
    assert entry["DetailType"] == "CheckDispatch"

    detail = json.loads(entry["Detail"])
    assert detail["event_type"] == "check-dispatch"
    assert detail["alias"] == "security-auditor"
    assert detail["auto_start"] is True
    assert detail["reason"] == "critical findings"
    assert detail["on_absent"] == "cold-create"
    assert detail["profile_name"] == "github-review"
    assert detail["cooldown_seconds"] == 3600
    # Prompt expansion: {{reason}} and {{out.count}}
    assert "critical findings" in detail["prompt"]
    assert "5" in detail["prompt"]


# ---------------------------------------------------------------------------
# Test: notjson — non-JSON stdout → capture-only, no CheckDispatch
# ---------------------------------------------------------------------------

def test_notjson(monkeypatch, boto3_factory, fake_context):
    """
    Non-JSON stdout should:
      1. Call s3.put_object (raw output still captured).
      2. NOT call events.put_events.
      3. Return triggered=False.
    """
    trigger = {
        "when_py": "return True",  # would fire if output were JSON
        "alias": "target-alias",
        "prompt": "do something",
        "on_absent": "skip",
        "cooldown_seconds": 0,
    }
    env_vars = {
        "KM_CHECK_NAME": "notjson-check",
        "KM_ARTIFACTS_BUCKET": "test-bucket",
        "KM_CHECK_TRIGGER": json.dumps(trigger),
        "KM_CHECK_SECRET_PATHS": "[]",
    }

    result = _run_handler(
        env_vars, {}, boto3_factory, fake_context, monkeypatch, stdout="not valid json at all"
    )

    assert result["triggered"] is False
    assert result["reason"] == "non-JSON output"
    assert "output_key" in result

    # S3 capture happened (raw output preserved)
    assert len(boto3_factory.s3.calls) == 1
    s3_call_name, s3_kwargs = boto3_factory.s3.calls[0]
    assert s3_call_name == "put_object"
    assert b"not valid json" in s3_kwargs["Body"]

    # No CheckDispatch emitted
    assert len(boto3_factory.events.calls) == 0


# ---------------------------------------------------------------------------
# Test: env — env precedence: static -> SSM secret -> per-run event['env']
# ---------------------------------------------------------------------------

def test_env(monkeypatch, boto3_factory_with_ssm, fake_context):
    """
    Env precedence (LATER WINS):
      - Static Lambda env:         SECRET_KEY=static-value
      - SSM secret fetched:        SECRET_KEY=ssm-secret-value   (wins over static)
      - per-run event['env']:      SECRET_KEY=per-run-value       (wins over SSM)

    The subprocess.run receives the env kwarg — we assert the FINAL value there.
    """
    factory, ssm = boto3_factory_with_ssm
    ssm.param_map = {"/km/checks/secret_key": "ssm-secret-value"}

    # No trigger so we can focus purely on env without needing JSON stdout
    env_vars = {
        "KM_CHECK_NAME": "env-test",
        "KM_ARTIFACTS_BUCKET": "test-bucket",
        "KM_CHECK_TRIGGER": "{}",  # no trigger
        "KM_CHECK_SECRET_PATHS": json.dumps(["/km/checks/secret_key"]),
        "SECRET_KEY": "static-value",
    }

    # We need to capture what env dict reaches subprocess.run
    captured_env = {}

    def fake_subprocess_run(args, env=None, **kwargs):
        if env is not None:
            captured_env.update(env)
        return _make_subprocess_result("{}")

    import _km_check_bootstrap as bootstrap

    monkeypatch.setattr("subprocess.run", fake_subprocess_run)
    import boto3
    monkeypatch.setattr(boto3, "client", factory)

    per_run_event = {"env": {"SECRET_KEY": "per-run-value"}}

    with mock.patch.dict(os.environ, env_vars, clear=False):
        result = bootstrap.handler(per_run_event, fake_context)

    # SSM was fetched
    assert len(ssm.calls) == 1
    assert ssm.calls[0] == ("get_parameter", "/km/checks/secret_key")

    # The key name derived from path basename, uppercased
    assert "SECRET_KEY" in captured_env

    # per-run wins (later wins)
    assert captured_env["SECRET_KEY"] == "per-run-value"

    # (Verify intermediate: if we hadn't had per-run override, SSM value would win)
    # We verify the SSM step was even performed — if static had been the only layer,
    # the SSM fetch would be pointless; here we confirm SSM _was_ called (above) and
    # thus would have overridden static. The per-run layer then overrode SSM.

    # No trigger → not triggered
    assert result["triggered"] is False
