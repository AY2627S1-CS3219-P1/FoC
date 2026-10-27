"""Mounted B3 controls at explicit Connect fixtures, not live Credit/auth proof.

Acceptance sources: settled Q1-Q5/Q15, NFR-07 widths, NFR-08 labels/focus,
and NFR-13 retained input and mutation feedback. Backend persistence and RPC
behavior are separately exercised by TestCancellationThroughConnectAndPostgres.
"""
import argparse
import json
import re
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

COURIER = "11111111-1111-4111-8111-111111111111"
REQUESTER = "22222222-2222-4222-8222-222222222222"
ORDER = "66666666-6666-4666-8666-666666666666"
OPERATION = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"


def stamp(delta=0):
    return (datetime.now(timezone.utc) + timedelta(seconds=delta)).isoformat().replace("+00:00", "Z")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:5193")
    parser.add_argument("--out", default=".local/order-cancellation-evidence")
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    origin = args.url.rstrip("/")
    state = {"identity": COURIER, "status": "AWAITING_PICKUP", "revision": 4,
             "courier": COURIER, "pending": False, "picked": False, "location": None,
             "lost": True, "stale": False, "locationLost": True, "cancels": [],
             "locations": [], "pickup": 0, "receipt": 0, "refuse": False}
    errors, checks = [], []
    cors = {"Access-Control-Allow-Origin": origin, "Access-Control-Allow-Credentials": "true",
            "Access-Control-Allow-Headers": "content-type,authorization,connect-protocol-version,connect-timeout-ms",
            "Access-Control-Allow-Methods": "GET,POST,OPTIONS", "Access-Control-Expose-Headers": "*"}

    def order():
        value = {"id": ORDER, "requesterId": REQUESTER, "status": "ORDER_STATUS_" + state["status"],
                 "deliveryBuildingName": "COM3", "revision": str(state["revision"]),
                 "itemDescription": "Cancellation recovery fixture", "rewardCredits": "20", "deliveryDuration": "1200s",
                 "expiresAt": stamp(1800), "createdAt": stamp(-120), "updatedAt": stamp(),
                 "pickup": {"locationId": "33333333-3333-4333-8333-333333333333", "name": "Campus Cafe", "buildingName": "COM3"},
                 "cancellationPending": state["pending"]}
        if state["courier"]:
            value["courierId"] = state["courier"]
        if state["picked"]:
            value["pickedUpAt"] = stamp(-60)
        if state["identity"] in (REQUESTER, state["courier"]):
            value["delivery"] = {"location": {"locationId": "44444444-4444-4444-8444-444444444444", "name": "Room 02-15", "buildingName": "COM3"}}
            if state["location"]:
                value["itemLocation"] = state["location"]
        return value

    def respond(route, value, status=200):
        route.fulfill(status=status, headers=cors, content_type="application/json", body=json.dumps(value))

    def route_request(route):
        request = route.request
        url = urlparse(request.url)
        if url.hostname not in ("localhost", "127.0.0.1") or url.port not in (8081, 8082, 8083, 8085):
            route.continue_()
            return
        if request.method == "OPTIONS":
            route.fulfill(status=204, headers=cors)
            return
        body = request.post_data_json or {}
        method = url.path.rsplit("/", 1)[-1]
        if method == "Refresh":
            respond(route, {"accessToken": "fixture-not-a-real-credential", "user": {"id": state["identity"], "email": "fixture@example.test", "displayName": "Fixture", "role": "USER_ROLE_USER"}})
        elif method == "GetOrder":
            respond(route, {"order": order()})
        elif method == "ListOffers":
            respond(route, {"offers": []})
        elif method == "GetDeliveryProof":
            # The composed detail page also mounts the proof panel. This
            # cancellation fixture has no uploaded or admitted evidence.
            respond(route, {"proofs": []})
        elif method == "CancelOrder":
            state["cancels"].append(body)
            if state["refuse"]:
                state["refuse"] = False
                respond(route, {"code": "failed_precondition", "message": "withdrawal recovery is unconfigured"}, 400)
                return
            if state["stale"]:
                state.update(stale=False, status="DELIVERING", picked=True, revision=11)
                respond(route, {"code": "aborted", "message": "confirmed revision changed"}, 409)
                return
            if state["lost"]:
                state.update(lost=False, pending=True, revision=5)
                route.abort("connectionreset")
                return
            if state["identity"] == REQUESTER:
                state.update(status="CANCELLED", pending=False, revision=12)
            status = "PENDING" if state["pending"] else "SUCCEEDED"
            respond(route, {"order": order(), "cancellation": {"operationId": OPERATION, "status": "CANCELLATION_STATUS_" + status}})
        elif method == "RecordItemLocation":
            state["locations"].append(body)
            if state["location"] != body["itemLocation"]:
                state["revision"] += 1
                state["location"] = body["itemLocation"]
            if state["locationLost"]:
                state["locationLost"] = False
                route.abort("connectionreset")
                return
            respond(route, {"order": order()})
        elif method == "ConfirmPickup":
            state["pickup"] += 1
            state.update(status="DELIVERING", picked=True, revision=21)
            respond(route, {"order": order()})
        elif method == "ConfirmReceipt":
            state["receipt"] += 1
            state.update(status="COMPLETED", revision=31)
            respond(route, {"order": order()})
        else:
            raise AssertionError("Unexpected fixture RPC: " + method)

    with sync_playwright() as pw:
        browser = pw.chromium.launch(channel="chrome", headless=True)
        context = browser.new_context(viewport={"width": 360, "height": 900}, record_video_dir=str(out / "video"))
        context.route("**/*", route_request)
        page = context.new_page()
        page.set_default_timeout(30000)
        expect.set_options(timeout=30000)
        page.on("pageerror", lambda error: errors.append(str(error)))
        def confirm_cancel():
            page.get_by_role("button", name=re.compile(r"^(Withdraw|Cancel order)$")).click()
            page.get_by_role("alertdialog", name="Confirm cancellation").get_by_role("button", name="Confirm cancellation", exact=True).click()

        requests = []
        page.on("request", lambda request: requests.append({"method": request.method, "path": urlparse(request.url).path}))
        page.goto(origin + "/orders/" + ORDER, wait_until="domcontentloaded", timeout=120000)
        try:
            expect(page.get_by_role("button", name=re.compile(r"^(Withdraw|Cancel order)$"))).to_be_visible()
        except Exception:
            (out / "startup-debug.json").write_text(json.dumps({"requests": requests, "pageErrors": errors}, indent=2))
            page.screenshot(path=str(out / "startup-failure.png"), full_page=True)
            raise
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        page.get_by_label("Cancellation reason", exact=False).fill("Need to withdraw")
        confirm_cancel()
        expect(page.get_by_role("alert").filter(has_text="We could not confirm your cancellation")).to_be_visible()
        original = dict(state["cancels"][0])
        page.reload(wait_until="domcontentloaded", timeout=120000)
        page.get_by_role("button", name="Check cancellation").click()
        expect(page.get_by_role("status").filter(has_text="Still confirming your withdrawal")).to_be_visible()
        assert state["cancels"][1] == original
        expect(page.get_by_role("button", name="Confirm pickup", exact=True)).to_be_disabled()
        checks.append("Lost response plus page reload retained identical key, reason and confirmed revision; pending withdrawal blocks pickup")
        page.screenshot(path=str(out / "withdrawal-pending-360.png"), full_page=True)
        state.update(pending=False, status="AWAITING_COURIER", courier=None, revision=6)
        page.get_by_role("button", name="Check cancellation").click()
        expect(page.get_by_role("status").filter(has_text="Cancellation confirmed")).to_be_visible()
        expect(page.get_by_text("Room 02-15", exact=False)).to_have_count(0)
        checks.append("Resolved historical withdrawal refreshes to the redacted current order")

        state.update(identity=REQUESTER, status="AWAITING_PICKUP", courier=COURIER, picked=False, revision=10, stale=True)
        page.reload(wait_until="domcontentloaded", timeout=120000)
        page.get_by_label("Cancellation reason", exact=False).fill("Changed plans")
        confirm_cancel()
        expect(page.get_by_role("alert").filter(has_text="check the details")).to_be_visible()
        expect(page.get_by_text("Cancel after pickup. The reserved reward is paid to the courier.", exact=True)).to_be_visible()
        expect(page.get_by_label("Cancellation reason", exact=False)).to_have_value("Changed plans")
        page.screenshot(path=str(out / "stale-confirmation-360.png"), full_page=True)
        stale_key = state["cancels"][-1]["idempotencyKey"]
        confirm_cancel()
        expect(page.get_by_text("Final item location: Final location pending", exact=True)).to_be_visible()
        assert state["cancels"][-1]["expectedRevision"] == "11" and state["cancels"][-1]["idempotencyKey"] != stale_key
        checks.append("Stale pre-pickup cancellation is rejected; retained reason and fresh consequences require a new confirmed intent")

        state["identity"] = COURIER
        page.set_viewport_size({"width": 768, "height": 1000})
        page.reload(wait_until="domcontentloaded", timeout=120000)
        field = page.get_by_label("Final item location", exact=True)
        field.fill("Library desk")
        page.get_by_role("button", name="Save final item location").click()
        expect(page.get_by_role("alert").filter(has_text="Your input is still here")).to_be_visible()
        expect(field).to_have_value("Library desk")
        page.get_by_role("button", name="Save final item location").click()
        expect(page.get_by_role("status").filter(has_text="Final item location saved")).to_be_visible()
        assert state["locations"][0] == state["locations"][1]
        field.fill("Residence reception")
        page.get_by_role("button", name="Save final item location").click()
        expect(page.get_by_text("Final item location: Residence reception", exact=True)).to_be_visible()
        field.focus()
        assert field.evaluate("el => el === document.activeElement")
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        page.screenshot(path=str(out / "disposition-correction-768.png"), full_page=True)
        page.set_viewport_size({"width": 1440, "height": 1000})
        page.screenshot(path=str(out / "disposition-correction-1440.png"), full_page=True)
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        checks.append("Lost location response retained text; identical retry and correction work with labelled, visibly focused controls at 768/1440px")

        state.update(identity=REQUESTER, status="AWAITING_PICKUP", pending=True, picked=False, revision=18, location=None)
        page.reload(wait_until="domcontentloaded", timeout=120000)
        expect(page.get_by_role("button", name=re.compile(r"^(Withdraw|Cancel order)$"))).to_be_enabled()
        confirm_cancel()
        expect(page.get_by_role("status").filter(has_text="Cancellation confirmed")).to_be_visible()
        checks.append("Requester can cancel while the courier withdrawal is pending")

        state.update(identity=COURIER, status="AWAITING_PICKUP", pending=False, picked=False, revision=19, refuse=True)
        page.reload(wait_until="domcontentloaded", timeout=120000)
        confirm_cancel()
        expect(page.get_by_role("alert").filter(has_text="The order changed or could not be cancelled")).to_be_visible()
        expect(page.get_by_role("button", name="Check cancellation")).to_have_count(0)
        expect(page.get_by_role("button", name="Confirm pickup", exact=True)).to_be_enabled()
        checks.append("Definitive pre-admission refusal clears retained intent and does not lock pickup")

        before_storage_failure = len(state["cancels"])
        page.evaluate("() => { Storage.prototype.setItem = function() { throw new DOMException('Fixture quota exceeded', 'QuotaExceededError'); }; }")
        confirm_cancel()
        expect(page.get_by_role("status").filter(has_text="Cancellation not submitted")).to_be_visible()
        expect(page.get_by_role("alert").filter(has_text="Cancellation was not sent")).to_be_visible()
        assert len(state["cancels"]) == before_storage_failure
        checks.append("Storage failure is reported as not submitted and sends no cancellation RPC")

        state.update(status="AWAITING_PICKUP", picked=False, revision=20, location=None)
        page.reload(wait_until="domcontentloaded", timeout=120000)
        page.get_by_role("button", name="Confirm pickup", exact=True).click()
        expect(page.get_by_role("status").filter(has_text="Pickup confirmed")).to_be_visible()
        state.update(identity=REQUESTER, status="DELIVERED", revision=30)
        page.reload(wait_until="domcontentloaded", timeout=120000)
        page.get_by_role("button", name="Confirm receipt", exact=True).click()
        expect(page.get_by_role("status").filter(has_text="Receipt confirmed")).to_be_visible()
        assert state["pickup"] == 1 and state["receipt"] == 1
        checks.append("Mounted pickup and receipt controls use their existing RPCs and refresh current state")
        assert not errors, errors
        context.close()
        browser.close()
    (out / "report.json").write_text(json.dumps({"checks": checks, "pageErrors": errors, "fixtureBoundary": "Browser Connect fixtures; backend proven separately against PostgreSQL"}, indent=2))
    print(json.dumps({"passed": len(checks), "pageErrors": errors, "out": str(out)}, indent=2))


if __name__ == "__main__":
    main()
