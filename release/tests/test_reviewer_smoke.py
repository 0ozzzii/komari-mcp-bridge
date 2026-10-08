import importlib.util
import json
from pathlib import Path
import unittest


class SmokeTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location("smoke", Path(__file__).resolve().parents[1] / "reviewer-smoke-test.py")
        self.smoke = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.smoke)
        self.sha = "1" * 40
        self.providers = [("primary", "https://primary.example/v1", "private-test-key", "model"),
                          ("backup", "https://backup.example/v1", "private-backup-key", "model")]
        self.now = 0

    def invoke(self, call, providers=None):
        def sleep(seconds):
            self.now += seconds
        return self.smoke.probe(self.providers if providers is None else providers, self.sha,
                                call=call, clock=lambda: self.now, sleep=sleep)

    def valid(self):
        return {"decision": "approve", "reviewed_sha": self.sha, "summary": "ok", "findings": []}

    def test_both_bindings_tested_serially_without_real_approval(self):
        starts = []
        def call(*args):
            starts.append(self.now)
            return self.valid()
        report = self.invoke(call)
        self.assertEqual(starts, [0, 10])
        self.assertTrue(report["passed"])
        self.assertNotIn("decision", report)
        self.assertNotIn("private-test-key", json.dumps(report))
        self.assertNotIn("primary.example", json.dumps(report))

    def test_primary_429_does_not_prevent_independent_backup_probe_or_retry(self):
        calls = []
        def call(url, *args):
            calls.append(url)
            if "primary" in url:
                raise self.smoke.reviewer.ProviderFailure("HTTP 429", retryable=True)
            return self.valid()
        report = self.invoke(call)
        self.assertEqual(len(calls), 2)
        self.assertFalse(report["passed"])
        self.assertEqual(report["providers"][0]["error"], "HTTP 429")
        self.assertTrue(report["providers"][1]["passed"])

    def test_missing_binding_and_invalid_response_fail_closed(self):
        def forbidden(*args):
            self.fail("Missing binding must not make an API request")
        report = self.invoke(forbidden, [("primary", "https://example.com", "", "model")])
        self.assertFalse(report["passed"])
        report = self.invoke(lambda *args: dict(self.valid(), reviewed_sha="2" * 40))
        self.assertFalse(report["passed"])
        self.assertTrue(all(not r["passed"] for r in report["providers"]))


if __name__ == "__main__":
    unittest.main()
