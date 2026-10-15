"""Verify local UDID profile signing and a signed device callback."""

import json
from pathlib import Path
import subprocess
import tempfile
import urllib.request

BASE = "http://127.0.0.1:8787"


def call(path, method="GET", body=None):
    request = urllib.request.Request(BASE + path, data=body, method=method)
    with urllib.request.urlopen(request) as response:
        return response.status, response.read()


for digest in ("sha1", "sha256"):
    status, body = call("/api/udid/sessions", "POST", b"{}")
    assert status == 201
    session = json.loads(body)
    status, profile = call(session["profileUrl"])
    assert status == 200
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "profile.der").write_bytes(profile)
        subprocess.run(["openssl", "cms", "-verify", "-inform", "DER", "-in", str(root / "profile.der"),
                        "-noverify", "-out", str(root / "profile.xml")], check=True, capture_output=True)
        profile_xml = (root / "profile.xml").read_text()
        assert session["challenge"] in profile_xml
        assert "https://apphub.bensonday.cc/api/udid/callback" in profile_xml
        udid = "A" * 40
        (root / "device.xml").write_text(
            f"<plist><dict><key>CHALLENGE</key><string>{session['challenge']}</string>"
            f"<key>UDID</key><string>{udid}</string></dict></plist>"
        )
        subprocess.run(["openssl", "cms", "-sign", "-binary", "-nodetach", "-md", digest,
                        "-in", str(root / "device.xml"), "-signer", "/private/tmp/myapphub-profile-cert.pem",
                        "-inkey", "/private/tmp/myapphub-profile-key.pem", "-outform", "DER",
                        "-out", str(root / "device.der")], check=True, capture_output=True)
        status, final_profile = call("/api/udid/callback", "POST", (root / "device.der").read_bytes())
        assert status == 200 and final_profile
        (root / "final.der").write_bytes(final_profile)
        subprocess.run(["openssl", "cms", "-verify", "-inform", "DER", "-in", str(root / "final.der"),
                        "-noverify", "-out", str(root / "final.xml")], check=True, capture_output=True)
        final_xml = (root / "final.xml").read_text()
        # iOS refuses to install a completion profile whose PayloadContent is empty.
        assert "com.apple.applicationaccess" in final_xml
        assert "<key>PayloadContent</key><array></array>" not in final_xml
    status, body = call(session["resultUrl"])
    assert status == 200 and json.loads(body)["udid"] == udid
    status, body = call(session["resultUrl"], "DELETE")
    assert status == 200 and json.loads(body)["deleted"]
print("Local UDID profile signing, signed callback, result, and deletion passed")
