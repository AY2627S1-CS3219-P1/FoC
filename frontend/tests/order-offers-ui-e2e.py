"""Actual mounted B1 controls at Connect fixtures, not live financial proof.

Acceptance sources: approved immutable acceptance UUID/current-order refresh,
uncertain-response and suspension key retention, explicit counteroffer retry
warning, typed rejected details, and user-authorized combined browser proof.
Run against the combined checkout's frontend. Python Playwright + Chrome needed.
"""
import argparse
import base64
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

COURIER = "11111111-1111-4111-8111-111111111111"
REQUESTER = "22222222-2222-4222-8222-222222222222"
NEWER = "88888888-8888-4888-8888-888888888888"
ORDER = "66666666-6666-4666-8666-666666666666"
PROPOSAL_ORDER = "99999999-9999-4999-8999-999999999999"
OFFER = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
ATTEMPT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"


def stamp(delta=0):
    return (datetime.now(timezone.utc) + timedelta(seconds=delta)).isoformat().replace("+00:00", "Z")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:5187")
    parser.add_argument("--out", default=".local/order-offers-ui-evidence")
    parser.add_argument("--channel", default="chrome")
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    origin = args.url.rstrip("/")
    state = {"identity": COURIER, "suspended": False, "courier": None,
             "accept": [], "proposal": [], "counteroffer": [], "offers": [], "reads": 0}
    assertions, page_errors = [], []
    cors = {"Access-Control-Allow-Origin": origin, "Access-Control-Allow-Credentials": "true",
            "Access-Control-Allow-Headers": "content-type,authorization,connect-protocol-version,connect-timeout-ms",
            "Access-Control-Allow-Methods": "GET,POST,OPTIONS", "Access-Control-Expose-Headers": "*"}

    def order(oid):
        courier = state["courier"] if oid == ORDER else None
        value = {"id": oid, "requesterId": REQUESTER, "deliveryBuildingName": "COM3",
                 "status": "ORDER_STATUS_AWAITING_PICKUP" if courier else "ORDER_STATUS_AWAITING_COURIER",
                 "pickup": {"locationId": "33333333-3333-4333-8333-333333333333", "name": "Campus Cafe", "buildingName": "COM3"},
                 "itemDescription": "Acceptance recovery fixture", "rewardCredits": "20", "deliveryDuration": "1800s",
                 "expiresAt": stamp(1800), "revision": "4" if courier else "2", "createdAt": stamp(-60), "updatedAt": stamp()}
        if courier:
            value["courierId"] = courier
        if state["identity"] in (REQUESTER, courier):
            value["delivery"] = {"location": {"locationId": "44444444-4444-4444-8444-444444444444", "name": "Room 02-15", "buildingName": "COM3"}, "instructions": "Call outside"}
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
            respond(route, {"accessToken": "fixture-not-a-real-credential", "user": {"id": state["identity"], "email": "fixture@example.test", "displayName": "Fixture", "role": "USER_ROLE_SUSPENDED_USER" if state["suspended"] else "USER_ROLE_USER"}})
        elif method == "GetOrder":
            state["reads"] += 1
            respond(route, {"order": order(body["id"])})
        elif method == "ListOffers":
            respond(route, {"offers": state["offers"] if body["orderId"] == PROPOSAL_ORDER else []})
        elif method == "AcceptOrder":
            state["accept"].append(body)
            if len(state["accept"]) == 1:
                state["courier"] = COURIER
                route.abort("connectionreset")
            elif state["suspended"]:
                respond(route, {"code": "permission_denied", "message": "Suspended after earlier admission"}, 403)
            else:
                state["courier"] = NEWER
                respond(route, {"attempt": {"attemptId": body["attemptId"], "status": "ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED"}, "order": order(ORDER)})
        elif method == "MakeOffer":
            state["proposal"].append(body)
            offer = {"id": OFFER, "orderId": PROPOSAL_ORDER, "courierId": COURIER, "credits": body["credits"], "status": "OFFER_STATUS_OPEN", "createdAt": stamp()}
            state["offers"] = [offer]
            respond(route, {"offer": offer})
        elif method == "AcceptOffer":
            state["counteroffer"].append(body)
            if len(state["counteroffer"]) == 1:
                respond(route, {"attempt": {"attemptId": ATTEMPT, "status": "ASSIGNMENT_ATTEMPT_STATUS_COMPENSATING"}})
            else:
                # External protobuf contract: string attempt_id=1, enum status=2
                # (REJECTED=3), string rejection=3. Connect error detail is binary.
                aid, reason = ATTEMPT.encode(), b"insufficient_credits"
                wire = bytes([10, len(aid)]) + aid + bytes([16, 3, 26, len(reason)]) + reason
                respond(route, {"code": "failed_precondition", "message": "Recorded financial rejection", "details": [{"type": "order.v1.AssignmentAttemptResult", "value": base64.b64encode(wire).decode()}]}, 400)
        else:
            respond(route, {"code": "unimplemented", "message": method}, 501)

    report = {"status": "FAIL", "scope": "Actual shared detail composition and B1 controls using Connect fixtures; not real DB/RPC/Credit/browser-to-live-server proof", "assertions": assertions}
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True, channel=args.channel)
        context = browser.new_context(viewport={"width": 360, "height": 900}, record_video_dir=str(out / "video"))
        context.route("**/*", route_request)
        page = context.new_page()
        page.set_default_timeout(20000)
        expect.set_options(timeout=20000)
        page.on("pageerror", lambda error: page_errors.append(str(error)))
        try:
            page.goto(origin + "/orders/" + ORDER)
            controls = page.get_by_role("region", name="Acceptance and offers", exact=True)
            accept = controls.get_by_role("button", name="Accept current reward (20 credits)", exact=True)
            expect(accept).to_be_enabled()
            for width in (360, 768, 1440):
                page.set_viewport_size({"width": width, "height": 900})
                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"overflow at {width}"
                page.screenshot(path=str(out / f"controls-{width}.png"), full_page=True)
            assertions.append("Shared protected singleton/detail snippet mounts actual B1 controls at 360/768/1440 without horizontal overflow")
            accept.click()
            expect(controls.get_by_text("Keep the saved identity", exact=False)).to_be_visible()
            assert len(state["accept"]) == 1
            original = state["accept"][0].copy()
            key = f"order-offers:v1:{COURIER}:{ORDER}"
            stored = page.evaluate("key => JSON.parse(sessionStorage.getItem(key))", key)
            assert stored["key"] == original["attemptId"] and stored["outcome"] == "uncertain"
            state["suspended"] = True
            controls.get_by_role("button", name="Retry the same saved request", exact=True).click()
            expect(controls.get_by_text("Your account cannot", exact=False)).to_be_visible()
            assert len(state["accept"]) == 2 and state["accept"][1] == original
            assert page.evaluate("key => JSON.parse(sessionStorage.getItem(key)).outcome", key) == "uncertain"
            expect(controls.get_by_role("button", name="Start a deliberate new action", exact=True)).to_have_count(0)
            assertions.append("Lost acceptance response then PermissionDenied preserves original UUID and uncertain state, never unlocks a new intent")
            page.reload()
            expect(controls.get_by_text("Your account can read its history", exact=False)).to_be_visible()
            expect(controls.get_by_role("button", name="Retry the same saved request", exact=True)).to_be_disabled()
            assert page.evaluate("key => JSON.parse(sessionStorage.getItem(key)).key", key) == original["attemptId"]
            page.screenshot(path=str(out / "suspended-retained-attempt.png"), full_page=True)
            assertions.append("Suspended reload retains historical detail/saved UUID while mutation controls are disabled")
            state["suspended"] = False
            page.reload()
            retry = controls.get_by_role("button", name="Retry the same saved request", exact=True)
            expect(retry).to_be_enabled()
            retry.click()
            expect(controls.get_by_text("This earlier attempt succeeded.", exact=False)).to_be_visible()
            expect(page.locator("dd").filter(has_text=NEWER)).to_be_visible()
            assert len(state["accept"]) == 3 and all(value == original for value in state["accept"])
            expect(controls.get_by_role("button", name="Accept current reward (20 credits)", exact=True)).to_be_disabled()
            assertions.append("Restored user retries same UUID; historical success is distinct from refreshed CURRENT courier, with no new assignment claim")
            page.goto(origin + "/orders/" + PROPOSAL_ORDER)
            expect(controls.get_by_label("Counteroffer credits", exact=True)).to_be_enabled()
            controls.get_by_label("Counteroffer credits", exact=True).fill("30")
            controls.get_by_role("button", name="Submit / replace my open offer", exact=True).click()
            expect(controls.get_by_text("Proposal recorded.", exact=False)).to_be_visible()
            assert len(state["proposal"]) == 1 and state["proposal"][0]["credits"] == "30"
            assert len(state["accept"]) == 3
            assertions.append("Courier proposal uses actual factory and does not claim assignment or start acceptance")
            state["identity"] = REQUESTER
            page.reload()
            offer_accept = controls.get_by_role("button", name="Accept 30-credit offer", exact=True)
            expect(offer_accept).to_be_enabled()
            offer_accept.click()
            expect(controls.get_by_text("Credit recovery is pending.", exact=False)).to_be_visible()
            repeat = controls.get_by_role("button", name="Repeat saved acceptance (may start a new financial attempt)", exact=True)
            dialogs = []
            def dismiss(dialog):
                dialogs.append(dialog.message)
                dialog.dismiss()
            page.once("dialog", dismiss)
            repeat.click()
            assert len(state["counteroffer"]) == 1 and dialogs and "new financial attempt" in dialogs[0]
            page.once("dialog", lambda dialog: dialog.accept())
            repeat.click()
            expect(controls.get_by_text("Insufficient credits.", exact=False)).to_be_visible()
            expect(controls.get_by_role("button", name="Start a deliberate new action", exact=True)).to_be_enabled()
            assert len(state["counteroffer"]) == 2 and state["counteroffer"][0] == state["counteroffer"][1] == {"id": OFFER}
            page.screenshot(path=str(out / "typed-counteroffer-rejection.png"), full_page=True)
            assertions.append("Requester sees compensating rather than success; explicit warning gates repeated acceptance, typed rejected detail stays failure")
            assert not page_errors, page_errors
            report.update(status="PASS", acceptance_requests=len(state["accept"]), proposal_requests=len(state["proposal"]), counteroffer_requests=len(state["counteroffer"]), page_errors=page_errors)
        except Exception as error:
            report["failure"] = str(error)
            page.screenshot(path=str(out / "failure.png"), full_page=True)
            raise
        finally:
            context.close()
            browser.close()
            (out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
            print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
