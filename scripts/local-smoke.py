"""Exercise the local Worker, D1, and R2 without touching remote resources."""

import hashlib
import json
from pathlib import Path
import secrets
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:8787"
VARS = dict(line.split("=", 1) for line in Path(".dev.vars").read_text().splitlines() if "=" in line)
TOKEN = VARS["APPHUB_TOKEN"]


def call(path, method="GET", body=None, headers=None):
    values = {"Authorization": "Bearer " + TOKEN, **(headers or {})}
    if isinstance(body, dict):
        body = json.dumps(body).encode()
        values["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=body, method=method, headers=values)
    with urllib.request.urlopen(request) as response:
        return response.status, response.read(), response.headers


slug = "test-" + secrets.token_hex(4)
content = b"PK\x03\x04integration-package"
digest = hashlib.sha256(content).hexdigest()
metadata = {
    "platform": "android", "identifier": "com.example." + slug.replace("-", ""),
    "name": "Integration", "versionName": "1.0", "buildNumber": "1",
    "slug": slug, "promote": True, "sha256": digest, "fileSize": len(content),
}
status, body, _ = call("/api/v1/uploads", "POST", metadata)
assert status == 201
upload_id = json.loads(body)["id"]
status, body, _ = call(f"/api/v1/uploads/{upload_id}/parts/1", "PUT", content,
                       {"X-Part-SHA256": digest, "Content-Type": "application/octet-stream"})
assert status == 200
part = json.loads(body)
status, body, _ = call(f"/api/v1/uploads/{upload_id}/complete", "POST", {"parts": [part]})
first_version = json.loads(body)["version"]["id"]
assert status == 201 and json.loads(body)["app"]["slug"] == slug
status, body, _ = call(f"/api/public/apps/{slug}")
assert status == 200 and json.loads(body)["version"]["versionName"] == "1.0"
status, body, _ = call(f"/api/public/apps/{slug}/access", "POST", {"password": "", "platform": "android"})
assert status == 200
url = json.loads(body)["downloadUrl"]
with urllib.request.urlopen(urllib.request.Request(url, headers={"Range": "bytes=0-3"})) as response:
    assert response.status == 206 and response.read() == b"PK\x03\x04"
status, _, headers = call("/api/admin/login", "POST", {"token": TOKEN})
assert status == 200 and "HttpOnly" in headers["Set-Cookie"]
cookie = headers["Set-Cookie"].split(";", 1)[0]
with urllib.request.urlopen(urllib.request.Request(BASE + "/api/admin/session", headers={"Cookie": cookie})) as response:
    assert json.loads(response.read())["authenticated"]
status, _, _ = call(f"/api/admin/apps/{slug}/password", "PUT", {"password": "test-password-2026"})
assert status == 200
try:
    urllib.request.urlopen(url)
    raise AssertionError("old download token remained valid")
except urllib.error.HTTPError as error:
    assert error.code == 401
try:
    call(f"/api/public/apps/{slug}/access", "POST", {"password": "wrong", "platform": "android"})
    raise AssertionError("wrong password accepted")
except urllib.error.HTTPError as error:
    assert error.code == 401
status, _, _ = call(f"/api/public/apps/{slug}/access", "POST", {"password": "test-password-2026", "platform": "android"})
assert status == 200

second_content = b"PK\x03\x04integration-package-v2"
second_digest = hashlib.sha256(second_content).hexdigest()
second_metadata = {**metadata, "versionName": "2.0", "buildNumber": "2", "sha256": second_digest,
                   "fileSize": len(second_content), "promote": False}
status, body, _ = call("/api/v1/uploads", "POST", second_metadata)
assert status == 201
second_upload = json.loads(body)["id"]
_, body, _ = call(f"/api/v1/uploads/{second_upload}/parts/1", "PUT", second_content,
                  {"X-Part-SHA256": second_digest, "Content-Type": "application/octet-stream"})
status, body, _ = call(f"/api/v1/uploads/{second_upload}/complete", "POST", {"parts": [json.loads(body)]})
assert status == 201
second_version = json.loads(body)["version"]["id"]
status, body, _ = call(f"/api/admin/apps/{slug}/versions/{second_version}/promote", "POST", {})
assert status == 200 and json.loads(body)["app"]["currentVersionId"] == second_version
status, _, _ = call(f"/api/admin/apps/{slug}/versions/{first_version}", "DELETE")
assert status == 200
status, body, _ = call("/api/admin/trash")
assert status == 200 and any(item["id"] == first_version for item in json.loads(body)["versions"])
status, _, _ = call(f"/api/admin/apps/{slug}/versions/{first_version}/restore", "POST", {})
assert status == 200
status, _, _ = call(f"/api/admin/apps/{slug}", "DELETE")
assert status == 200
try:
    call(f"/api/public/apps/{slug}")
    raise AssertionError("deleted app remained public")
except urllib.error.HTTPError as error:
    assert error.code == 404
status, _, _ = call(f"/api/admin/apps/{slug}/restore", "POST", {})
assert status == 200
status, body, _ = call(f"/api/public/apps/{slug}")
assert status == 200 and json.loads(body)["version"]["id"] == second_version
print("Local upload, delivery, login, password, version switching, deletion, and recovery passed")
