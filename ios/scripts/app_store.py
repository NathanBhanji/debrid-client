"""Minimal App Store Connect transport. Fresh short-lived JWT per request; no secret logging."""
import base64
import json
import os
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

from distribution import APP, ASC_APP, TEST_GROUP


class RefuseRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


class AppStore:
    def __init__(self):
        config = {}
        for line in (Path.home() / ".config/factory/ios-secrets.env").read_text().splitlines():
            line = line.strip().removeprefix("export ")
            if "=" in line and not line.startswith("#"):
                name, value = line.split("=", 1)
                if name in ("ASC_KEY_ID", "ASC_ISSUER_ID"):
                    config[name] = value.strip().strip("\"'")
        self.key_id = config["ASC_KEY_ID"]
        self.issuer = str(uuid.UUID(config["ASC_ISSUER_ID"]))
        if not re.fullmatch(r"[A-Za-z0-9]{8,32}", self.key_id):
            raise ValueError("Invalid API key identifier")
        self.key = Path.home() / ".appstoreconnect/private_keys" / ("AuthKey_" + self.key_id + ".p8")
        if not self.key.is_file():
            raise ValueError("App Store Connect private key file unavailable")

    @staticmethod
    def b64(value):
        return base64.urlsafe_b64encode(value).rstrip(b"=")

    def token(self):
        header = {"alg": "ES256", "kid": self.key_id, "typ": "JWT"}
        payload = {"iss": self.issuer, "iat": int(time.time()), "exp": int(time.time()) + 600, "aud": "appstoreconnect-v1"}
        unsigned = self.b64(json.dumps(header).encode()) + b"." + self.b64(json.dumps(payload).encode())
        der = subprocess.check_output(["openssl", "dgst", "-sha256", "-sign", str(self.key)], input=unsigned, timeout=10)
        if der[0] != 0x30 or der[2] != 2:
            raise ValueError("Unexpected ES256 signature encoding")
        length = der[3]
        r = int.from_bytes(der[4:4 + length], "big")
        at = 4 + length
        if der[at] != 2:
            raise ValueError("Unexpected ES256 signature encoding")
        length = der[at + 1]
        s = int.from_bytes(der[at + 2:at + 2 + length], "big")
        return (unsigned + b"." + self.b64(r.to_bytes(32, "big") + s.to_bytes(32, "big"))).decode()

    def call(self, method, path, body=None, params=None):
        if not path.startswith("/v1/") or "?" in path or "#" in path:
            raise ValueError("Unexpected App Store Connect route")
        url = "https://api.appstoreconnect.apple.com" + path
        if params:
            url += "?" + urllib.parse.urlencode(params)
        request = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None,
            headers={"Authorization": "Bearer " + self.token(), "Content-Type": "application/json"}, method=method)
        try:
            with urllib.request.build_opener(RefuseRedirects()).open(request, timeout=30) as response:
                data = response.read(4 * 1024 * 1024 + 1)
                if len(data) > 4 * 1024 * 1024:
                    raise ValueError("App Store Connect response exceeds bound")
                return json.loads(data) if data else None
        except urllib.error.HTTPError as error:
            # Response bodies may contain tester addresses. Never include them in an exception/log.
            raise RuntimeError("App Store Connect HTTP" + str(error.code) + " for " + method + " " + path) from None

    def verify_app(self):
        app = self.call("GET", "/v1/apps/" + ASC_APP)["data"]
        if app["attributes"]["bundleId"] != APP:
            raise ValueError("App Store Connect app/bundle mismatch")

    def builds(self, build, version):
        return self.call("GET", "/v1/builds", params={"filter[app]": ASC_APP, "filter[version]": str(build),
            "filter[preReleaseVersion.version]": version, "limit": "20"})["data"]

    def verify_group(self):
        group = self.call("GET", "/v1/betaGroups/" + TEST_GROUP, params={"include": "app"})["data"]
        attributes = group["attributes"]
        if group["relationships"]["app"]["data"]["id"] != ASC_APP or attributes.get("isInternalGroup") is not True:
            raise ValueError("Expected dedicated internal group for this app")
        if attributes.get("publicLinkEnabled") or attributes.get("hasAccessToAllBuilds"):
            raise ValueError("Unexpected public link or automatic distribution")
        testers = self.call("GET", "/v1/betaGroups/" + TEST_GROUP + "/betaTesters")["data"]
        if len(testers) != 1:
            raise ValueError("Expected exactly the verified dedicated internal tester")
        return testers[0]["id"]

    def verify_audience(self, build_id, expected_tester):
        if self.verify_group() != expected_tester:
            raise ValueError("Tester changed from the verified handoff")
        groups = self.call("GET", "/v1/builds/" + build_id + "/betaGroups")["data"]
        identifiers = {group["id"] for group in groups}
        if identifiers - {TEST_GROUP}:
            raise ValueError("Build already belongs to an unexpected testing group")
        return identifiers
