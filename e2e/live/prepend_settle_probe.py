#!/usr/bin/env python
"""前插之后逐帧盯**锚行**：屏幕 y、内容坐标 offsetTop、scrollHeight、scrollTop。

用来回答一个问题：加载完成后那一下位移，是「回补写多了」还是「回补之后内容高度又变了」。
两者在数据上分得很开 ——

    ｜锚行屏幕 y 变 且 offsetTop 不变   ⇒ 是我们把 scrollTop 写错了（回补过度/不足）
    ｜锚行屏幕 y 变 且 offsetTop 跟着变 ⇒ 是内容上方的高度变了（估算行被实测纠正），
    ｜                                    回补已经收工，没人再管

只读：只发 GET，不发消息、不重启、不改配置。
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import harness  # noqa: E402

SAMPLER = """
window.__anchor = {
  log: [],
  start() {
    const el = window.__liveProbe.scroller();
    if (!el) return false;
    this.el = el;
    // 选视口内最靠顶的那一行当锚：它就是 loadMore 里的锚行。
    const box = el.getBoundingClientRect();
    const rows = [...el.querySelectorAll('[data-timeline-key]')];
    const a = rows.find(r => r.getBoundingClientRect().top >= box.top - 1);
    if (!a) return false;
    this.key = a.dataset.timelineKey;
    let n = 0;
    const frame = () => {
      const node = el.querySelector(`[data-timeline-key="${CSS.escape(this.key)}"]`);
      const b = el.getBoundingClientRect();
      this.log.push({
        i: n++, t: Math.round(performance.now()),
        top: Math.round(el.scrollTop), sh: Math.round(el.scrollHeight), h: el.clientHeight,
        y: node ? Math.round(node.getBoundingClientRect().top - b.top) : null,
        ot: node ? Math.round(node.offsetTop) : null,
        rows: el.querySelectorAll('[data-timeline-key]').length,
      });
      this.raf = requestAnimationFrame(frame);
    };
    this.raf = requestAnimationFrame(frame);
    return this.key;
  },
  stop() { cancelAnimationFrame(this.raf); return {key: this.key, log: this.log}; },
};
"""


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="")
    parser.add_argument("--root", default="mindfs")
    parser.add_argument("--session", default="")
    parser.add_argument("--min-exchanges", type=int, default=24)
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--step", type=int, default=120)
    parser.add_argument("--after", type=int, default=2500, help="前插触发后继续滚多久 ms")
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    print("产物：", harness.dump(harness.bundle_hashes(env)))

    from playwright.sync_api import sync_playwright
    from scroll_repro import pick_long_session

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=not args.headed)
        ctx = browser.new_context(viewport={"width": 1280, "height": 900}, service_workers="block")
        page = ctx.new_page()
        preps: list[float] = []
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

        centre = page.evaluate("""() => {
          const el = window.__liveProbe.scroller();
          const r = el.getBoundingClientRect();
          return {x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2)};
        }""")
        page.mouse.move(centre["x"], centre["y"])
        mark = len(preps)
        fired = None
        for _ in range(400):
            page.mouse.wheel(0, -args.step)
            page.wait_for_timeout(16)
            if len(preps) > mark:
                fired = page.evaluate("() => Math.round(performance.now())")
                break
        if fired is None:
            print("× 没能触发前插")
            browser.close()
            return 4
        # 从前插触发那一刻起才装采样器：锚行必须是**前插前就已挂载**的那一行。
        key = page.evaluate(SAMPLER + "window.__anchor.start();")
        print(f"锚行 {key}；触发时刻 {fired}")
        page.mouse.move(5, 5)  # 停手，只看收尾
        page.wait_for_timeout(args.after)
        out = page.evaluate("window.__anchor.stop()")
        browser.close()

    log = out["log"]
    Path("/tmp/live-anchor.json").write_text(json.dumps(out))
    print(f"帧 {len(log)}")
    print(f"{'frame':<7}{'t':<8}{'top':<7}{'sh':<8}{'y':<8}{'ot':<9}{'dy':<7}{'dSH':<8}{'dOT':<7}rows")
    for k, e in enumerate(log):
        p = log[k - 1] if k else None
        dy = dsh = dot = None
        if p:
            if e["y"] is not None and p["y"] is not None:
                dy = e["y"] - p["y"]
            dsh = e["sh"] - p["sh"]
            if e["ot"] is not None and p["ot"] is not None:
                dot = e["ot"] - p["ot"]
        # 只打印有变化的帧 + 前 6 帧
        interesting = k < 6 or (dy not in (None, 0)) or dsh or (dot not in (None, 0))
        if interesting:
            print(f'{e["i"]:<7}{e["t"]:<8}{e["top"]:<7}{e["sh"]:<8}{str(e["y"]):<8}{str(e["ot"]):<9}'
                  f'{str(dy):<7}{str(dsh):<8}{str(dot):<7}{e["rows"]}')
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
