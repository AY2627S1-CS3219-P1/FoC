"""Repeatable frontend E2E at mocked Connect/S3 seams, not backend certification.

Prerequisites: Python Playwright with Chromium and a running frontend.
Run: python3 frontend/tests/order-ui-e2e.py --url http://127.0.0.1:5187
Artifacts default to .local/order-ui-evidence/ (ignored).
"""
import argparse
import base64
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

USER = "11111111-1111-4111-8111-111111111111"
OTHER = "22222222-2222-4222-8222-222222222222"
PICKUP = "33333333-3333-4333-8333-333333333333"
DELIVERY = "44444444-4444-4444-8444-444444444444"
BUILDING = "55555555-5555-4555-8555-555555555555"
ORDER = "66666666-6666-4666-8666-666666666666"
IMAGE = "77777777-7777-4777-8777-777777777777"


def stamp(delta=0):
    return (datetime.now(timezone.utc) + timedelta(seconds=delta)).isoformat().replace("+00:00", "Z")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:5187")
    parser.add_argument("--out", default=".local/order-ui-evidence")
    parser.add_argument("--channel", default=None, help="Use installed Chrome with --channel chrome")
    parser.add_argument("--diagnose", action="store_true", help="Capture initial-page diagnostics only")
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    origin = args.url.rstrip("/")
    state = {"suspended": False, "create": [], "uploads": 0, "reads": 0, "history": [], "open": [], "lost_create": True, "deadline": False}
    assertions = []
    cors = {"Access-Control-Allow-Origin": origin, "Access-Control-Allow-Credentials": "true", "Access-Control-Allow-Headers": "content-type,authorization,connect-protocol-version,connect-timeout-ms", "Access-Control-Allow-Methods": "GET,POST,OPTIONS", "Access-Control-Expose-Headers": "*"}

    def order(private=False):
        delivery = {"location": {"buildingName": "COM3"}}
        if private:
            delivery = {"location": {"locationId": DELIVERY, "name": "Room 02-15", "buildingName": "COM3", "floor": "2"}, "instructions": "Call when outside"}
        return {"id": ORDER, "deliveryBuildingName": "COM3", "requesterId": USER if private else OTHER, "status": "ORDER_STATUS_EXPIRED" if state["deadline"] else "ORDER_STATUS_AWAITING_COURIER", "pickup": {"locationId": PICKUP, "name": "Campus Cafe", "buildingName": "COM3"}, "delivery": delivery, "itemDescription": "Two meals for a study group", "rewardCredits": "20", "deliveryDuration": "1800s", "expiresAt": stamp(600), "revision": "2", "createdAt": stamp(-30), "updatedAt": stamp()}

    def route_request(route):
        request = route.request
        parsed = urlparse(request.url)
        if parsed.hostname == "storage.fixture":
            if request.method == "OPTIONS":
                route.fulfill(status=204, headers=cors)
                return
            state["uploads"] += 1
            # Object is stored, but the upload acknowledgment is lost.
            route.abort("connectionreset")
            return
        if parsed.hostname not in ("localhost", "127.0.0.1") or parsed.port not in (8081, 8082, 8083, 8085):
            route.continue_()
            return
        if request.method == "OPTIONS":
            route.fulfill(status=204, headers=cors)
            return
        body = request.post_data_json or {}
        method = parsed.path.rsplit("/", 1)[-1]
        if method == "Refresh":
            response = {"accessToken": "fixture-token-not-a-real-credential", "user": {"id": USER, "email": "fixture@example.test", "displayName": "Fixture Requester", "role": "USER_ROLE_SUSPENDED_USER" if state["suspended"] else "USER_ROLE_USER"}}
        elif method == "GetMyAccount":
            response = {"account": {"userId": USER, "balance": "100", "reserved": "0", "available": "100"}}
        elif method == "ListBuildings":
            response = {"buildings": [{"id": BUILDING, "name": "COM3"}]}
        elif method == "ListCategories":
            response = {"categories": []}
        elif method == "ListLocations":
            name = body.get("search", "")
            location_id = DELIVERY if "room" in name.lower() else PICKUP
            response = {"locations": [{"id": location_id, "name": "Room 02-15" if location_id == DELIVERY else "Campus Cafe", "building": {"id": BUILDING, "name": "COM3"}}], "page": 1, "pageSize": 20, "totalItems": "1", "totalPages": 1}
        elif method == "ListOpenOrders":
            state["open"].append(body)
            response = {"orders": [order()], "pageInfo": {"page": 1, "pageSize": 20, "totalItems": "1", "totalPages": 1}}
        elif method == "ListMyOrders":
            state["history"].append(body)
            response = {"orders": [order()], "pageInfo": {"page": 1, "pageSize": 20, "totalItems": "1", "totalPages": 1}}
        elif method == "ListOffers":
            response = {"offers": []}
        elif method == "GetOrder":
            state["reads"] += 1
            response = {"order": order(private=not state["suspended"])}
        elif method == "CreateItemImageUpload":
            response = {"imageId": IMAGE, "uploadUrl": "https://storage.fixture/image", "uploadHeaders": {"Content-Type": "image/png", "If-None-Match": "*"}, "expiresAt": stamp(600)}
        elif method == "CreateOrder":
            state["create"].append(body)
            if state["lost_create"]:
                state["lost_create"] = False
                route.abort("connectionreset")
                return
            response = {"order": order(private=True)}
        else:
            route.fulfill(status=501, headers=cors, content_type="application/json", body=json.dumps({"code": "unimplemented", "message": method}))
            return
        route.fulfill(status=200, headers=cors, content_type="application/json", body=json.dumps(response))

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True, channel=args.channel)
        context = browser.new_context(viewport={"width": 360, "height": 900}, record_video_dir=str(out / "video"))
        context.route("**/*", route_request)
        page = context.new_page()
        page.set_default_timeout(20000)
        expect.set_options(timeout=20000)
        console_errors = []
        page.on("pageerror", lambda error: console_errors.append(str(error)))
        page.goto(origin + "/orders")
        if args.diagnose:
            page.wait_for_load_state("networkidle", timeout=30000)
            page.screenshot(path=str(out / "diagnostic.png"), full_page=True)
            print(json.dumps({"body": page.locator("body").inner_text(), "page_errors": console_errors, "fixture": state}, indent=2))
            context.close()
            browser.close()
            return
        expect(page.get_by_role("heading", name="Browse orders")).to_be_visible()
        expect(page.get_by_text("Two meals for a study group", exact=True)).to_be_visible()
        for width in (360, 768, 1440):
            page.set_viewport_size({"width": width, "height": 900})
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"horizontal overflow at {width}"
            page.screenshot(path=str(out / f"browse-{width}.png"), full_page=True)
        assertions.append("Browse renders coarse fixture destinations without horizontal scrolling at 360/768/1440")
        page.get_by_label("Destination building/area").select_option("COM3")
        expect(page).to_have_url(origin + "/orders?destination=COM3")
        expect(page.get_by_text("Two meals for a study group", exact=True)).to_be_visible()
        assert state["open"][-1].get("deliveryBuildingName") == "COM3"
        assert "deliveryLocationId" not in state["open"][-1]
        assertions.append("Destination filter sends the explicit coarse building field, never an exact Location ID")

        page.goto(origin + "/orders/new")
        expect(page.get_by_text("Available credits:", exact=False)).to_be_visible()
        for label, search, button in (("Pickup location", "Cafe", "Campus Cafe · COM3"), ("Delivery location", "room", "Room 02-15 · COM3")):
            group = page.get_by_role("group", name=label, exact=True)
            group.get_by_label(label + " search", exact=True).fill(search)
            group.get_by_role("button", name="Search", exact=True).click()
            group.get_by_role("button", name=button, exact=True).click()
        page.get_by_label("Item description", exact=True).fill("Two meals for a study group")
        page.get_by_label("Delivery instructions (optional)", exact=True).fill("Call when outside")
        page.get_by_label("Reward in whole credits", exact=True).fill("20")
        png = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
        page.get_by_label("Item images", exact=False).set_input_files({"name": "meal.png", "mimeType": "image/png", "buffer": png})
        expect(page.get_by_text("Upload status unknown.", exact=False)).to_be_visible()
        page.get_by_label("Item images", exact=False).set_input_files({"name": "notes.txt", "mimeType": "text/plain", "buffer": b"not an image"})
        expect(page.get_by_text("Choose JPEG, PNG, WebP, HEIC or HEIF.", exact=True)).to_be_visible()
        page.get_by_role("button", name="Create Order", exact=True).click()
        expect(page.get_by_text("Not submitted. Retry or remove failed images first.", exact=True)).to_be_visible()
        assert not state["create"], "Failed selected image must not be silently omitted"
        page.locator(".uploads li").filter(has_text="notes.txt").get_by_role("button", name="Remove image", exact=True).click()
        assertions.append("Invalid selected image blocks creation until explicit removal, preserving the other image reference")
        for width in (360, 768, 1440):
            page.set_viewport_size({"width": width, "height": 900})
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"creation horizontal overflow at {width}"
            page.screenshot(path=str(out / f"creation-{width}.png"), full_page=True)
        page.get_by_role("button", name="Create Order", exact=True).click()
        expect(page.get_by_text("Failed to confirm the result.", exact=False)).to_be_visible()
        assert len(state["create"]) == 1 and state["create"][0]["itemImageIds"] == [IMAGE]
        assert state["uploads"] == 1
        page.screenshot(path=str(out / "uncertain-submission.png"), full_page=True)
        assertions.append("Lost upload acknowledgment does not silently remove the image or create an Order before explicit submission")
        page.reload()
        expect(page.get_by_text("Recovered a submitted Order.", exact=False)).to_be_visible()
        page.get_by_role("button", name="Check/retry same submission", exact=True).click()
        expect(page).to_have_url(origin + "/orders/" + ORDER)
        expect(page.get_by_role("heading", name="Two meals for a study group", exact=True)).to_be_visible()
        assert len(state["create"]) == 2 and state["create"][0] == state["create"][1]
        assert state["uploads"] == 1
        assert page.evaluate("Object.keys(sessionStorage).filter(k => k.startsWith('foc.order.submission.')).length") == 0
        assertions.append("Lost CreateOrder response survives refresh; retry sends identical fields/image IDs/key, without re-upload; resolution clears tab record")

        state["deadline"] = True
        page.get_by_role("button", name="Refresh order", exact=True).click()
        expect(page.get_by_text("Expired", exact=True)).to_be_visible()
        assertions.append("Explicit refresh renders authoritative backend status rather than deriving terminality in the browser")
        state["suspended"] = True
        page.goto(origin + "/orders/mine?role=courier")
        expect(page.get_by_role("heading", name="My Orders", exact=True)).to_be_visible()
        expect(page.get_by_text("Two meals for a study group", exact=True)).to_be_visible()
        assert state["history"][-1].get("role") == "PARTICIPANT_ROLE_COURIER"
        page.goto(origin + "/orders/new")
        expect(page.get_by_text("Your account is suspended. Creation is unavailable.", exact=False)).to_be_visible()
        expect(page.get_by_role("button", name="Create Order", exact=True)).to_have_count(0)
        assertions.append("Suspended user retains courier-filtered history while CreateOrder UI is unavailable")
        assert not console_errors, console_errors
        page.screenshot(path=str(out / "suspended.png"), full_page=True)
        context.close()
        browser.close()
    report = {"status": "PASS", "scope": "Frontend at mocked Connect and S3 seams; not real backend authorization, S3 signature, Credit or Kafka evidence", "assertions": assertions, "create_requests": len(state["create"]), "object_put_attempts": state["uploads"], "browser": "Chromium", "artifacts": str(out)}
    (out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
