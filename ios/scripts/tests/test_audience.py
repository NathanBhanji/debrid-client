import contextlib
import io
import json
import os
import runpy
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from app_store import AppStore
from distribution import APP, ASC_APP, TEST_GROUP


class AudienceOrderingTests(unittest.TestCase):
    def exercise(self, changed_member=False, other_group=False):
        class FakeAPI(AppStore):
            def __init__(self): self.checks = 0; self.polls = 0; self.writes = []
            def verify_app(self): pass
            def verify_group(self):
                self.checks += 1
                return "changed-tester" if changed_member and self.checks > 1 else "expected-tester"
            def builds(self, *args):
                self.polls += 1
                return [{"id": "fixture-build", "attributes": {"processingState": "PROCESSING" if self.polls == 1 else "VALID", "expired": False}}]
            def call(self, method, path, *args, **kwargs):
                if method != "GET": self.writes.append((method, path)); raise AssertionError("Audience gate allowed a write")
                if path == "/v1/betaGroups":
                    self.assert_query = kwargs.get("params")
                    return {"data": [{"id": "unexpected-group", "relationships": {"app": {"data": {"id": ASC_APP}}}}] if other_group else []}
                if path.endswith("/betaBuildLocalizations"): return {"data": []}
                raise AssertionError("Unexpected fixture request")
        api = FakeAPI()
        with tempfile.TemporaryDirectory(prefix="debrid-audience-test-") as temporary:
            handoff = Path(temporary) / "tester.json"; handoff.write_text(json.dumps({"assigned_beta_tester_id": "expected-tester"}))
            manifest = Path(temporary) / "upload.json"; manifest.write_text(json.dumps({"bundle": APP, "asc_app_id": ASC_APP,
                "build": "1", "version": "0.1.0", "uploaded": True, "dirty_preview": False, "source": "1" * 40, "ipa_sha256": "2" * 64}))
            script = Path(__file__).resolve().parents[1] / "distribute-testflight.py"
            argv = [str(script), "--build-number", "1", "--wait-seconds", "2", "--assign", "--tester-handoff", str(handoff), "--upload-manifest", str(manifest)]
            previous = os.umask(0o077)
            try:
                with patch("sys.argv", argv), patch("app_store.AppStore", return_value=api), patch("workflow.acquire_lock"), patch("time.sleep"), contextlib.redirect_stdout(io.StringIO()):
                    with self.assertRaises(ValueError): runpy.run_path(str(script), run_name="__main__")
            finally: os.umask(previous)
        self.assertEqual(api.writes, [])
        self.assertEqual(api.polls, 2, "Exercise a real processing-wait boundary before the audience refusal")

    def test_changed_member_after_processing_cannot_receive_any_write(self): self.exercise(changed_member=True)
    def test_existing_other_build_group_blocks_before_any_write(self): self.exercise(other_group=True)


class AudienceReadbackTests(unittest.TestCase):
    def exercise(self, result):
        api = object.__new__(AppStore)
        with patch.object(api, "verify_group", return_value="expected-tester"), patch.object(api, "call", return_value=result) as call:
            groups = api.verify_audience("fixture-build", "expected-tester")
            call.assert_called_once_with("GET", "/v1/betaGroups", params={"filter[builds]": "fixture-build", "include": "app", "limit": "200"})
            return groups

    def test_supported_single_relationship_filter_and_exact_app(self):
        group = {"id": TEST_GROUP, "relationships": {"app": {"data": {"id": ASC_APP}}}}
        self.assertEqual(self.exercise({"data": [group], "links": {"next": None}}), {TEST_GROUP})
        self.assertEqual(self.exercise({"data": []}), set())

    def test_partial_or_other_app_audience_refuses(self):
        for result in ({"data": [], "links": {"next": "https://example.invalid/next"}},
                       {"data": [{"id": TEST_GROUP, "relationships": {"app": {"data": {"id": "other-app"}}}}]},
                       {"data": [{"id": TEST_GROUP}]}):
            with self.assertRaises(ValueError): self.exercise(result)


if __name__ == "__main__": unittest.main()
