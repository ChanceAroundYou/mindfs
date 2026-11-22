#!/usr/bin/env python3
"""W5 验收：回合结算 · 会话跟进 · 重启恢复（隔离实例，真实模型）。

三条现场各一个场景，互不依赖，可单独跑：

  A  #100 型：直连长回合（>10 min）→ 期间卡片摘要不是转交口信 → 结算后有一条可读总结
  B  #68  型：任务绑定会话里用户自己推一轮 → 提问卡作答 → turn_settled 带 out_of_band /
             answered_question，任务在 running ↔ waiting_user 之间跟随
  C  #106 型：长回合中途 kill -9（整棵进程树，模拟断电）→ 5 min 内出现 turn_interrupted
             与可见说明 → supervision=on 自动重试一轮并成功

边界：只读 SQLite（`mode=ro`），只动隔离实例；生产 7331 全程不碰。C 会重启隔离实例，
所以必须给 --iso-root / --iso-port / --iso-src（与 `scripts/mindfs-iso.sh` 的三个环境变量同义）；
C 的机械部分（杀进程树 / 重启 / 判据）在同目录 `test_router_turn_settle_follow_restart.py`。

用法：
  python3 e2e/test_router_turn_settle_follow.py --scenario a \
    --base http://127.0.0.1:7436/mindfs --user <uid> --root proj \
    --db /tmp/mindfs-w5/config/mindfs/users/<uid>/meta/proj/tasks/task-kanban.db

（任务库跟着账户元数据走：`<config-dir>/users/<uid>/meta/<root>/tasks/task-kanban.db`；
项目目录下 `.mindfs/` 只有会话与工作区数据。）
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import sys
import time
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import websocket  # noqa: E402

from test_task_router_coordinate_common import (  # noqa: E402
    DB,
    SETTLE_TIMEOUT,
    Client,
    clip,
    result,
    wait_for,
)

POLL = 3.0
# 转交口信：Router 把活交出去后的占位，不是结果。卡片摘要里出现即算回归。
HANDOFF_MARKERS = ("已交给执行会话", "交给执行会话", "完成后我会汇总")
# 四段式（规则 18）与兜底摘要（turn_summary.go）两套结构，任一成立即可读。
ROUTER_MARKERS = ("结论", "证据", "没做", "未验证", "下一步")
FALLBACK_MARKERS = ("本轮未生成总结", "用户诉求", "最后进展", "状态：")

A_PROMPT = (
    "这是一次通道压测，请严格按顺序执行，不要提问、不要省略、不要合并、不要改用其他命令：\n"
    "连续执行 13 次 Bash 工具调用。第 n 次（n=1..13）的命令是 `sleep 60 && echo TICK <n>`"
    "（把 <n> 换成序号），timeout 参数设为 120000。必须等上一次返回后再发起下一次。\n"
    "13 次全部跑完后，只回复一行：DONE 13"
)

B_SEED = "只回复一行 READY，不要调用任何工具，也不要做别的事。"
B_PROMPT = (
    "请严格按下面两步做，不要做别的事：\n"
    "1. 用 AskUserQuestion 工具问我：问题原文「这次验收用哪个代号？」，选项只有「蓝鲸」和「白鹭」；\n"
    "2. 用 AskUserQuestion 工具再问我：问题原文「验收日期按哪天算？」，选项只有「今天」和「明天」。\n"
    "两个问题必须分两次工具调用、按顺序问。都答完之后只回复一行：两个答案都收到了。"
)


# ── 共用取证 ────────────────────────────────────────────────────────────────


def summary_reason(text):
    """卡片摘要是否可读。返回 None 表示合格，否则是给人看的原因。"""
    text = (text or "").strip()
    if not text:
        return "摘要是空的"
    if len(text) > 600:
        return f"摘要 {len(text)} 字，超过 600 硬上限"
    if any(marker in text for marker in HANDOFF_MARKERS):
        return "摘要是转交口信，不是结果"
    markers = [m for m in ROUTER_MARKERS + FALLBACK_MARKERS if m in text]
    if len(markers) < 2:
        return f"摘要没有结构（只命中 {markers}），读起来是一段原文"
    return None


def summary_source(text):
    text = text or ""
    if any(m in text for m in ROUTER_MARKERS):
        return "router"
    if any(m in text for m in FALLBACK_MARKERS):
        return "fallback"
    return "unknown"


def events(db, task_id, kind=None):
    sql = (
        "SELECT id, kind, instance_key, turn_id, status, payload_json, created_at"
        " FROM router_events WHERE task_id = ?"
    )
    args = [task_id]
    if kind:
        sql += " AND kind = ?"
        args.append(kind)
    return db.query(sql + " ORDER BY created_at ASC, id ASC", tuple(args))


def event_payload(row):
    try:
        return json.loads(row.get("payload_json") or "{}")
    except json.JSONDecodeError:
        return {}


def parse_ts(value):
    try:
        return datetime.datetime.fromisoformat(str(value).replace("Z", "+00:00")).timestamp()
    except ValueError:
        return 0.0


def decisions(db, task_id):
    rows = db.query(
        "SELECT id, trigger_kind, status, turn_id, actions_json, applied_seq, created_at"
        " FROM router_decisions WHERE task_id = ? ORDER BY created_at ASC, id ASC",
        (task_id,),
    )
    for row in rows:
        try:
            row["actions"] = json.loads(row.get("actions_json") or "[]")
        except json.JSONDecodeError:
            row["actions"] = []
    return rows


def decision_kinds(row):
    kinds = []
    for action in row.get("actions") or []:
        if isinstance(action, dict):
            kinds.append(str(action.get("kind") or action.get("type") or ""))
    return kinds


def settle_summary_body(row):
    """结算回合里承担总结的那条 say 正文。

    与 `activity_route_reply.go::settleSummaryActionSeq` 同义：显式
    `purpose=summary` 优先；没写就由最后一条 say 承担（运行期事件回合的兜底）。
    返回空串表示这条决策没有 say 动作。
    """
    says = [a for a in row.get("actions") or []
            if isinstance(a, dict) and str(a.get("kind") or a.get("type") or "") == "say"]
    if not says:
        return ""
    explicit = [a for a in says if str(a.get("purpose") or "") == "summary"]
    return str((explicit or says)[-1].get("body") or "").strip()


class WSClient:
    """只发不读的 WS 客户端：答案与留言都由服务端广播确认，轮询 HTTP 更方便取证。"""

    def __init__(self, base, user, client_id="w5-follow"):
        parsed = urllib.parse.urlparse(base)
        ws_base = "ws://" + parsed.netloc + parsed.path.rstrip("/") + "/ws"
        query = urllib.parse.urlencode({"client_id": client_id, "user": user})
        self.url = ws_base + "?" + query
        self.conn = websocket.create_connection(self.url, timeout=20)
        self.seq = 0

    def send(self, frame_type, payload):
        self.seq += 1
        frame_id = f"w5-{int(time.time() * 1000)}-{self.seq}"
        self.conn.send(json.dumps({"id": frame_id, "type": frame_type, "payload": payload},
                                  ensure_ascii=False))
        return frame_id

    def close(self):
        try:
            self.conn.close()
        except Exception:  # noqa: BLE001 —— 关闭失败不影响取证结论
            pass


def payload_questions(payload):
    """提问卡的题干：直写 questions，或 adapter 原样透传的 input（JSON 字符串）。"""
    raw = payload.get("questions")
    if isinstance(raw, list):
        return raw
    text = payload.get("input")
    if not isinstance(text, str) or not text.strip():
        return []
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        return []
    questions = parsed.get("questions") if isinstance(parsed, dict) else None
    return questions if isinstance(questions, list) else []


def answers_for(activity, preferred=("蓝鲸", "今天")):
    """从提问卡 payload 生成答案：每题选首选标签，没有首选就选第一项。"""
    try:
        payload = json.loads(activity.get("payload_json") or "{}")
    except json.JSONDecodeError:
        payload = {}
    questions = payload_questions(payload if isinstance(payload, dict) else {})
    answers, labels = {}, []
    for index, question in enumerate(questions):
        options = [str((opt or {}).get("label") or "").strip()
                   for opt in (question.get("options") or []) if isinstance(opt, dict)]
        options = [opt for opt in options if opt]
        chosen = next((label for label in preferred if label in options), options[0] if options else "是")
        answers[f"q_{index}"] = chosen
        labels.append(chosen)
    return answers, questions, labels


def waiting_questions(client, task_id):
    return [a for a in client.activities(task_id)
            if a.get("type") == "question" and a.get("status") == "waiting_user"]


# ── A / #100 型 ──────────────────────────────────────────────────────────────


def scenario_a(client, db, opts):
    started = time.time()
    task_id = client.create_task("W5-A 直连长回合")
    client.post_activity(task_id, A_PROMPT, choice="disabled")
    evidence = {"task_id": task_id, "prompt_ticks": 13}
    wait_for(lambda: client.task(task_id), lambda t: (t or {}).get("status") == "running",
             180, "task running (A)")

    samples = []
    for delay, label in ((120, "2min"), (600, "10min")):
        target = started + delay
        while time.time() < target:
            time.sleep(min(15, max(1, target - time.time())))
        card = client.task(task_id) or {}
        samples.append({"at": label, "status": card.get("status"),
                        "summary": clip(card.get("result_summary") or "", 200)})
    evidence["mid_turn"] = samples
    # 「期间」才算：采样点必须还在跑，且摘要不是转交口信。
    a2 = all(s["status"] == "running" and not any(m in (s["summary"] or "") for m in HANDOFF_MARKERS)
             for s in samples)

    # 回合结束后先拿兜底摘要，再等 Router 的结算回合（预算 300s）。
    task = wait_for(lambda: client.task(task_id),
                    lambda t: (t or {}).get("status") not in ("running", "pending", "paused"),
                    SETTLE_TIMEOUT + 900, "long turn finished (A)")
    elapsed = time.time() - started
    evidence["turn_seconds"] = round(elapsed)

    def current_summary():
        card = client.task(task_id) or {}
        text = card.get("result_summary") or ""
        return {"summary": text, "status": card.get("status"),
                "source": summary_source(text), "reason": summary_reason(text)}

    # 兜底摘要是回合结束那一刻先落的占位，Router 的结算回合随后才用四段式覆盖它
    # （实测应用延迟 10–25 s）。这里必须等到 Router 那版，否则会在占位上空过断言。
    try:
        final = wait_for(current_summary, lambda v: v["source"] == "router", 300, "router summary (A)")
    except TimeoutError:
        final = current_summary()
    reason = None if final.get("source") == "router" else final.get("reason") or "Router 没有覆盖兜底摘要"
    evidence["summary"] = clip(final.get("summary") or "", 600)
    evidence["summary_source"] = final.get("source")
    evidence["turn_settled"] = [event_payload(e) for e in events(db, task_id, "turn_settled")][-2:]
    evidence["decisions"] = [{"trigger": d["trigger_kind"], "status": d["status"],
                              "kinds": decision_kinds(d)} for d in decisions(db, task_id)]
    evidence["model_task_status"] = task.get("status")

    settled = events(db, task_id, "turn_settled")
    a4 = bool(settled) and int(event_payload(settled[-1]).get("duration_ms") or 0) >= 600_000
    return result(
        "A", a2 and reason is None and a4,
        evidence, int(elapsed * 1000),
        notes=("；".join(filter(None, [
            f"直连回合 {evidence['turn_seconds']}s",
            "" if a2 else "回合中卡片摘要出现转交口信",
            "" if reason is None else f"摘要不合格：{reason}",
            "" if a4 else "没有 ≥10 min 的 turn_settled 事件",
        ])) or "三条断言全过"),
    )


# ── B / #68 型 ──────────────────────────────────────────────────────────────


def scenario_b(client, db, opts):
    started = time.time()
    task_id = client.create_task("W5-B 会话跟进")
    client.post_activity(task_id, B_SEED, choice="disabled")
    task = wait_for(lambda: client.task(task_id),
                    lambda t: (t or {}).get("status") == "waiting_user" and (t or {}).get("main_session_key"),
                    SETTLE_TIMEOUT, "seed turn done + main session (B)")
    session_key = task.get("main_session_key") or ""
    summary_before = task.get("result_summary") or ""
    evidence = {"task_id": task_id, "session_key": session_key,
                "summary_before": clip(summary_before, 200)}

    ws = WSClient(opts.base, opts.user)
    ws_sent_at = time.time()
    try:
        ws.send("session.message", {
            "root_id": opts.root, "session_key": session_key, "type": "chat",
            "agent": "claude", "content": B_PROMPT,
        })
        running = wait_for(lambda: client.task(task_id),
                           lambda t: (t or {}).get("status") == "running", 180, "out-of-band turn running (B)")
        evidence["status_during"] = running.get("status")

        answered, labels, cards = 0, [], 0
        done_cards = set()
        deadline = time.time() + 600
        while time.time() < deadline and answered < 2:
            pending = [a for a in waiting_questions(client, task_id)
                       if a.get("id") not in done_cards]
            if not pending:
                time.sleep(POLL)
                continue
            cards += 1
            card = pending[-1]
            done_cards.add(card.get("id"))
            answers, questions, chosen = answers_for(card)
            if not answers:
                raise RuntimeError(f"提问卡没有 questions：{clip(card.get('payload_json') or '', 400)}")
            ws.send("session.answer_question", {
                "root_id": opts.root, "session_key": session_key, "agent": "claude",
                "tool_use_id": card.get("tool_use_id") or "", "answers": answers,
            })
            answered += len(answers)
            labels.extend(chosen)

            def card_row(card_id=card.get("id")):
                rows = [a for a in client.activities(task_id) if a.get("id") == card_id]
                return rows[0] if rows else None

            wait_for(card_row, lambda row: row is not None and row.get("status") in ("completed", "cancelled"),
                     120, f"answer accepted for {card.get('id')}")
        evidence.update({"answered": answered, "labels": labels, "answer_cards": cards})

        # 先等这一轮真的结算：种子回合的 Router 决策可能还在飞，任务状态可能被它
        # 提前翻回 waiting_user，直接等状态会误以为本回合已经结束。
        wait_for(lambda: [e for e in events(db, task_id, "turn_settled")
                          if parse_ts(e["created_at"]) >= ws_sent_at - 2],
                 lambda rows: bool(rows), SETTLE_TIMEOUT, "WS turn settled (B)")
        final = wait_for(lambda: client.task(task_id),
                         lambda t: (t or {}).get("status") == "waiting_user", SETTLE_TIMEOUT,
                         "task back to waiting_user (B)")
        evidence["status_after"] = final.get("status")
    finally:
        ws.close()

    settled = events(db, task_id, "turn_settled")
    flags = [event_payload(e) for e in settled]
    evidence["turn_settled_count"] = len(settled)
    evidence["turn_settled_flags"] = [{"out_of_band": p.get("out_of_band"),
                                       "answered_question": p.get("answered_question")} for p in flags]

    def readable():
        current = client.task(task_id) or {}
        return {"summary": current.get("result_summary") or ""}

    # 摘要必须由**这一轮**结算回合写出来：种子回合也会生成一条 summary，只判
    # 「有可读摘要」会被它顶包（脚本刚起就跑完的话尤其明显）。所以要求存在一条
    # out-of-band 结算之后 applied 的 turn_settled 决策，且它的总结正文就是任务
    # 当前的 result_summary。
    ws_settle_at = min(
        (parse_ts(e["created_at"]) for e, p in zip(settled, flags) if p.get("out_of_band")),
        default=time.time(),
    )
    evidence["ws_settle_at"] = ws_settle_at

    def summary_tied():
        current = client.task(task_id) or {}
        text = current.get("result_summary") or ""
        if summary_reason(text) is not None:
            return {"summary": text, "tied": False, "body": ""}
        body = ""
        for row in decisions(db, task_id):
            if row["trigger_kind"] != "turn_settled" or row["status"] != "applied":
                continue
            if parse_ts(row["created_at"]) < ws_settle_at - 5:
                continue
            candidate = settle_summary_body(row)
            if candidate and (candidate[:40] in text or text in candidate):
                body = candidate
        return {"summary": text, "tied": bool(body), "body": body}

    try:
        summary = wait_for(summary_tied, lambda v: v["tied"], 300, "summary tied to WS turn (B)")
    except TimeoutError:
        summary = summary_tied()
    reason = summary_reason(summary.get("summary"))
    evidence["summary"] = clip(summary.get("summary") or "", 600)
    evidence["summary_source"] = summary_source(summary.get("summary"))
    evidence["summary_tied"] = summary.get("tied")
    evidence["summary_body"] = clip(summary.get("body") or "", 300)
    evidence["summary_mentions_answer"] = any(label and label in (summary.get("summary") or "")
                                              for label in labels)
    evidence["decisions"] = [{"trigger": d["trigger_kind"], "status": d["status"],
                              "kinds": decision_kinds(d)} for d in decisions(db, task_id)]

    flagged = any(p.get("out_of_band") and p.get("answered_question") for p in flags)
    b1 = evidence.get("status_during") == "running"
    b2 = evidence["answered"] >= 2 and len([
        a for a in client.activities(task_id) if a.get("type") == "user_answer"
    ]) >= 1
    p = result(
        "B", b1 and b2 and flagged and reason is None and summary.get("tied"),
        evidence, int((time.time() - started) * 1000),
        notes=("；".join(filter(None, [
            "" if b1 else "任务没有在推进时变成 running",
            "" if b2 else f"作答计数不足：{evidence['answered']}",
            "" if flagged else "没有同时带 out_of_band + answered_question 的 turn_settled",
            "" if reason is None else f"摘要不合格：{reason}",
            "" if summary.get("tied") else "摘要不是这一轮结算回合写的（可能沿用种子回合）",
        ])) or "四条断言全过"),
    )
    return p


def main():
    parser = argparse.ArgumentParser(description="W5 回合结算 / 会话跟进 / 重启恢复验收")
    parser.add_argument("--scenario", default="abc", help="a / b / c，可组合（默认 abc）")
    parser.add_argument("--base", required=True, help="隔离实例 base，例如 http://127.0.0.1:7436/mindfs")
    parser.add_argument("--user", required=True, help="隔离账户 id")
    parser.add_argument("--root", default="proj", help="隔离 root id")
    parser.add_argument("--db", required=True, help="隔离 root 的 task-kanban.db")
    parser.add_argument("--iso-root", default="", help="MINDFS_ISO_ROOT（C 需要）")
    parser.add_argument("--iso-port", type=int, default=0, help="MINDFS_ISO_PORT（C 需要）")
    parser.add_argument("--iso-src", default="", help="MINDFS_ISO_SRC（C 需要）")
    parser.add_argument("--repo", default=os.path.dirname(HERE), help="仓库根（找 mindfs-iso.sh）")
    opts = parser.parse_args()
    if ":7331" in opts.base:
        raise SystemExit("拒绝在线上实例上跑：--base 指向 7331，本脚本只动隔离实例")

    client = Client(opts.base, opts.user, opts.root)
    db = DB(opts.db)
    status = client.router_status()
    print(f"base={opts.base} root={opts.root} router={status}", flush=True)
    if status.get("mode") != "coordinate":
        raise SystemExit(f"router.mode 不是 coordinate（{status.get('mode')}）：先按 W5 配置重启实例")

    # C 会 kill -9 并重启实例，机械部分单独一个文件（守 500 行硬线）；它反过来
    # 又 import 本文件的取证工具，所以只能在这里延迟导入，避免循环导入。
    from test_router_turn_settle_follow_restart import scenario_c

    runners = {"a": scenario_a, "b": scenario_b, "c": scenario_c}
    out = []
    for key in opts.scenario.lower():
        if key not in runners:
            raise SystemExit(f"unknown scenario {key}")
        print(f"── 场景 {key.upper()} ──", flush=True)
        try:
            out.append(runners[key](client, db, opts))
        except (TimeoutError, RuntimeError) as exc:
            out.append(result(key.upper(), False, {"error": clip(str(exc), 800)}, 0,
                              notes="脚本中断（细节见 error）"))
        print(json.dumps(out[-1], ensure_ascii=False), flush=True)
    passed = sum(1 for item in out if item.get("pass"))
    print(f"\n{passed}/{len(out)} PASS", flush=True)
    return 0 if passed == len(out) else 1


if __name__ == "__main__":
    sys.exit(main())
