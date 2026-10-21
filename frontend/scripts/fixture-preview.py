#!/usr/bin/env python3
"""Interactive Order UI preview, using only Python's standard library and existing npm deps.

Run from any directory: python3 frontend/scripts/fixture-preview.py
Open the printed frontend URL. The separate controls URL switches sample users and
can lose the next creation response to demonstrate same-tab recovery. All orders,
images, credits and sessions are simulated in memory and disappear on shutdown.
No real service, signing key, environment file or application source is changed.
"""
import argparse
import copy
import html
import json
import math
import os
from pathlib import Path
import signal
import socket
import subprocess
import threading
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse
from uuid import uuid4

USER = "11111111-1111-4111-8111-111111111111"
COURIER = "22222222-2222-4222-8222-222222222222"
BUILDING = "55555555-5555-4555-8555-555555555555"
PICKUP = "33333333-3333-4333-8333-333333333333"
DELIVERY = "44444444-4444-4444-8444-444444444444"
LIMIT = 10 * 1024 * 1024


def stamp(seconds=0):
    return (datetime.now(timezone.utc) + timedelta(seconds=seconds)).isoformat().replace("+00:00", "Z")


class FixtureError(Exception):
    def __init__(self, code, message, status=400):
        self.code, self.message, self.status = code, message, status


class Preview(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, port, frontend, role):
        super().__init__(("127.0.0.1", port), Handler)
        self.frontend = frontend
        self.api = f"http://127.0.0.1:{self.server_port}"
        self.role, self.signed_in, self.lose_next = role, True, False
        self.lock = threading.RLock()
        self.images, self.retained, self.orders = {}, {}, {}
        self.locations = [
            {"id": PICKUP, "name": "Campus Cafe", "building": {"id": BUILDING, "name": "COM3"}, "floor": "1", "isSupplier": True},
            {"id": DELIVERY, "name": "Study Room 02-15", "building": {"id": BUILDING, "name": "COM3"}, "floor": "2"},
        ]
        for description, requester, courier, status in [
            ("Chicken rice and iced tea", USER, None, "AWAITING_COURIER"),
            ("Collect stationery from Campus Cafe", COURIER, None, "AWAITING_COURIER"),
            ("Dinner for the project group", USER, COURIER, "DELIVERING"),
            ("Library printouts", COURIER, USER, "COMPLETED"),
        ]:
            order = self.make_order(description, requester)
            order["status"] = "ORDER_STATUS_" + status
            if courier:
                order.update(courierId=courier, assignedAt=stamp(-600), pickedUpAt=stamp(-300))
            if status == "COMPLETED":
                order.update(deliveredAt=stamp(-120), completedAt=stamp(-60))
            self.orders[order["id"]] = order

    def user(self):
        courier = self.role == "courier"
        return {"id": COURIER if courier else USER, "email": "courier@example.test" if courier else "requester@example.test", "displayName": "Fixture Courier" if courier else "Fixture Requester", "role": "USER_ROLE_SUSPENDED_USER" if self.role == "suspended" else "USER_ROLE_USER"}

    def make_order(self, description, requester):
        return {"id": str(uuid4()), "requesterId": requester, "status": "ORDER_STATUS_AWAITING_COURIER", "pickup": {"locationId": PICKUP, "name": "Campus Cafe", "buildingName": "COM3", "floor": "1"}, "delivery": {"location": {"locationId": DELIVERY, "name": "Study Room 02-15", "buildingName": "COM3", "floor": "2"}, "instructions": "Call when outside"}, "deliveryBuildingName": "COM3", "itemDescription": description, "itemImages": [], "rewardCredits": "20", "deliveryDuration": "1800s", "expiresAt": stamp(3600), "createdAt": stamp(-600), "updatedAt": stamp(), "revision": "1"}

    def view(self, order):
        value = copy.deepcopy(order)
        if self.user()["id"] not in (order["requesterId"], order.get("courierId")):
            value["delivery"] = {"location": {"buildingName": order["deliveryBuildingName"]}}
        return value

    def rpc(self, path, body):
        if path.startswith("/user.v1.AuthService/"):
            method = path.rsplit("/", 1)[-1]
            if method in ("Logout", "LogoutAll"):
                self.signed_in = False
                return {}
            if method == "RequestLink":
                print(f"[fixture] Simulated login link: {self.frontend}/login?token=fixture", flush=True)
                return {}
            if method in ("Login", "Register"):
                self.signed_in = True
            if method in ("Refresh", "Login", "Register"):
                if not self.signed_in:
                    raise FixtureError("unauthenticated", "Use the preview controls to sign in again", 401)
                return {"accessToken": "fixture-not-a-real-token", "user": self.user()}
        if not self.signed_in:
            raise FixtureError("unauthenticated", "Fixture session is signed out", 401)
        method = path.rsplit("/", 1)[-1]
        if path == "/user.v1.ProfileService/GetMyProfile":
            return {"profile": self.user()}
        if path == "/credit.v1.CreditAccountService/GetMyAccount":
            return {"account": {"userId": self.user()["id"], "balance": "160", "reserved": "60", "available": "100"}}
        if path.startswith("/supplier.location.v1.LocationDiscoveryService/"):
            if method == "ListBuildings":
                return {"buildings": [{"id": BUILDING, "name": "COM3"}]}
            if method == "ListCategories":
                return {"categories": []}
            if method == "GetLocation":
                location = next((v for v in self.locations if v["id"] == body.get("id")), None)
                if not location:
                    raise FixtureError("not_found", "Sample location not found", 404)
                return {"location": location}
            if method == "ListLocations":
                values = [v for v in self.locations if body.get("search", "").casefold() in v["name"].casefold() and (not body.get("buildingId") or body["buildingId"] == BUILDING) and (not body.get("suppliersOnly") or v.get("isSupplier"))]
                return self.paginate(values, body, "locations", flat=True)
        if path.startswith("/order.v1.OrderService/"):
            if method in ("ListOpenOrders", "ListMyOrders"):
                values = list(self.orders.values())
                caller = self.user()["id"]
                if method == "ListOpenOrders":
                    values = [v for v in values if v["status"] == "ORDER_STATUS_AWAITING_COURIER" and (not body.get("pickupLocationId") or v["pickup"]["locationId"] == body["pickupLocationId"]) and (not body.get("deliveryBuildingName") or v["deliveryBuildingName"] == body["deliveryBuildingName"])]
                else:
                    role = body.get("role")
                    values = [v for v in values if (v["requesterId"] == caller if role == "PARTICIPANT_ROLE_REQUESTER" else v.get("courierId") == caller if role == "PARTICIPANT_ROLE_COURIER" else caller in (v["requesterId"], v.get("courierId")))]
                    if body.get("status") not in (None, "ORDER_STATUS_UNSPECIFIED", 0):
                        values = [v for v in values if v["status"] == body["status"]]
                return self.paginate([self.view(v) for v in values], body, "orders")
            if method == "GetOrder":
                if body.get("id") not in self.orders:
                    raise FixtureError("not_found", "Sample order not found", 404)
                return {"order": self.view(self.orders[body["id"]])}
            if method == "ListOffers":
                return {"offers": []}
            if method in ("CreateOrder", "CreateItemImageUpload") and self.role == "suspended":
                raise FixtureError("permission_denied", "Fixture account is suspended", 403)
            if method == "CreateItemImageUpload":
                image_id = str(uuid4())
                self.images[image_id] = None
                return {"imageId": image_id, "uploadUrl": f"{self.api}/images/{image_id}", "uploadHeaders": {"Content-Type": body.get("contentType", "image/png")}, "expiresAt": stamp(3600)}
            if method == "CreateOrder":
                key = (self.user()["id"], body.get("idempotencyKey"))
                if not key[1] or not body.get("itemDescription") or body.get("pickupLocationId") != PICKUP or body.get("delivery", {}).get("locationId") not in (PICKUP, DELIVERY):
                    raise FixtureError("invalid_argument", "Choose sample locations and enter a description")
                if key in self.retained:
                    original, order_id = self.retained[key]
                    if original != body:
                        raise FixtureError("failed_precondition", "Retry payload differs from the retained fixture submission")
                    return {"order": self.view(self.orders[order_id])}
                if int(body.get("rewardCredits", "0")) not in range(1, 101):
                    raise FixtureError("resource_exhausted", "Fixture available credits: 100")
                if any(not self.images.get(v) for v in body.get("itemImageIds", [])):
                    raise FixtureError("invalid_argument", "An attached fixture image has not been uploaded")
                order = self.make_order(body["itemDescription"], self.user()["id"])
                destination = next(v for v in self.locations if v["id"] == body["delivery"]["locationId"])
                order.update(rewardCredits=body["rewardCredits"], expiresAt=body.get("expiresAt", stamp(3600)), deliveryDuration=body.get("deliveryDuration", "1800s"), delivery={"location": {"locationId": destination["id"], "name": destination["name"], "buildingName": "COM3", "floor": destination["floor"]}, "instructions": body["delivery"].get("instructions", "")}, itemImages=[{"id": v, "downloadUrl": f"{self.api}/images/{v}"} for v in body.get("itemImageIds", [])])
                self.orders[order["id"]] = order
                self.retained[key] = (copy.deepcopy(body), order["id"])
                if self.lose_next:
                    self.lose_next = False
                    return None
                return {"order": self.view(order)}
        raise FixtureError("unimplemented", f"Not included in this Order UI fixture: {path}", 501)

    @staticmethod
    def paginate(values, body, field, flat=False):
        page, size = max(1, int(body.get("page", 1))), max(1, min(100, int(body.get("pageSize", 20))))
        info = {"page": page, "pageSize": size, "totalItems": str(len(values)), "totalPages": math.ceil(len(values) / size)}
        result = {field: values[(page - 1) * size:page * size]}
        result.update(info if flat else {"pageInfo": info})
        return result


class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def respond(self, status, data=b"", content_type="application/json"):
        self.send_response(status)
        origin = self.headers.get("Origin")
        if origin in (self.server.frontend, self.server.api):
            self.send_header("Access-Control-Allow-Origin", origin)
            self.send_header("Access-Control-Allow-Credentials", "true")
            self.send_header("Vary", "Origin")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms, X-User-Agent, X-Connect-User-Agent")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_OPTIONS(self):
        self.respond(204)

    def do_GET(self):
        path = urlparse(self.path).path
        with self.server.lock:
            if path.startswith("/images/") and (image := self.server.images.get(path.split("/")[-1])):
                self.respond(200, image[0], image[1])
                return
            if path not in ("/", "/__preview"):
                self.respond(404)
                return
            user = self.server.user()
            options = "".join(f'<option value="{v}" {"selected" if v == self.server.role else ""}>{v}</option>' for v in ("requester", "courier", "suspended"))
            self.respond(200, f'''<!doctype html><html><head><title>Fixture preview controls</title></head><body style="font:18px system-ui;max-width:42rem;margin:3rem auto;padding:1rem"><h1>Fixture preview controls</h1><p>Simulated login, credits, locations, orders and uploads. No real transactions. Memory resets when this script stops.</p><p>Current sample: {html.escape(user['displayName'])} ({html.escape(self.server.role)})</p><form method="post" action="/__preview"><label>Sample user <select name="role">{options}</select></label><p><label><input type="checkbox" name="lose_next" value="yes"> Lose the next creation response (saved order remains recoverable)</label></p><button>Apply and open frontend</button></form><p><a href="{self.server.frontend}/orders">Open frontend without changing scenario</a></p><p>This preview has only the current checkout's UI. It does not add acceptance, proof or lifecycle controls.</p></body></html>'''.encode(), "text/html; charset=utf-8")

    def read_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length < 0 or length > LIMIT:
            raise FixtureError("resource_exhausted", "Fixture request exceeds 10 MB", 413)
        return self.rfile.read(length)

    def do_PUT(self):
        self.do_POST()

    def do_POST(self):
        try:
            if self.headers.get("Origin") not in (None, self.server.frontend, self.server.api):
                raise FixtureError("permission_denied", "Fixture origin not allowed", 403)
            data = self.read_body()
            path = urlparse(self.path).path
            with self.server.lock:
                if path == "/__preview":
                    fields = parse_qs(data.decode())
                    role = fields.get("role", [""])[0]
                    if role not in ("requester", "courier", "suspended"):
                        raise FixtureError("invalid_argument", "Unknown sample user")
                    self.server.role, self.server.signed_in = role, True
                    self.server.lose_next = fields.get("lose_next") == ["yes"]
                    self.send_response(303)
                    self.send_header("Location", self.server.frontend + "/orders")
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                if path.startswith("/images/"):
                    image_id = path.split("/")[-1]
                    if image_id not in self.server.images:
                        raise FixtureError("not_found", "Unknown fixture image", 404)
                    self.server.images[image_id] = (data, self.headers.get("Content-Type", "image/png"))
                    self.respond(200)
                    return
                body = json.loads(data or b"{}")
                if not isinstance(body, dict):
                    raise FixtureError("invalid_argument", "Expected a JSON object")
                result = self.server.rpc(path, body)
                if result is None:
                    self.close_connection = True
                    self.connection.shutdown(socket.SHUT_RDWR)
                    return
                self.respond(200, json.dumps(result).encode())
        except FixtureError as error:
            self.respond(error.status, json.dumps({"code": error.code, "message": error.message}).encode())
        except (ValueError, TypeError, KeyError):
            self.respond(400, b'{"code":"invalid_argument","message":"Invalid fixture request"}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=5188, help="Frontend port (default: 5188)")
    parser.add_argument("--api-port", type=int, default=0, help="Fixture API port (default: automatically chosen)")
    parser.add_argument("--role", choices=("requester", "courier", "suspended"), default="requester")
    args = parser.parse_args()
    if not 1 <= args.port <= 65535 or not 0 <= args.api_port <= 65535:
        parser.error("Invalid port")
    frontend = Path(__file__).resolve().parents[1]
    if not (frontend / "node_modules").is_dir():
        parser.error("Run npm ci in frontend first")
    # Fail before launching anything if this would replace another local server.
    with socket.socket() as check:
        check.bind(("127.0.0.1", args.port))
    server = Preview(args.api_port, f"http://127.0.0.1:{args.port}", args.role)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    env = os.environ.copy()
    for service in ("USER", "SUPPLIER", "ORDER", "CREDIT"):
        env[f"PUBLIC_{service}_SERVICE_URL"] = server.api
    print(f"[fixture] SIMULATED DATA ONLY. Frontend: {server.frontend}/orders", flush=True)
    print(f"[fixture] Controls: {server.api}/__preview", flush=True)
    child = None
    def interrupt(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupt)
    try:
        child = subprocess.Popen(["npm", "run", "dev", "--", "--host", "127.0.0.1", "--port", str(args.port), "--strictPort"], cwd=frontend, env=env, start_new_session=True)
        return child.wait()
    except KeyboardInterrupt:
        return 0
    finally:
        if child is not None:
            try:
                os.killpg(child.pid, signal.SIGTERM)
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
            except ProcessLookupError:
                pass
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    raise SystemExit(main())
