# Delivery proof UI: desktop and mobile verification

The mounted order page uses its real Svelte components, proof controller and Connect adapter. Playwright intercepts authentication, order, proof and storage requests with explicit synthetic fixtures. Parcel images are generated and labeled as synthetic. These captures establish layout and browser interactions, not live authentication, persistence, storage, codec quality or delivery truth.

Before captures use the unchanged proof UI at `a56dde8c2713dac73a472fbcabd69e6ec2fb74a7`. After captures add an unbroken long filename and a portrait preview and reach the native chooser using Tab and open it with Space. Viewports are 360, 390, 768 and 1440 pixels wide, with 1000-pixel viewport height. Images below are panel crops; full-page context is linked separately.

## Screenshots

Each cell links **before / after** for the same state and viewport. Selection after-captures include a deliberately long filename.

| State | 360px | 390px | 768px | 1440px |
| --- | --- | --- | --- | --- |
| No photos | [before](before/empty-panel-360.png) / [after](after/empty-panel-360.png) | [before](before/empty-panel-390.png) / [after](after/empty-panel-390.png) | [before](before/empty-panel-768.png) / [after](after/empty-panel-768.png) | [before](before/empty-panel-1440.png) / [after](after/empty-panel-1440.png) |
| Three selected | [before](before/selection-panel-360.png) / [after](after/selection-panel-360.png) | [before](before/selection-panel-390.png) / [after](after/selection-panel-390.png) | [before](before/selection-panel-768.png) / [after](after/selection-panel-768.png) | [before](before/selection-panel-1440.png) / [after](after/selection-panel-1440.png) |
| Partial upload failure and denied confirmation | [before](before/upload-failure-panel-360.png) / [after](after/upload-failure-panel-360.png) | [before](before/upload-failure-panel-390.png) / [after](after/upload-failure-panel-390.png) | [before](before/upload-failure-panel-768.png) / [after](after/upload-failure-panel-768.png) | [before](before/upload-failure-panel-1440.png) / [after](after/upload-failure-panel-1440.png) |
| Photos checked | [before](before/uploaded-panel-360.png) / [after](after/uploaded-panel-360.png) | [before](before/uploaded-panel-390.png) / [after](after/uploaded-panel-390.png) | [before](before/uploaded-panel-768.png) / [after](after/uploaded-panel-768.png) | [before](before/uploaded-panel-1440.png) / [after](after/uploaded-panel-1440.png) |
| Reloaded immutable pending confirmation | [before](before/pending-panel-360.png) / [after](after/pending-panel-360.png) | [before](before/pending-panel-390.png) / [after](after/pending-panel-390.png) | [before](before/pending-panel-768.png) / [after](after/pending-panel-768.png) | [before](before/pending-panel-1440.png) / [after](after/pending-panel-1440.png) |
| Saved evidence gallery | [before](before/gallery-panel-360.png) / [after](after/gallery-panel-360.png) | [before](before/gallery-panel-390.png) / [after](after/gallery-panel-390.png) | [before](before/gallery-panel-768.png) / [after](after/gallery-panel-768.png) | [before](before/gallery-panel-1440.png) / [after](after/gallery-panel-1440.png) |

Full-page context: [mobile gallery](after/gallery-full-360.png), [desktop gallery](after/gallery-full-1440.png).

[Interaction recording](retry-reload.mp4) shows selection, failed upload, retained files, removal retry, outage rejection, lost confirmation, reload and keyboard retry. The recording holds the browser at 360x1000 and slows browser actions for inspection. Native-resolution screenshots show the wider layouts.

## Repeat the browser check

Requires Node 22.12+, Python 3, Playwright, Pillow 10.1+ and Chrome. Install frontend dependencies with `npm ci` in `frontend`, and Python dependencies with `python3 -m pip install playwright 'Pillow>=10.1'`. Run checks/builds before starting the browser capture because SvelteKit sync/build can refresh the dev app and discard in-memory selection.

From `frontend`, start `npm run dev -- --host 127.0.0.1 --port 5196`. When Vite reports ready, run from the repository root:

```sh
python3 frontend/tests/delivery-proof-ui-e2e.py \
  --url http://127.0.0.1:5196 --browser-channel chrome \
  --out .local/delivery-proof-ui
```

Observed: four scenarios passed with zero browser page errors. The check verifies retained files after lost PUT/verification, same upload identity on retry, no confirmation before all photos pass, the five-photo cap, removal recovery, rejection of an uncertified outage, immutable pending recovery after reload, identical confirmation input on keyboard retry, participant-only evidence and no horizontal page overflow at the four captured widths. Tab reached the native file chooser, Space opened it, and it accepted the unbroken long-name selection. The gallery exposes one link per photo and includes a portrait preview. The fixed-mobile recording run also checks that image boxes fit their preview frames.

For the fixed-mobile recording, use the same command with `--recording-only --capture-widths 360 --slow-mo 200` and a separate `--out` directory. This skips screenshot captures, whose temporary viewport changes can affect Chrome video frames.

Frontend verification: `npm run check` reported zero errors/warnings, `npm test` passed 112 tests in 16 files, and `npm run build` completed. The build reports existing adapter-auto environment-detection and transitive browser-module notices. Backend code is unchanged and backend/live-service checks were not rerun for this presentation change. Stop only the Vite process started for this walkthrough. The fixture does not write live records.
