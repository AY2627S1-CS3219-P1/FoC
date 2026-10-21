"""Mounted delivery-proof UI at explicit Connect/storage fixtures.

Acceptance: retained upload retry/removal, five-photo proof limit, unknown
confirmation locking, durable pending recovery and participant-only evidence.
Real PostgreSQL/S3/decoder/RPC behavior is separately covered by
TestProofsThroughConnectPostgresAndS3. This script is not live auth/Credit proof.
"""
import argparse
import json
import io
from PIL import Image, ImageDraw
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

COURIER = "11111111-1111-4111-8111-111111111111"
REQUESTER = "22222222-2222-4222-8222-222222222222"
OTHER = "55555555-5555-4555-8555-555555555555"
ORDER = "66666666-6666-4666-8666-666666666666"


def stamp(delta=0):
    return (datetime.now(timezone.utc) + timedelta(seconds=delta)).isoformat().replace("+00:00", "Z")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:5194")
    parser.add_argument("--browser-channel", default=None)
    parser.add_argument("--capture-widths", nargs="+", type=int, default=[360, 390, 768, 1440])
    parser.add_argument("--recording-only", action="store_true")
    parser.add_argument("--slow-mo", type=int, default=0)
    parser.add_argument("--out", default=".local/order-proof-integration/browser")
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    origin = args.url.rstrip("/")
    # Synthetic, labeled image checks preview layout, not real photo/codec quality.
    picture = Image.new("RGB", (1000, 700), "#e8edf4")
    draw = ImageDraw.Draw(picture)
    draw.rounded_rectangle((200, 150, 800, 560), radius=20, fill="#b98854", outline="#654321", width=8)
    draw.rectangle((460, 150, 540, 560), fill="#e2c9a6")
    draw.text((30, 30), "SYNTHETIC DELIVERY PROOF - UI FIXTURE", fill="#183458", font_size=32)
    draw.text((240, 400), "PARCEL AT ROOM 02-15", fill="#183458", font_size=30)
    buffer = io.BytesIO()
    picture.save(buffer, format="PNG")
    png = buffer.getvalue()
    portrait = picture.resize((700, 1000))
    portrait_buffer = io.BytesIO()
    portrait.save(portrait_buffer, format="PNG")
    portrait_png = portrait_buffer.getvalue()
    state = {"identity": COURIER, "role": "USER_ROLE_USER", "status": "DELIVERING", "revision": 4,
             "grants": {}, "uploads": [], "verified": set(), "putFailure": True, "verifyFailure": True,
             "removeFailure": True, "removed": [], "confirmations": [], "pending": None, "accepted": False}
    checks, errors = [], []
    cors = {"Access-Control-Allow-Origin": origin, "Access-Control-Allow-Credentials": "true",
            "Access-Control-Allow-Headers": "content-type,authorization,connect-protocol-version,connect-timeout-ms,if-none-match",
            "Access-Control-Allow-Methods": "GET,POST,PUT,OPTIONS", "Access-Control-Expose-Headers": "*"}

    def order():
        result = {"id": ORDER, "requesterId": REQUESTER, "courierId": COURIER,
                  "status": "ORDER_STATUS_" + state["status"], "revision": str(state["revision"]),
                  "deliveryBuildingName": "COM3", "itemDescription": "Delivery proof retry fixture",
                  "rewardCredits": "20", "deliveryDuration": "1200s", "expiresAt": stamp(1800),
                  "createdAt": stamp(-120), "updatedAt": stamp(), "pickedUpAt": stamp(-40),
                  "pickup": {"locationId": "33333333-3333-4333-8333-333333333333", "name": "Campus Cafe", "buildingName": "COM3"}}
        if state["identity"] in (REQUESTER, COURIER):
            result["delivery"] = {"location": {"locationId": "44444444-4444-4444-8444-444444444444", "name": "Room 02-15", "buildingName": "COM3"}}
        if state["accepted"]:
            result.update(deliveredAt=state["received"], unattendedDelivery=True)
        return result

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
        if url.path.startswith("/fixture-put/"):
            photo = url.path.rsplit("/", 1)[-1]
            if photo.endswith("000002") and state["putFailure"]:
                state["putFailure"] = False
                route.abort("connectionreset")
                return
            route.fulfill(status=200, headers=cors)
            return
        if url.path.startswith("/fixture-proof/"):
            route.fulfill(status=200, headers=cors, content_type="image/png", body=portrait_png if url.path.endswith("000002") else png)
            return
        body = request.post_data_json or {}
        method = url.path.rsplit("/", 1)[-1]
        if method == "Refresh":
            respond(route, {"accessToken": "fixture-not-a-real-credential", "user": {"id": state["identity"], "email": "fixture@example.test", "displayName": "Fixture", "role": state["role"]}})
        elif method == "GetOrder":
            respond(route, {"order": order()})
        elif method == "ListOffers":
            respond(route, {"offers": []})
        elif method == "CreateProofUpload":
            state["uploads"].append(body.copy())
            nonce = body["uploadId"]
            if nonce not in state["grants"]:
                state["grants"][nonce] = f"aaaaaaaa-aaaa-4aaa-8aaa-{len(state['grants']) + 1:012d}"
            photo = state["grants"][nonce]
            respond(route, {"proofId": photo, "uploadUrl": f"http://127.0.0.1:8083/fixture-put/{photo}",
                            "uploadHeaders": {"Content-Type": body["contentType"], "If-None-Match": "*"}, "expiresAt": stamp(600)})
        elif method == "VerifyProofUpload":
            if body["proofId"].endswith("000002") and state["verifyFailure"]:
                state["verifyFailure"] = False
                respond(route, {"code": "unavailable", "message": "validation is temporarily unavailable"}, 503)
            else:
                state["verified"].add(body["proofId"])
                respond(route, {})
        elif method == "AbandonProofUpload":
            state["removed"].append(body.copy())
            if state["removeFailure"]:
                state["removeFailure"] = False
                respond(route, {"code": "unavailable", "message": "retirement unknown"}, 503)
            else:
                respond(route, {})
        elif method == "ConfirmDelivery":
            submitted = {"orderId": body["orderId"], "proofIds": body.get("proofIds", []),
                         "unattended": body.get("unattended", False), "withoutProof": body.get("withoutProof", False)}
            state["confirmations"].append(submitted)
            if submitted["withoutProof"]:
                respond(route, {"code": "failed_precondition", "message": "no server-certified PUT outage"}, 400)
            elif state["pending"] is None and not state["accepted"]:
                assert all(photo in state["verified"] for photo in submitted["proofIds"])
                state["received"] = stamp()
                state["pending"] = {**submitted, "receivedAt": state["received"]}
                route.abort("connectionreset")
            else:
                assert submitted == {key: value for key, value in state["pending"].items() if key != "receivedAt"}
                state.update(status="DELIVERED", revision=5, accepted=True)
                respond(route, {"order": order()})
        elif method == "GetDeliveryProof":
            if state["accepted"]:
                respond(route, {"proofs": [{"proofId": photo, "contentType": "image/png", "downloadUrl": f"http://127.0.0.1:8083/fixture-proof/{photo}", "expiresAt": stamp(600)} for photo in state["pending"]["proofIds"]]})
            elif state["pending"]:
                respond(route, {"pending": state["pending"]})
            else:
                respond(route, {})
        else:
            respond(route, {})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, channel=args.browser_channel, slow_mo=args.slow_mo)
        context = browser.new_context(viewport={"width": 360, "height": 1000}, record_video_size={"width": 360, "height": 1000}, record_video_dir=str(out / "video"))
        context.route("**/*", route_request)
        page = context.new_page()
        page.set_default_timeout(30000)
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(f"{origin}/orders/{ORDER}")
        try:
            expect(page.get_by_label("Choose photos", exact=True)).to_be_visible(timeout=30000)
        except Exception:
            if not args.recording_only: page.screenshot(animations="disabled", path=str(out / "startup-failure.png"), full_page=True)
            print(json.dumps({"pageErrors": errors, "body": page.locator("body").inner_text()[:6000]}))
            raise
        def capture(name):
            if args.recording_only:
                return
            previous = page.viewport_size
            for width in args.capture_widths:
                page.set_viewport_size({"width": width, "height": 1000})
                assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), f"overflow: {name}/{width}"
                if not args.recording_only: page.screenshot(animations="disabled", path=str(out / f"{name}-{width}.png"), full_page=True)
                target = page.locator(".proof-panel" if page.locator(".proof-panel").count() else ".proof-evidence")
                target.screenshot(animations="disabled", path=str(out / f"{name}-panel-{width}.png"))
            page.set_viewport_size(previous)

        capture("empty")
        selected = page.get_by_role("list", name="Selected proof photos")
        files = [{"name": "proof" + "a" * 80 + ".png" if letter == "a" else f"proof-{letter}.png", "mimeType": "image/png", "buffer": png} for letter in "abc"]
        # Exercise the visible chooser and its native keyboard activation.
        chooser = page.get_by_label("Choose photos", exact=True)
        page.get_by_role("button", name="Refresh", exact=True).focus()
        for _ in range(20):
            page.keyboard.press("Tab")
            if chooser.evaluate("element => element === document.activeElement"):
                break
        expect(chooser).to_be_focused()
        with page.expect_file_chooser() as dialog:
            chooser.press("Space")
        assert dialog.value.is_multiple()
        assert chooser.get_attribute("capture") is None
        dialog.value.set_files(files[:2])
        expect(selected.get_by_role("listitem")).to_have_count(2)
        camera = page.get_by_role("button", name="Take photo", exact=True)
        camera.focus()
        with page.expect_file_chooser() as dialog:
            camera.press("Enter")
        assert not dialog.value.is_multiple()
        assert dialog.value.element.get_attribute("capture") == "environment"
        assert dialog.value.element.get_attribute("accept") == "image/*"
        dialog.value.set_files([])
        expect(selected.get_by_role("listitem")).to_have_count(2)
        with page.expect_file_chooser() as dialog:
            camera.click()
        dialog.value.set_files({"name": "unsupported.tiff", "mimeType": "image/tiff", "buffer": png})
        expect(page.get_by_text("Replace this file with a JPEG", exact=False)).to_be_visible()
        page.get_by_role("button", name="Remove unsupported.tiff", exact=True).click()
        with page.expect_file_chooser() as dialog:
            camera.press("Enter")
        dialog.value.set_files(files[2])
        expect(page.locator("#delivery-proof-camera")).to_have_value("")
        expect(page.get_by_text(files[0]["name"], exact=True)).to_be_visible()
        expect(selected.get_by_role("listitem")).to_have_count(3)
        chooser.set_input_files([{**files[0], "name": f"extra-{index}.png"} for index in range(2)])
        expect(selected.get_by_role("listitem")).to_have_count(5)
        with page.expect_file_chooser() as dialog:
            camera.click()
        dialog.value.set_files({**files[0], "name": "sixth-camera-photo.png"})
        expect(page.get_by_text("Select at most 5 photos.", exact=False)).to_be_visible()
        expect(selected.get_by_role("listitem")).to_have_count(5)
        for index in range(2):
            page.get_by_role("button", name=f"Remove extra-{index}.png", exact=True).click()
        expect(selected.get_by_role("listitem")).to_have_count(3)
        checks.append("Keyboard camera icon opens a single-photo environment-capture chooser; empty selection changes nothing; camera shares file validation and the five-photo limit with the normal multi-photo picker")
        capture("selection")
        page.get_by_role("button", name="Upload photos", exact=True).click()
        expect(page.get_by_text("Could not check this upload.", exact=False)).to_be_visible()
        expect(selected.get_by_role("listitem")).to_have_count(3)
        page.get_by_role("button", name="Confirm delivery", exact=True).click()
        expect(page.get_by_text("Upload your photos before confirming delivery.", exact=False)).to_be_visible()
        assert len(state["confirmations"]) == 0
        capture("upload-failure")
        if not args.recording_only: page.screenshot(animations="disabled", path=str(out / "delivery-proof-retained-360.png"), full_page=True)
        if not args.recording_only: page.locator(".proof-panel").screenshot(path=str(out / "delivery-proof-retained-panel-360.png"))
        page.get_by_role("button", name="Upload photos", exact=True).click()
        expect(page.get_by_text("Photos saved. Confirm once the item is delivered.", exact=False)).to_be_visible()
        capture("uploaded")
        assert len(state["uploads"]) == 4 and state["uploads"][1]["uploadId"] == state["uploads"][3]["uploadId"]
        checks.append("Lost PUT/verification retained all files; confirmation was denied locally; retry reused the same upload identity without reuploading successful files")
        page.get_by_label("Choose photos", exact=True).set_input_files([{**file, "name": "extra-" + file["name"]} for file in files])
        expect(page.get_by_text("Select at most 5 photos.", exact=False)).to_be_visible()
        expect(selected.get_by_role("listitem")).to_have_count(3)
        remove = page.get_by_role("button", name="Remove proof-c.png", exact=True)
        remove.click()
        expect(page.get_by_text("Could not remove this photo.", exact=False).first).to_be_visible()
        expect(selected.get_by_role("listitem")).to_have_count(3)
        remove.click()
        expect(selected.get_by_role("listitem")).to_have_count(2)
        assert state["removed"][0] == state["removed"][1]
        page.get_by_label("Choose photos", exact=True).set_input_files({"name": "proof-d.png", "mimeType": "image/png", "buffer": png})
        page.get_by_role("button", name="Upload photos", exact=True).click()
        expect(page.get_by_text("Photos saved. Confirm once the item is delivered.", exact=False)).to_be_visible()
        checks.append("Five-photo cap is separate; uncertain removal retained the file until the same retirement identity was acknowledged, then allowed a replacement")
        page.get_by_role("checkbox", name="The requester was unreachable.", exact=False).check()
        page.get_by_text("Can't upload photos?", exact=True).click()
        page.get_by_role("button", name="Confirm without photos", exact=True).click()
        expect(page.get_by_text("This action could not be completed.", exact=False)).to_be_visible()
        expect(selected.get_by_role("listitem")).to_have_count(3)
        page.get_by_role("button", name="Confirm delivery", exact=True).click()
        expect(page.get_by_text("We couldn’t check your confirmation.", exact=False)).to_be_visible()
        expect(page.get_by_label("Choose photos", exact=True)).to_be_disabled()
        expect(camera).to_be_disabled()
        expect(page.locator("#delivery-proof-camera")).to_be_disabled()
        page.reload()
        retry = page.get_by_role("button", name="Try confirmation again", exact=True)
        expect(retry).to_be_enabled()
        expect(page.get_by_text("3 saved", exact=False)).to_be_visible()
        expect(page.get_by_label("Choose photos", exact=True)).to_be_disabled()
        expect(camera).to_be_disabled()
        expect(page.get_by_role("checkbox", name="The requester was unreachable.", exact=False)).to_be_checked()
        retry.focus()
        page.keyboard.press("Tab")
        page.keyboard.press("Shift+Tab")
        capture("pending")
        if not args.recording_only: page.screenshot(animations="disabled", path=str(out / "delivery-proof-pending-360.png"), full_page=True)
        if not args.recording_only: page.locator(".proof-panel").screenshot(path=str(out / "delivery-proof-pending-panel-360.png"))
        retry.press("Enter")
        expect(page.get_by_role("heading", name="Delivery proof", exact=True)).to_be_visible()
        normal = [request for request in state["confirmations"] if not request["withoutProof"]]
        assert len(normal) == 2 and normal[0] == normal[1] and normal[0]["unattended"]
        checks.append("No uncertified outage bypass; lost confirmation/reload recovered the server's immutable three-photo unattended submission and keyboard retry sent identical input")
        state["identity"] = REQUESTER
        page.reload()
        expect(page.get_by_role("heading", name="Delivery proof", exact=True)).to_be_visible()
        expect(page.get_by_role("list", name="Saved delivery proof photos").get_by_role("listitem")).to_have_count(3)
        for width in args.capture_widths:
            page.set_viewport_size({"width": width, "height": 1000})
            expect(page.get_by_role("img", name="Delivery proof photo 1", exact=True)).to_be_visible()
            assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), f"horizontal overflow at {width}"
            if not args.recording_only: page.screenshot(animations="disabled", path=str(out / f"delivery-proof-evidence-{width}.png"), full_page=True)
            if not args.recording_only: page.locator(".proof-evidence").screenshot(path=str(out / f"delivery-proof-gallery-panel-{width}.png"))
        gallery = page.get_by_role("list", name="Saved delivery proof photos")
        expect(gallery.get_by_role("link")).to_have_count(3)
        for image in gallery.get_by_role("img").all():
            image.scroll_into_view_if_needed()
            expect(image).to_have_js_property("complete", True)
            assert image.evaluate("element => element.naturalWidth > 0")
            preview = image.locator("..")
            box = preview.bounding_box()
            pixels = image.bounding_box()
            assert box and pixels
            assert pixels["x"] >= box["x"] - 1 and pixels["y"] >= box["y"] - 1, (box, pixels)
            assert pixels["x"] + pixels["width"] <= box["x"] + box["width"] + 1, (box, pixels)
            assert pixels["y"] + pixels["height"] <= box["y"] + box["height"] + 1, (box, pixels)
        assert gallery.get_by_role("img").nth(1).evaluate("element => element.naturalHeight > element.naturalWidth")
        capture("gallery")
        state.update(identity=OTHER, role="USER_ROLE_ADMIN")
        page.reload()
        expect(page.get_by_text("Delivery proof retry fixture", exact=True)).to_be_visible()
        expect(page.get_by_role("heading", name="Delivery proof", exact=True)).to_have_count(0)
        expect(page.get_by_label("Choose photos", exact=True)).to_have_count(0)
        checks.append(f"Requester evidence gallery stays within {args.capture_widths}px; nonparticipant admin sees neither evidence nor confirmation controls")
        assert not errors, errors
        context.close()
        browser.close()
    (out / "checks.json").write_text(json.dumps({"checks": checks, "pageErrors": errors, "scope": "Mounted UI and real service adapter at explicit Connect/storage fixtures; backend seam covered separately"}, indent=2))
    print(f"DELIVERY PROOF UI PASS: {len(checks)} scenarios, {args.capture_widths}px, zero page errors")
    for check in checks:
        print("PASS:", check)


if __name__ == "__main__":
    main()
