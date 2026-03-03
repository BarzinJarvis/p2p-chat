# P2P Chat RTC Fixes — Test Results (2026-03-01)

## Fixes Applied

### BUG 1 — Voice RTC status bar UI
- Added `.voice-conn-bar` CSS class with card background, border-radius, padding, flexbox layout
- Replaced inline `style` on `#voice-conn-bar` with `class="voice-conn-bar"` + `style="display:none"`
- Each peer entry now shows: **bold name**, colored status dot, status label
- Bar hidden (`display:none`) when no peers or voice inactive; shown (`display:flex`) when active

### BUG 2 — One-way video (phone can't see PC video/screen share)
- **A)** Removed `if(pc.signalingState!=='stable'){return;}` check after `createOffer()` in `onnegotiationneeded`. Now lets `setLocalDescription()` throw naturally if state changed (caught by try/catch). Added `if(s._makingOffer)return;` guard at top.
- **B)** Changed initiator `renegotiateAll()` from 300ms to 500ms, and made it conditional on `localVideoStream||screenStream` existing.
- **C)** Verified `_addVideoToPeers` has `setTimeout(()=>renegotiateAll(),150)` — confirmed working.
- **D)** `toggleScreenShare` already calls `_addVideoToPeers(screenStream)` which includes `setTimeout(()=>renegotiateAll(),150)`, plus an additional explicit `setTimeout(()=>renegotiateAll(),150)` after.
- **E)** Added `track.onunmute` listener in `addOrUpdateVideoTile` to retry `vid.play()` on mobile.

### BUG 3 — RTC connection broken after leaving voice
- Fixed `cleanupVoice()`: now nulls out audio senders (`replaceTrack(null)`) on all peers BEFORE stopping local tracks.
- Verified `toggleVoice` re-join: uses `kindSender` → `nullSender` → `addTrack` fallback chain — correctly reuses nulled audio senders.
- Fixed `pc.onconnectionstatechange`: refreshes `audioEl.srcObject` and retries `.play()` when state becomes `connected` after failure/disconnect.

### Other
- `sw.js` cache already at `'p2pchat-v2.9'` — no change needed.

## Test Results

| Test | Result |
|------|--------|
| T1: Desktop voice call (2 peers, leave+rejoin) | ✅ PASS |
| T2: Video overlay opens on vcall-btn click | ✅ PASS |
| T3: Mobile emulation (Pixel 5, join + voice) | ✅ PASS |

- **JS console errors**: 0
- **Screenshots taken**: 8 (01–08)
- **Connected dots after rejoin**: 2 (confirms BUG 3 fix works)
- **Voice conn bar visible**: true on both desktop and mobile (confirms BUG 1 fix)

## Remaining Notes
- Screen share cannot be fully tested headless (requires `getDisplayMedia` which needs user gesture)
- Cross-device video (BUG 2) requires real network testing between phone and PC
- The `renegotiateAll` function still has a `signalingState!=='stable'` check which correctly skips unstable peers
