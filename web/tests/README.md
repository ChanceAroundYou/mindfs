# Session virtualization browser regression

Start `npm run dev` in `web`, then open `/mindfs/tests/session-virtualization.html`
(the dev server runs under the `/mindfs` deploy prefix, so the bare path 404s).
Run `await window.runVirtualizationChecks()` in the browser console on a fresh page.
It throws on failure and returns measured row/request counts on success.

Run `await window.runStreamingTextChecks()` to check 16 content/timestamp updates
in both short and virtualized histories. The row, Markdown wrapper, unchanged
paragraph, and code block must retain their DOM identity while text updates.

On a fresh page, run `await window.runScrollIntentChecks()` for slow upward
scrolling from the tail. It dispatches wheel/touch intent and applies twelve
5px scroll steps, then checks reading-position retention during append/viewport
changes and automatic following after returning to the bottom. Run it at desktop
and mobile viewport sizes; synthetic touch events do not replace real-device QA.

Run `await window.runPagingChecks()` for the windowed-pagination path
(`getSessionWindow` is stubbed in-fixture, so no backend is needed). It scrolls
up until six history pages load and asserts the anchor row's real rect drift
stays within one line, then that clicking "jump to latest" lands exactly on
`scrollHeight - clientHeight` (`gap === 0`), and that the button's visibility
tracks the actual distance from bottom for programmatic scrollbar drags as well
as gestures. Pass `{only:"prepend"}` to stop after the first phase.

The fixture renders the real SessionViewer with 3,000 synthetic tool calls and
stubs detail requests. It checks bounded mounting, initial tail positioning,
search jumps, lazy detail loading, expansion/detail retention across unmounts,
reading position during append, reopening, tail following, short sessions,
large diff expansion, pending question retention, and default user-shell expansion. No live agent
or session is required. The fixture is not a production build entry point.
