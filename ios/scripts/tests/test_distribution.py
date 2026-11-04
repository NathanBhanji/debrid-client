import copy
import datetime
import hashlib
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from distribution import APP, GROUP, TEAM, validate_profile, validate_signed_entitlements


class ProfileSafetyTests(unittest.TestCase):
    def setUp(self):
        self.now = datetime.datetime(2026, 11, 4)
        self.cert = b"test-only public certificate fixture"
        self.identity = hashlib.sha1(self.cert).hexdigest().upper()
        self.profile = {"TeamIdentifier": [TEAM], "ExpirationDate": datetime.datetime(2027, 11, 4),
            "DeveloperCertificates": [self.cert], "Entitlements": {"application-identifier": TEAM + "." + APP,
            "com.apple.developer.team-identifier": TEAM, "com.apple.security.application-groups": [GROUP], "get-task-allow": False}}

    def check(self, profile):
        validate_profile(profile, APP, self.identity, self.now)

    def test_exact_profile_accepts_but_weakening_group_does_not(self):
        self.check(self.profile)
        for groups in ([], ["group.other.app"], [GROUP, "group.other.app"]):
            p = copy.deepcopy(self.profile); p["Entitlements"]["com.apple.security.application-groups"] = groups
            with self.assertRaises(ValueError): self.check(p)

    def test_wrong_bundle_team_and_development_profile_reject(self):
        for key, value in [("application-identifier", TEAM + ".*"), ("application-identifier", TEAM + ".other.app"),
                           ("com.apple.developer.team-identifier", "OTHERTEAM"), ("get-task-allow", True)]:
            p = copy.deepcopy(self.profile); p["Entitlements"][key] = value
            with self.assertRaises(ValueError): self.check(p)

    def test_wrong_certificate_expiry_and_device_profiles_reject(self):
        for key, value in [("DeveloperCertificates", [b"other"]), ("ExpirationDate", self.now),
                           ("ProvisionedDevices", ["test-device"]), ("ProvisionsAllDevices", True)]:
            p = copy.deepcopy(self.profile); p[key] = value
            with self.assertRaises(ValueError): self.check(p)

    def test_actual_main_keychain_must_be_exact(self):
        e = copy.deepcopy(self.profile["Entitlements"]); e["keychain-access-groups"] = [TEAM + "." + APP]
        validate_signed_entitlements(e, APP)
        for values in ([], [TEAM + "." + APP, TEAM + ".other"], [TEAM + ".*"]):
            e["keychain-access-groups"] = values
            with self.assertRaises(ValueError): validate_signed_entitlements(e, APP)

    def test_extension_has_no_explicit_keychain_access(self):
        e = copy.deepcopy(self.profile["Entitlements"]); e["application-identifier"] = TEAM + "." + APP + ".share"
        validate_signed_entitlements(e, APP + ".share")
        for values in ([TEAM + "." + APP], [TEAM + ".other"], [TEAM + "." + APP + ".share"]):
            e["keychain-access-groups"] = values
            with self.assertRaises(ValueError): validate_signed_entitlements(e, APP + ".share")

    def test_upload_cannot_opt_into_dirty_or_invalid_build(self):
        script = Path(__file__).resolve().parents[1] / "testflight.py"
        for flags in [("--build-number", "1", "--upload", "--allow-dirty"), ("--build-number", "0")]:
            run = subprocess.run([sys.executable, str(script), *flags], capture_output=True, timeout=10)
            self.assertEqual(run.returncode, 2)


if __name__ == "__main__": unittest.main()
