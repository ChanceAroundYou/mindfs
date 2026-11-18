#!/usr/bin/env python3
"""Task Router R1 coordinate 隔离验收（T10）：需求→执行→主动汇报、中途留言归宿、
失败与 HumanGate、隔离进程重启对账。

用法（只在隔离实例上跑，绝不指向线上 7331）：

    python3 e2e/test_task_router_coordinate.py \
        --base http://127.0.0.1:7433/mindfs \
        --user <隔离账户 id> --root proj \
        --db /tmp/mindfs-r1-t10/config/mindfs/users/<uid>/meta/proj/tasks/task-kanban.db \
        --out /tmp/mindfs-r1-t10/t10-result.json \
        --scenarios A,B,C,D

    # 场景 D 要重启隔离实例，必须显式给隔离根/端口/仓路径；不给则只读快照并标 blocked
    ... --iso-root /tmp/mindfs-r1-t10 --iso-port 7433 --repo /path/to/worktree

边界（与 T10 brief 一致）：

- 纯标准库；`--db` **只读**打开（`mode=ro`）。脚本从不写库、从不改配置文件。
- `router_events` 没有 HTTP 端点：事件只读库，决策读 `GET .../router-decisions`。
- 只碰 `--base` 指向的实例。场景 D 只动 `--iso-root` 自己的 pid/端口，并核对线上
  7331 的 health 前后一致。
- 不采集凭据与转录；正文一律截断到 300 字符。
- 任何「未运行」写 blocked 与原因，绝不写 pass。

退出码：全部 pass = 0；有 fail = 1；模型/凭据不可用 = 2。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import traceback
import urllib.error
import urllib.request

from test_task_router_coordinate_common import (  # noqa: E402  （同目录导入，脚本直跑）
    ApiError,
    Client,
    DB,
    clip,
    is_model_unavailable,
    result,
)

SCENARIO_NAMES = {
    "A": "需求→执行→主动汇报",
    "B": "中途留言有归宿",
    "C": "失败与 HumanGate 明确说明",
    "D": "隔离进程重启对账",
}

ONLINE_PORT = 7331


def online_health(port=ONLINE_PORT):
    """线上健康检查。隔离自证的一条硬判据：开始/结束/重启前后都必须是 200。"""
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/mindfs/health", timeout=5) as resp:
            return resp.status
    except urllib.error.HTTPError as err:
        return err.code
    except (urllib.error.URLError, OSError):
        return 0


def run_scenarios(args, client, db):
    from test_task_router_coordinate_ab import scenario_a, scenario_b
    from test_task_router_coordinate_c import scenario_c
    from test_task_router_coordinate_cd import scenario_d
    from test_task_router_coordinate_fixtures import ensure_fixtures

    fixture_versions = ensure_fixtures(client)
    runners = {
        "A": lambda: scenario_a(client, db, fixture_versions),
        "B": lambda: scenario_b(client, db, fixture_versions),
        "C": lambda: scenario_c(client, db, fixture_versions),
        "D": lambda: scenario_d(client, db, fixture_versions, args.iso_root, args.iso_port, args.repo),
    }
    results = []
    for key in args.scenarios:
        label = f"[{key}] {SCENARIO_NAMES[key]}"
        print(f"→ {label} 开始", flush=True)
        started = time.time()
        try:
            entry = runners[key]()
        except (ApiError, TimeoutError, KeyboardInterrupt) as err:
            entry = result(key, False, {"error": clip(str(err), 800),
                                        "traceback": clip(traceback.format_exc(), 1200)},
                           int((time.time() - started) * 1000), "场景执行异常")
        except Exception as err:  # noqa: BLE001  验收脚本要把任何异常变成显式失败
            entry = result(key, False, {"error": clip(repr(err), 800),
                                        "traceback": clip(traceback.format_exc(), 1200)},
                           int((time.time() - started) * 1000), "脚本异常，非产品判定")
        entry["scenario"] = key
        entry["name"] = SCENARIO_NAMES[key]
        results.append(entry)
        print(f"← {label} = {entry.get('status')} ({entry['latency_ms']}ms)", flush=True)
    return results


def latencies(results):
    samples = sorted(r["latency_ms"] for r in results if r.get("status") != "blocked")
    if not samples:
        return {"samples": 0, "p50_ms": None, "p95_ms": None, "note": "样本不足"}
    out = {"samples": len(samples), "p50_ms": samples[len(samples) // 2], "p95_ms": None}
    if len(samples) < 3:
        out["note"] = "样本不足（<3），不报 p95"
    else:
        out["p95_ms"] = samples[min(len(samples) - 1, int(len(samples) * 0.95))]
    return out


def parse_scenarios(raw):
    keys = []
    for part in raw.split(","):
        part = part.strip().upper()
        if not part:
            continue
        if part not in SCENARIO_NAMES:
            raise SystemExit(f"未知场景 {part}（可选 {','.join(SCENARIO_NAMES)}）")
        keys.append(part)
    return keys


def main(argv=None):
    parser = argparse.ArgumentParser(description="Task Router R1 coordinate 隔离验收（T10）")
    parser.add_argument("--base", required=True, help="隔离实例 base，如 http://127.0.0.1:7433/mindfs")
    parser.add_argument("--user", required=True, help="隔离账户 id")
    parser.add_argument("--root", required=True, help="隔离 root id")
    parser.add_argument("--db", required=True, help="隔离 root 的 task-kanban.db（只读打开）")
    parser.add_argument("--out", default="", help="结果 JSON 输出路径")
    parser.add_argument("--scenarios", default="A,B,C,D", help="要跑的场景，逗号分隔")
    parser.add_argument("--iso-root", default="", help="隔离根（场景 D 重启用，如 /tmp/mindfs-r1-t10）")
    parser.add_argument("--iso-port", type=int, default=7433, help="隔离端口（场景 D 重启用）")
    parser.add_argument("--repo", default="", help="本 worktree 路径（场景 D 调 scripts/mindfs-iso.sh）")
    args = parser.parse_args(argv)
    args.scenarios = parse_scenarios(args.scenarios)

    if f":{ONLINE_PORT}" in args.base or ONLINE_PORT == args.iso_port:
        print(f"拒绝：不得指向线上 {ONLINE_PORT}", file=sys.stderr)
        return 2
    if "D" in args.scenarios and not args.iso_root:
        print("提示：未给 --iso-root，场景 D 只做只读快照并标 blocked", flush=True)
    if "D" in args.scenarios and args.iso_root and not args.repo:
        args.repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

    client = Client(args.base, args.user, args.root)
    try:
        db = DB(args.db)
    except ValueError as err:
        print(f"拒绝：{err}", file=sys.stderr)
        return 2
    print(f"base={args.base} user={args.user} root={args.root} scenarios={args.scenarios}", flush=True)

    health_before = online_health()
    results = run_scenarios(args, client, db)
    health_after = online_health()

    blocked = [r for r in results if r.get("status") == "blocked"]
    failed = [r for r in results if not r["pass"] and r.get("status") != "blocked"]
    payload = {
        "base": args.base,
        "root": args.root,
        "db": args.db,
        "iso": {"root": args.iso_root, "port": args.iso_port},
        "router_status": client.router_status(),
        "online_health": {"before": health_before, "after": health_after},
        "scenarios": results,
        "latency": latencies(results),
        "summary": {
            "total": len(results),
            "passed": sum(1 for r in results if r["pass"]),
            "failed": len(failed),
            "blocked": len(blocked),
        },
    }
    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)
        print(f"结果已写入 {args.out}", flush=True)
    else:
        print(json.dumps(payload, ensure_ascii=False, indent=2))

    if health_before != 200 or health_after != 200:
        print(f"警告：线上 health before={health_before} after={health_after}", file=sys.stderr)
    errors = " ".join(str((r.get("evidence") or {}).get("error") or "") for r in results)
    errors += " ".join(str((r.get("evidence") or {}).get("decision", {}).get("error") or "") for r in results)
    if blocked and len(blocked) == len(results) and is_model_unavailable(errors):
        return 2
    if failed:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
