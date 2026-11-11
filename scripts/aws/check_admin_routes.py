#!/usr/bin/env python3
"""Read-only HTTPS acceptance checks for the app and authenticated operator UIs.

Credentials are read from the selected cluster into memory, never arguments,
files, or output. Login creates sessions but no application records are changed.
"""

import argparse
import base64
from html.parser import HTMLParser
import http.cookiejar
import json
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


class Markup(HTMLParser):
    def __init__(self):
        super().__init__()
        self.assets = []
        self.hidden = {}

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "script" and attrs.get("src"):
            self.assets.append(attrs["src"])
        if tag == "input" and attrs.get("type") == "hidden" and attrs.get("name"):
            self.hidden[attrs["name"]] = attrs.get("value", "")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--origin", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--kubeconfig", required=True)
    args = parser.parse_args()
    origin = args.origin.rstrip("/")
    url = urllib.parse.urlsplit(origin)
    require(url.scheme == "https" and url.netloc and not url.path and not url.query
            and not url.fragment and not url.username, "Supply an HTTPS origin without a path")

    def credentials(namespace, name):
        result = subprocess.run(
            ["kubectl", "--kubeconfig", args.kubeconfig, "--context", args.context,
             "-n", namespace, "get", "secret", name, "-o", "json", "--request-timeout=20s"],
            capture_output=True, check=False,
        )
        require(result.returncode == 0, "Could not read operator credentials from the selected cluster")
        return {k: base64.b64decode(v).decode() for k, v in json.loads(result.stdout)["data"].items()}

    def client():
        jar = http.cookiejar.CookieJar()
        return jar, urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), NoRedirect())

    def request(opener, path, data=None, content_type=None):
        headers = {"Origin": origin}
        if content_type:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(origin + path, data=data, headers=headers)
        try:
            with opener.open(req, timeout=30) as response:
                return response.status, response.headers, response.read()
        except urllib.error.HTTPError as response:
            return response.code, response.headers, response.read()

    def passed(label):
        print("PASS " + label, flush=True)

    _, anonymous = client()
    status, _, body = request(anonymous, "/")
    require(status == 200, "Main app unavailable")
    markup = Markup()
    markup.feed(body.decode())
    # SvelteKit emits modulepreloads and an inline import rather than script src.
    assets = markup.assets + re.findall(r'(?:import\(|href=)[\"\']([^\"\']*\/[^\"\']+\.js)[\"\']', body.decode())
    require(assets, "No frontend JavaScript asset found")
    asset = urllib.parse.urlsplit(urllib.parse.urljoin(origin + "/", assets[0]))
    require(asset.netloc == url.netloc, "Frontend asset leaves the expected origin")
    status, headers, _ = request(anonymous, asset.path)
    require(status == 200 and "javascript" in headers.get("Content-Type", ""), "Frontend asset unavailable")
    passed("main app and JavaScript asset")
    for path in ("/user.v1.HealthService/Check", "/supplier.v1.HealthService/Check",
                 "/order.v1.HealthService/Check", "/communication.v1.HealthService/Check",
                 "/moderation.v1.HealthService/Check"):
        status, _, _ = request(anonymous, path, b"{}", "application/json")
        require(status == 200, "Public health RPC failed: " + path)
    passed("public service health RPCs")
    # Empty email reaches real sign-in validation without delivering a message.
    # This catches origin rejection even when app HTML and health still work.
    status, _, body = request(anonymous, "/user.v1.AuthService/RequestLink",
                              b'{"email":""}', "application/json")
    require(status == 400 and json.loads(body).get("code") == "invalid_argument",
            "App sign-in request rejected before email validation")
    passed("app sign-in origin validation; no email sent")

    for path in ("/grafana", "/kafka"):
        status, headers, _ = request(anonymous, path)
        location = urllib.parse.urlsplit(urllib.parse.urljoin(origin, headers.get("Location", "")))
        require(status in (301, 308) and location.scheme == "https" and location.hostname == url.hostname
                and location.path == path + "/", "Incorrect HTTPS subpath redirect: " + path)
    passed("HTTPS trailing-slash redirects")

    status, _, _ = request(anonymous, "/grafana/api/datasources")
    require(status == 401, "Anonymous Grafana data-source API was not rejected")
    grafana = credentials("foc-observability", "grafana-admin")
    grafana_jar, grafana_client = client()
    status, _, _ = request(grafana_client, "/grafana/login",
                           json.dumps({"user": grafana["username"], "password": grafana["password"]}).encode(),
                           "application/json")
    require(status == 200, "Grafana login failed")
    require(any(c.name == "foc_grafana_session" and c.secure and c.path.rstrip("/") == "/grafana"
                for c in grafana_jar), "Grafana session cookie is not Secure and subpath-scoped")
    status, _, body = request(grafana_client, "/grafana/api/datasources")
    require(status == 200 and any(d.get("uid") == "loki" for d in json.loads(body)), "Loki data source unavailable")
    status, _, body = request(grafana_client, "/grafana/api/datasources/proxy/uid/loki/loki/api/v1/labels")
    require(status == 200 and json.loads(body).get("status") == "success", "Authenticated Loki query failed")
    passed("Grafana login, cookie isolation, and authenticated Loki query")

    status, _, _ = request(anonymous, "/kafka/api/clusters")
    require(status in (302, 303, 401, 403), "Anonymous Kafka cluster API was not rejected")
    kafka = credentials("foc", "kafka-ui-auth")
    kafka_jar, kafka_client = client()
    status, _, body = request(kafka_client, "/kafka/login")
    require(status == 200, "Kafka login page unavailable")
    markup = Markup()
    markup.feed(body.decode())
    form = markup.hidden | {"username": kafka["SPRING_SECURITY_USER_NAME"],
                            "password": kafka["SPRING_SECURITY_USER_PASSWORD"]}
    status, _, _ = request(kafka_client, "/kafka/login", urllib.parse.urlencode(form).encode(),
                           "application/x-www-form-urlencoded")
    require(status in (302, 303), "Kafka login failed")
    require(any(c.name == "foc_kafka_session" and c.secure and c.path == "/kafka" for c in kafka_jar),
            "Kafka session cookie is not Secure and subpath-scoped")
    for path in ("/kafka/api/clusters", "/kafka/api/clusters/foc/topics?page=1&perPage=25"):
        status, headers, body = request(kafka_client, path)
        require(status == 200 and "json" in headers.get("Content-Type", ""), "Authenticated Kafka API failed")
        json.loads(body)
    passed("Kafka login, cookie isolation, cluster and topic reads")

    for jar in (grafana_jar, kafka_jar):
        app_request = urllib.request.Request(origin + "/")
        jar.add_cookie_header(app_request)
        require(not app_request.has_header("Cookie"), "Operator session cookie leaked to app path")
    passed("operator cookies excluded from main app")
    for path in ("/kafka/actuator/prometheus", "/kafka/metrics"):
        status, _, _ = request(anonymous, path)
        require(status == 403, "Kafka operational metrics were exposed publicly")
    passed("Kafka operational endpoints blocked at the mesh")
    for path in ("/loki/api/v1/labels", "/user.v1.PublicKeyService/GetPublicKeys",
                 "/credit.v1.CreditReservationService/Reserve"):
        status, _, _ = request(anonymous, path, b"{}", "application/json")
        require(status == 404, "Private endpoint was routed publicly: " + path)
    passed("private endpoints remain unrouted")
    print("PASS unified-origin acceptance", flush=True)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # Never print response bodies, subprocess output, or credential values.
        print("FAIL " + (str(error) if isinstance(error, RuntimeError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
