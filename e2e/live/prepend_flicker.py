#!/usr/bin/env python
"""上翻加载时「全屏闪烁」的逐帧取证 —— 对着**线上**跑（也可指到隔离实例）。

口径：视口被已挂载行覆盖的百分比。全屏闪烁 = 某一帧覆盖率掉到很低（视口空了一块），
而滚动容器与内容层都还在。这个数只依赖 DOM 矩形，不依赖截图，慢机器上也读得准。

    .venv/bin/python e2e/live/prepend_flicker.py
    ... --url http://127.0.0.1:7431/mindfs --root proj --session <key>

只读：只发 GET，不发消息、不重启、不改配置。
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import harness  # noqa: E402

# 采样器装在页面里：每帧记一次覆盖率，另外用 MutationObserver 记每次结构提交的瞬间
# （微任务阶段，即浏览器要绘制的那个状态）。
SAMPLER_JS = """
window.__flick = {
  log: [],
  start() {
    const scroller = () => window.__liveProbe.scroller();
    const el = scroller();
    if (!el) return false;
    this.el = el;
    const coverage = () => {
      const box = el.getBoundingClientRect();
      const top = box.top, bottom = top + el.clientHeight;
      const spans = [];
      for (const row of el.querySelectorAll('[data-timeline-key]')) {
        const r = row.getBoundingClientRect();
        const a = Math.max(r.top, top), z = Math.min(r.bottom, bottom);
        if (z > a) spans.push([a - top, z - top]);
      }
      spans.sort((x, y) => x[0] - y[0]);
      let covered = 0, cur = null;
      for (const s of spans) {
        if (!cur) { cur = s.slice(); continue; }
        if (s[0] <= cur[1]) cur[1] = Math.max(cur[1], s[1]);
        else { covered += cur[1] - cur[0]; cur = s.slice(); }
      }
      if (cur) covered += cur[1] - cur[0];
      return Math.round((covered / el.clientHeight) * 100);
    };
    const snap = (why) => {
      const rows = el.querySelectorAll('[data-timeline-key]');
      const first = rows[0];
      this.log.push({
        why, t: Math.round(performance.now()),
        top: Math.round(el.scrollTop), pct: coverage(), rows: rows.length,
        first: first ? first.dataset.timelineKey : null,
      });
    };
    this.snap = snap;
    snap('start');
    const frame = () => {
      snap('raf');
      this.raf = requestAnimationFrame(frame);
    };
    this.raf = requestAnimationFrame(frame);
    const box = el.querySelector('[data-timeline-key]')?.parentElement;
    this.box = box;
    this.obs = new MutationObserver((records) => {
      if (records.some(r => r.type === 'childList')) snap('commit');
    });
    if (box) this.obs.observe(box, { childList: true });
    return true;
  },
  stop() {
    cancelAnimationFrame(this.raf);
    if (this.obs) this.obs.disconnect();
    return this.log;
  },
};
"""


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="", help="覆盖 .env 里的 MINDFS_LIVE_URL")
    parser.add_argument("--root", default="mindfs")
    parser.add_argument("--session", default="")
    parser.add_argument("--min-exchanges", type=int, default=24)
    parser.add_argument("--settle-ms", type=int, default=4000)
    parser.add_argument("--threshold", type=int, default=60, help="覆盖率低于这个值算「闪」")
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--bundle", default="", help="用这份 index-*.js 顶替线上产物（A/B 用）")
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    print("产物：", harness.dump(harness.bundle_hashes(env)))

    from playwright.sync_api import sync_playwright

    sys.path.insert(0, str(Path(__file__).resolve().parent))
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
        preps: list[float] = []
        page.on("request", lambda req: preps.append(1) if "before_seq=" in req.url else None)

        harness.login(page, env)
        target, root = args.session, args.root
        if not target:
            picked = pick_long_session(page, env, root, args.min_exchanges)
            if not picked:
                print(f"× {root} 下没有 ≥{args.min_exchanges} 条的会话")
                browser.close()
                return 2
            target = picked["key"]
            print(f"会话 {target}（{picked['total']} 条历史）")

        harness.open_session(page, env, root, target)
        page.wait_for_timeout(1500)
        before = page.evaluate("window.__liveProbe.metrics()")
        print("加载后：", harness.dump(before))
        if not before.get("virtualized"):
            print("× 该会话没走虚拟化，闪烁路径走不到")
            browser.close()
            return 3

        started = page.evaluate(SAMPLER_JS + "window.__flick.start();")
        if not started:
            print("× 采样器没装上（找不到滚动容器）")
            browser.close()
            return 1

        # 滚进提前量带（一屏）：分步上滚，每步都落在已渲染区间里。
        mark = len(preps)
        for step in range(200):
            moved = page.evaluate("""() => {
              const el = window.__liveProbe.scroller();
              const before = el.scrollTop;
              el.scrollTop = Math.max(0, before - Math.round(el.clientHeight * 0.25));
              return {before: Math.round(before), after: Math.round(el.scrollTop)};
            }""")
            page.wait_for_timeout(120)
            if step % 10 == 0:
                print(f"  step {step}: {moved}")
            if len(preps) > mark:
                break
        print(f"前插请求：{len(preps) - mark}")
        print("上滚后：", harness.dump(page.evaluate("window.__liveProbe.metrics()")))
        page.wait_for_timeout(args.settle_ms)
        log = page.evaluate("window.__flick.stop()")

        browser.close()

    blank = [e for e in log if e["pct"] < args.threshold]
    print(f"采样 {len(log)} 条（{len([e for e in log if e['why'] == 'raf'])} 帧，"
          f"{len([e for e in log if e['why'] == 'commit'])} 次提交）")
    if not blank:
        print(f"✓ 没有覆盖率低于 {args.threshold}% 的帧 —— 在 {args.url or env['MINDFS_LIVE_URL']} 上没复现闪烁")
        return 0
    print(f"× {len(blank)} 条覆盖率 < {args.threshold}%：")
    for e in blank[:20]:
        print(f'  {e["why"]:<7} t={e["t"]:<8} top={e["top"]:<7} pct={e["pct"]:<4} '
              f'rows={e["rows"]:<3} first={(e["first"] or "?")[-18:]}')
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
