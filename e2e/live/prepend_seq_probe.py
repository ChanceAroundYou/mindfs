#!/usr/bin/env python
"""前插前后的**有序**事件日志（scroll / wheel / 结构提交 / rAF），带锚行屏幕 y。

`prepend_visible_jump.py` 只能给「帧边界快照」，会把一帧里的多次写入压成一次，
看不出是谁把 scrollTop 写多的。这里把顺序摊开：

    wheel  用户手势（浏览器随后自己改 scrollTop）
    scroll 浏览器派发（虚拟器的 offset 只在这里更新）
    commit 结构提交（微任务，即将绘制）
    raf    帧边界

判据仍是残差：行在屏幕上的位移 dy 应恒等于 -(scrollTop 变化)，除非内容高度变了。
所以每条 scroll 之后看 `dy + dTop` 不为 0 就是内容高度变了（或别人也写了 scrollTop）。

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
window.__seq = {
  log: [], n: 0,
  push(kind, extra) {
    const el = this.el;
    const node = this.key ? el.querySelector(`[data-timeline-key="${CSS.escape(this.key)}"]`) : null;
    const box = el.getBoundingClientRect();
    this.log.push(Object.assign({
      k: kind, n: this.n++, t: +performance.now().toFixed(1),
      top: Math.round(el.scrollTop), sh: Math.round(el.scrollHeight),
      y: node ? Math.round(node.getBoundingClientRect().top - box.top) : null,
      rows: el.querySelectorAll('[data-timeline-key]').length,
    }, extra || {}));
  },
  start() {
    const el = window.__liveProbe.scroller();
    if (!el) return false;
    this.el = el;
    const box = el.getBoundingClientRect();
    const a = [...el.querySelectorAll('[data-timeline-key]')]
      .find(r => r.getBoundingClientRect().top >= box.top - 1);
    if (!a) return false;
    this.key = a.dataset.timelineKey;

    el.addEventListener('scroll', () => this.push('scroll'), {passive: true});
    el.addEventListener('wheel', (e) => this.push('wheel', {dy: e.deltaY}), {passive: true});
    const parent = a.parentElement;
    this.obs = new MutationObserver((recs) => {
      if (recs.some(r => r.type === 'childList')) this.push('commit');
    });
    this.obs.observe(parent, {childList: true});
    const frame = () => { this.push('raf'); this.raf = requestAnimationFrame(frame); };
    this.raf = requestAnimationFrame(frame);
    this.push('start');
    return this.key;
  },
  stop() { cancelAnimationFrame(this.raf); if (this.obs) this.obs.disconnect(); return {key: this.key, log: this.log}; },
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
    parser.add_argument("--after", type=int, default=2500)
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
        key = page.evaluate(SAMPLER + "window.__seq.start();")
        print(f"锚行 {key}")
        fired = None
        for _ in range(400):
            page.mouse.wheel(0, -args.step)
            page.wait_for_timeout(args.step and 16)
            if fired is None and len(preps) > mark:
                fired = page.evaluate("() => +performance.now().toFixed(1)")
                page.wait_for_timeout(args.after)
                break
        print(f"前插请求：{len(preps) - mark}；触发时刻 {fired}")
        out = page.evaluate("window.__seq.stop()")
        browser.close()

    log = out["log"]
    Path("/tmp/live-seq.json").write_text(json.dumps(out))
    # 只打印前插触发前后 2.5 秒内的记录，并且只保留状态真正变了的行
    print(f"记录 {len(log)} 条")
    print(f"{'n':<6}{'t':<9}{'kind':<8}{'top':<7}{'sh':<8}{'y':<7}{'rows':<6}{'dy':<7}{'dTop':<8}{'dSH':<8}residual")
    prev = None
    for e in log:
        if prev is None:
            show = True
            dy = dTop = dsh = None
        else:
            dy = None if (e["y"] is None or prev["y"] is None or e["rows"] != prev["rows"] and e["y"] is None) else e["y"] - prev["y"]
            dTop = e["top"] - prev["top"]
            dsh = e["sh"] - prev["sh"]
            res = None if dy is None else dy + dTop
            show = bool(dTop) or e["k"] in ("commit",) or (dy not in (None, 0)) or e["k"] == "wheel"
        if show:
            res = None if (prev is None or dy is None) else dy + dTop
            print(f'{e["n"]:<6}{e["t"]:<9}{e["k"]:<8}{e["top"]:<7}{e["sh"]:<8}{str(e["y"]):<7}{e["rows"]:<6}'
                  f'{str(dy):<7}{str(dTop):<8}{str(dsh):<8}{str(res)}')
        prev = e
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
