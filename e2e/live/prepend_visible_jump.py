#!/usr/bin/env python
"""前插期间**用户持续上滚**时，量「视口内可见行有没有跳」。

`prepend_motion.py` 在前插请求发出后就停手了 —— 那测的是「回补单独发生的形态」。
真人是**轮子/手指不停**，回补与手势叠在一起。`prepend_flicker.py` 只量空白（覆盖率），
看不见「内容整体位移一截」这种闪。

判据（不需要知道『是谁写的 scrollTop』）：

    取**视口内最靠上那一行**当锚。它上面只有屏外内容，内容只会在它上方被插入。
    行在屏幕上的 y 就是 `contentY - scrollTop`，所以对锚行有

        屏幕位移 dy  ==  上方插入的高度 dH  -  scrollTop 变化 dS

    而「用户滚了多少」在探针里是自己写的，可以精确记账（`dProbe`）。用户滚动是本测试里
    唯一**允许**改变屏幕位置的原因，于是正确行为恰好是

        dy  ==  -dProbe

    产品把 `dH` 补掉之后，右边就只剩用户自己的滚动了。任何偏离 —— 补多了、补少了、
    补晚了一帧 —— 都直接体现为 dy ≠ -dProbe。不需要区分「产品写的」和「虚拟库写的」，
    那是上一版判据的老路：帧粒度的归属推断必然出错（同一帧里回补写 +9864、用户滚 -120，
    只看 dS 会把两者合成 +9744），而眼睛只看 dy。

为什么由探针自己写 scrollTop（而不是 page.mouse.wheel）：`dProbe` 必须可记账。代价是
没有真实 wheel 事件（产品里 wheel 只负责「让位吸底」，而本测试从底部往上滚，stick 早已
是 false），所以这条路径与真人等价。`ponytail:` 若将来产品给 wheel 增加别的语义，
这里要换成「真实 wheel + 逐帧事件归因」。

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
window.__jump = {
  log: [], probeWritten: 0, writing: false,
  start(step, interval) {
    const el = window.__liveProbe.scroller();
    if (!el) return false;
    this.el = el;
    // 只给**探针自己**的写入记账；产品与虚拟库的写入一律不记 —— 判据不需要它。
    const desc = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
    const self = this;
    Object.defineProperty(el, 'scrollTop', {
      get() { return desc.get.call(this); },
      set(v) {
        const before = desc.get.call(this);
        desc.set.call(this, v);
        if (self.writing) self.probeWritten += desc.get.call(this) - before;
      },
      configurable: true,
    });
    this.anchorKey = '';
    let n = 0;
    const frame = () => {
      const box = el.getBoundingClientRect();
      // 锚行 = 视口内最靠上那一行。它上面只有屏外内容，插入只发生在它上方。
      let anchor = null;
      for (const r of el.querySelectorAll('[data-timeline-key]')) {
        const y = Math.round(r.getBoundingClientRect().top - box.top);
        if (y >= -1 && (anchor === null || y < anchor.y)) {
          anchor = {k: String(r.dataset.timelineKey), y};
        }
      }
      this.log.push({i: n++, t: Math.round(performance.now()),
                     top: Math.round(el.scrollTop), h: el.clientHeight,
                     sh: Math.round(el.scrollHeight), w: Math.round(this.probeWritten),
                     a: anchor ? anchor.k : null, y: anchor ? anchor.y : null});
      this.raf = requestAnimationFrame(frame);
    };
    this.raf = requestAnimationFrame(frame);
    this.timer = setInterval(() => {
      self.writing = true;
      el.scrollTop -= step;
      self.writing = false;
    }, interval);
    return true;
  },
  stop() {
    cancelAnimationFrame(this.raf);
    clearInterval(this.timer);
    return this.log;
  },
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
    parser.add_argument("--step", type=int, default=60, help="每次上滚多少像素")
    parser.add_argument("--interval", type=int, default=16, help="两次上滚间隔 ms")
    parser.add_argument("--after", type=int, default=2500, help="前插触发后继续滚多久 ms")
    parser.add_argument("--threshold", type=int, default=4, help="dy 偏离 -dProbe 多少像素算「跳」")
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    print("产物：", harness.dump(harness.bundle_hashes(env)))

    from playwright.sync_api import sync_playwright
    from scroll_repro import pick_long_session

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=not args.headed)
        # 显式关掉 service worker：`--bundle` 是 page.route 顶替线上产物，而 SW 是在
        # 自己的 target 里 fetch 的，page.route 拦不到它 —— 不关就可能两边都是同一份产物。
        ctx = browser.new_context(viewport={"width": 1280, "height": 900}, service_workers="block")
        page = ctx.new_page()
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

        page.evaluate(f"{SAMPLER}window.__jump.start({args.step}, {args.interval});")
        mark = len(preps)
        # 一直滚到前插触发，然后**不停手**再滚 --after 毫秒。
        fired_at = None
        for _ in range(600):
            page.wait_for_timeout(args.interval)
            if fired_at is None and len(preps) > mark:
                fired_at = page.evaluate("() => Math.round(performance.now())")
            elif fired_at is not None and page.evaluate(
                "() => Math.round(performance.now())"
            ) - fired_at > args.after:
                break
        # 注意：**不能**在 `scrollTop` 触顶时提前退出。提前量是一整屏，而请求发出后到
        # 回包之间只剩不到一屏的余量，按正常步长必然先滚到顶；此时退出就恰好漏掉前插
        # 落地的那一帧 —— 采样窗口里一次 10000px 级的 `scrollHeight` 跳变都没有，
        # 所有产物（包括修好之前那版）都会「通过」。滚到顶之后继续采样即可：那时
        # 探针不再写入，屏上发生的一切都归产品，正是最该看的一段。
        print(f"前插请求：{len(preps) - mark}；触发时刻 {fired_at}")
        log = page.evaluate("window.__jump.stop()")
        browser.close()
    Path("/tmp/live-jump.json").write_text(json.dumps(log))

    prev = None
    checked = 0
    laid_out = 0
    bad: list[tuple] = []
    for e in log:
        if prev is not None and prev["a"] and e["a"] == prev["a"]:
            if not (0 <= prev["y"] <= prev["h"] - 1):
                prev = e
                continue
            d_probe = e["w"] - prev["w"]
            dy = e["y"] - prev["y"]
            if abs(dy + d_probe) > args.threshold:
                bad.append((e["i"], e["t"], prev["top"], e["top"], dy, -d_probe, e["sh"] - prev["sh"]))
            else:
                checked += 1
            if e["sh"] != prev["sh"]:
                laid_out += 1
        prev = e

    print(f"帧 {len(log)}；锚行可比对的帧 {checked + len(bad)}；其中内容高度变化 {laid_out} 帧")
    # 覆盖度本身也要判定：没发生过前插、或没有一帧跨越内容变化，这一跑什么都证明不了。
    wrote = any(e["w"] != 0 for e in log)
    if not wrote or laid_out == 0:
        print(f"× 这一跑没验到东西：探针上滚={wrote}，跨内容变化帧={laid_out} —— 结论不作数")
        return 4
    if not bad:
        print(f"✓ 锚行的屏幕位移始终等于探针自己的滚动量（阈值 {args.threshold}px）—— 没复现抖动")
        return 0
    print(f"× {len(bad)} 帧里锚行的屏幕位移 ≠ 探针的滚动量（产品把内容位移补错了）：")
    for b in bad:
        print(f'  frame={b[0]:<5} t={b[1]:<7} scrollTop {b[2]}→{b[3]}  '
              f'屏幕位移={b[4]:<6} 应为={b[5]:<6} 内容高度变化={b[6]}')
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
