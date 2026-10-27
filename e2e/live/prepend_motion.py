#!/usr/bin/env python
"""前插前后逐帧记录**同一行在屏幕上的 y**，看内容是不是在动。

`prepend_flicker.py` 只量「视口有没有空」；这里量「内容有没有位移」。
用户说的「闪」如果来自回补收敛过程中的抖动，会在 dy 序列里直接暴露。

只读：只发 GET，不发消息、不重启、不改配置。
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import harness  # noqa: E402

SAMPLER = """
window.__move = {
  log: [],
  start() {
    const el = window.__liveProbe.scroller();
    if (!el) return false;
    this.el = el;
    let n = 0;
    const frame = () => {
      const box = el.getBoundingClientRect();
      const rows = [...el.querySelectorAll('[data-timeline-key]')];
      const pos = {};
      for (const r of rows) pos[r.dataset.timelineKey] = Math.round(r.getBoundingClientRect().top - box.top);
      this.log.push({i: n++, t: Math.round(performance.now()), top: Math.round(el.scrollTop), pos});
      this.raf = requestAnimationFrame(frame);
    };
    this.raf = requestAnimationFrame(frame);
    return true;
  },
  stop() { cancelAnimationFrame(this.raf); return this.log; },
};
"""


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="")
    parser.add_argument("--root", default="mindfs")
    parser.add_argument("--session", default="")
    parser.add_argument("--min-exchanges", type=int, default=24)
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--bundle", default="", help="用这份 index-*.js 顶替线上产物（A/B 用）")
    parser.add_argument("--threshold", type=int, default=3, help="单帧位移超过多少像素算「动」")
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    print("产物：", harness.dump(harness.bundle_hashes(env)))

    from playwright.sync_api import sync_playwright
    from scroll_repro import pick_long_session

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=not args.headed)
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        if args.bundle:
            served = harness.bundle_hashes(env).get("served") or ""
            body = Path(args.bundle).read_text(encoding="utf-8", errors="replace")
            page.route(f"**/assets/{served}",
                       lambda route: route.fulfill(status=200, content_type="application/javascript", body=body))
            print(f"! 已用 {Path(args.bundle).name} 顶替线上 bundle —— 这是 A/B，不是线上真相")
        preps: list[int] = []
        page.on("request", lambda req: preps.append(1) if "before_seq=" in req.url else None)

        harness.login(page, env)
        target, root = args.session, args.root
        if not target:
            picked = pick_long_session(page, env, root, args.min_exchanges)
            if not picked:
                print("× 没有够长的会话")
                browser.close()
                return 2
            target = picked["key"]
            print(f"会话 {target}（{picked['total']} 条历史）")

        harness.open_session(page, env, root, target)
        page.wait_for_timeout(1500)
        if not page.evaluate("window.__liveProbe.metrics().virtualized"):
            print("× 该会话没走虚拟化")
            browser.close()
            return 3

        page.evaluate(SAMPLER + "window.__move.start();")
        centre = page.evaluate("""() => {
          const el = window.__liveProbe.scroller();
          const r = el.getBoundingClientRect();
          return {x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2)};
        }""")
        page.mouse.move(centre["x"], centre["y"])
        mark = len(preps)
        markT = page.evaluate("() => Math.round(performance.now())")
        for _ in range(300):
            page.mouse.wheel(0, -120)
            page.wait_for_timeout(100)
            if len(preps) > mark:
                break
        print(f"前插请求：{len(preps) - mark}")
        page.wait_for_timeout(2500)
        log = page.evaluate("window.__move.stop()")
        browser.close()
    import json
    Path("/tmp/live-motion.json").write_text(json.dumps(log))

    # 逐帧位移：只统计连续两帧都在的键
    prev = None
    moves = []
    for e in log:
        if prev is not None:
            common = set(prev["pos"]) & set(e["pos"])
            if common:
                ds = sorted(abs(e["pos"][k] - prev["pos"][k]) for k in common)
                moved = [k for k in common if abs(e["pos"][k] - prev["pos"][k]) > args.threshold]
                moves.append((e["i"], e["t"], e["top"], prev["top"], round(max(ds), 1), len(moved), len(common)))
        prev = e
    # 分两类看：
    #   手滚（|dTop| ≈ 120）：行跟着滚是应当的。
    #   回补（|dTop| 很大）：行必须**纹丝不动** —— 动了就是用户看到的「跳/闪」。
    manual = [m for m in moves if abs(m[2] - m[3]) <= 200]
    compensate = [m for m in moves if abs(m[2] - m[3]) > 200]
    bad = [m for m in compensate if m[4] > args.threshold]
    print(f"帧数 {len(log)}；手滚帧 {len(manual)}；回补帧 {len(compensate)}；回补时行仍在动：{len(bad)}")
    for m in bad[:25]:
        print(f"  回补 frame={m[0]:<5} t={m[1]:<8} scrollTop {m[3]}→{m[2]}  maxDY={m[4]}  moved={m[5]}/{m[6]}")
    for m in compensate[:15]:
        print(f"  回补 frame={m[0]:<5} t={m[1]:<8} scrollTop {m[3]}→{m[2]}  maxDY={m[4]}  moved={m[5]}/{m[6]}")
    return 1 if bad else 0


if __name__ == "__main__":
    raise SystemExit(main())
