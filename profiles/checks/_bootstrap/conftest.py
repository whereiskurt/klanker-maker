# conftest.py — pytest fixtures for _km_check_bootstrap unit tests.
#
# boto3 is NOT installed locally (it is a Lambda runtime dependency).
# We inject a fake boto3 module into sys.modules before importing the bootstrap,
# so all tests work without a real AWS SDK.
#
# Stubs:
#   - boto3.client (s3.put_object, ssm.get_parameter, events.put_events)
#   - subprocess.run (monkeypatched per-test to return controllable stdout)
#   - Lambda context (get_remaining_time_in_millis)

import json
import sys
import types
import unittest.mock as mock

import pytest


# ---------------------------------------------------------------------------
# Inject a fake boto3 module into sys.modules at collection time.
# This ensures `import boto3` inside the bootstrap and conftest succeeds.
# ---------------------------------------------------------------------------

class FakeS3Client:
    def __init__(self):
        self.calls = []

    def put_object(self, **kwargs):
        self.calls.append(("put_object", kwargs))
        return {}


class FakeSSMClient:
    """Returns a per-path value from the injected map."""
    def __init__(self, param_map: dict | None = None):
        self.param_map = param_map or {}
        self.calls = []

    def get_parameter(self, Name, WithDecryption=True):
        self.calls.append(("get_parameter", Name))
        if Name not in self.param_map:
            raise Exception(f"ParameterNotFound: {Name}")
        return {"Parameter": {"Value": self.param_map[Name]}}


class FakeEventsClient:
    def __init__(self):
        self.calls = []

    def put_events(self, **kwargs):
        self.calls.append(("put_events", kwargs))
        return {"FailedEntryCount": 0, "Entries": [{"EventId": "fake-event-id"}]}


class BoTo3ClientFactory:
    """
    A callable that replaces boto3.client.
    Keeps separate instances per service so tests can inspect call records.
    """
    def __init__(self, s3=None, ssm=None, events=None):
        self.s3 = s3 or FakeS3Client()
        self.ssm = ssm or FakeSSMClient()
        self.events = events or FakeEventsClient()

    def __call__(self, service_name, **kwargs):
        if service_name == "s3":
            return self.s3
        if service_name == "ssm":
            return self.ssm
        if service_name == "events":
            return self.events
        raise ValueError(f"Unexpected boto3 service in test: {service_name}")


def _make_fake_boto3_module(factory: BoTo3ClientFactory) -> types.ModuleType:
    """Create a fake boto3 module backed by the given factory."""
    fake_boto3 = types.ModuleType("boto3")
    fake_boto3.client = factory
    return fake_boto3


# ---------------------------------------------------------------------------
# Fake Lambda context
# ---------------------------------------------------------------------------

class FakeContext:
    """Minimal Lambda context stub."""
    def get_remaining_time_in_millis(self):
        return 25_000  # 25 seconds remaining


@pytest.fixture
def fake_context():
    return FakeContext()


# ---------------------------------------------------------------------------
# boto3 factory fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def boto3_factory(monkeypatch):
    """
    Injects a fresh BoTo3ClientFactory as the boto3.client replacement.
    Returns the factory so tests can access .s3 / .ssm / .events.
    """
    factory = BoTo3ClientFactory()
    fake_boto3 = _make_fake_boto3_module(factory)
    monkeypatch.setitem(sys.modules, "boto3", fake_boto3)
    # Also patch the already-imported reference in the bootstrap module if present
    import importlib
    if "_km_check_bootstrap" in sys.modules:
        monkeypatch.setattr(sys.modules["_km_check_bootstrap"], "boto3", fake_boto3, raising=False)
    return factory


@pytest.fixture
def boto3_factory_with_ssm(monkeypatch):
    """
    Variant with an accessible FakeSSMClient so tests can seed its param_map.
    Returns (factory, ssm_client).
    """
    ssm = FakeSSMClient()
    factory = BoTo3ClientFactory(ssm=ssm)
    fake_boto3 = _make_fake_boto3_module(factory)
    monkeypatch.setitem(sys.modules, "boto3", fake_boto3)
    if "_km_check_bootstrap" in sys.modules:
        monkeypatch.setattr(sys.modules["_km_check_bootstrap"], "boto3", fake_boto3, raising=False)
    return factory, ssm
