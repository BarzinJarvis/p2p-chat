# P2P Chat v3.0 — Progress

## ✅ Completed Features

### Phase 1 — Critical Bug Fixes
- **P1-A**: Fixed multi-user voice call — `onnegotiationneeded` now queues pending negotiations with `_pendingNego` flag instead of silently dropping them when `_makingOffer` is true
- **P1-B**: Fixed reply button disappearing on desktop hover — added `:hover`/`:focus` persist CSS and pseudo-element side buffer for seamless mouse transition
- **P1-C**: File bubble `user-select:none` already existed in CSS; long-press on `.msg-row` works correctly

### Phase 2 — Core UX Features
- **P2-A**: Scroll-to-bottom button (Telegram style) — shows unread count badge when user scrolls up and new messages arrive, hides on click or manual scroll to bottom
- **P2-B**: Edit sent messages — long-press → select → edit button (pencil icon) fills input with current text, shows yellow "Editing" preview bar, updates DOM + broadcasts `edit-msg` via WS, shows "edited" label
- **P2-C**: File size validation (200MB max) — client-side check before upload modal, XHR with `upload.onprogress` for real progress bar, server `maxUpload` updated to 200MB
- **P2-D**: Dark/Light mode toggle — sun/moon button in drawer header, CSS variables in `[data-theme=light]`, persists to localStorage
- **P2-E**: Disconnect retry popup — full-screen overlay with countdown (5s auto-retry), beep sound on disconnect, manual retry/cancel buttons
- **P2-F**: RTC connection beep — plays beep on `disconnected`/`failed` state, positive tone on `connected`

### Phase 3 — UI/UX Features
- **P3-A**: User status dots (green online indicator) + device agent detection (📱/🖥️ icons in user list)
- **P3-C**: Room admin + delete messages — first user = admin, admin badge in user list, admin can delete any message via multi-select
- **P3-H**: Message sent sound — soft beep on successful send

### Backend Changes
- `hub.go`: Added `edit-msg` and `delete-msg` message type handlers (broadcast to room)
- `main.go`: Updated `maxUpload` to 200MB, updated error message
- `sw.js`: Cache version updated to `p2pchat-v3.0`

## Test Results
- **9/9 Playwright smoke tests passed** ✅
  - Alice joins, Bob joins, message delivery, theme toggle, scroll button (show+hide), voice call, disconnect overlay present, edit preview present

## Not Yet Implemented (nice-to-haves)
- P3-B: User details popup (click to see info)
- P3-D: Markdown support + code blocks
- P3-E: Video fullscreen orientation
- P3-F: Voice message reply preview
- P3-G: Modern upload animation (SVG ring)
- P3-I: Multi-line input transition (already works, just no CSS transition)
