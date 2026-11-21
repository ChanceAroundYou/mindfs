#!/usr/bin/env python3
"""T21 双实例隔离验收：离线优先的本地控制面。

两个真实进程、两份独立 XDG_CONFIG_HOME：

  主节点 A  https://127.0.0.1:7470  control_sync.role=primary
  边缘   B  http://127.0.0.1:7471   control_sync.role=edge → https://127.0.0.1:7470

覆盖计划里的六个场景：
  1. 在线转发：边缘的控制面 PUT 落到主节点，本机权威存储不动；
  2. 断网降级：主节点停掉后边缘进 offline-local，并把副本交给权威存储；
  3. 离线写：边缘控制面写落本地并记 pending；
  4. 重启保持：主节点仍不可达时重启边缘，模式与本地值都还在；
  5. 恢复无冲突：主节点回来，pending 推上去，两侧一致；
  6. 恢复有冲突：两侧改同一个键，按域冻结、出冲突卡、裁决后收敛。

绝不碰 7331：端口与配置目录都由本脚本新建，退出时（--keep 除外）整个根目录删掉。
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import socket
import sqlite3
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request

PRIMARY_PORT = 7470
EDGE_PORT = 7471
PREF_PATH = "/api/preferences/idle-session-resource-release"
PREF_KEY = "idle_session_resource_release_hours"
PIN_PATH = "/api/pins/session"
PIN_KEY = "e2e-session"


class Fail(RuntimeError):
    pass


def check(cond: bool, msg: str) -> None:
    if not cond:
        raise Fail(msg)


def log(msg: str) -> None:
    print(f"[e2e] {msg}", flush=True)


# ---------------------------------------------------------------- HTTP client


def _request(method, url, body=None, cafile=None, timeout=10):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    ctx = ssl.create_default_context(cafile=cafile) if cafile else None
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
            raw = resp.read()
            status = resp.status
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        status = exc.code
    try:
        parsed = json.loads(raw) if raw else None
    except json.JSONDecodeError:
        parsed = raw.decode(errors="replace")
    return status, parsed


class Node:
    def __init__(self, name, base, cafile=None):
        self.name = name
        self.base = base.rstrip("/")
        self.cafile = cafile

    def call(self, method, path, body=None, timeout=15):
        return _request(method, self.base + path, body, self.cafile, timeout)

    def wait_up(self, timeout=40):
        deadline = time.time() + timeout
        last = None
        while time.time() < deadline:
            try:
                status, _ = self.call("GET", "/health", timeout=2)
                if status == 200:
                    return
            except Exception as exc:  # noqa: BLE001 - 探活期间任何异常都只代表还没起来
                last = exc
            time.sleep(0.3)
        raise Fail(f"{self.name} 在 {timeout}s 内没起来：{last}")

    def prefs_hours(self):
        status, body = self.call("GET", PREF_PATH)
        check(status == 200, f"{self.name} GET {PREF_PATH} → {status} {body!r}")
        return body["hours"]


# ---------------------------------------------------------------- process mgmt


def port_free(port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            sock.bind(("127.0.0.1", port))
        except OSError:
            return False
    return True


def wait_port_free(port, timeout=20):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if port_free(port):
            return
        time.sleep(0.3)
    raise Fail(f"端口 {port} 仍被占用（有别的实例没停干净）")


class Instance:
    def __init__(self, name, binary, config_dir, log_path, port):
        self.name = name
        self.binary = binary
        self.config_dir = config_dir  # $XDG_CONFIG_HOME
        self.log_path = log_path
        self.port = port
        self.proc = None

    def start(self):
        check(self.proc is None, f"{self.name} 已经在跑")
        # 端口被别人占着时新进程会静默退出（或干脆服务另一份数据），
        # 后面的探活会打到别人身上 —— 先拦下来。
        if not port_free(self.port):
            raise Fail(f"{self.name} 的端口 {self.port} 已被占用，先停掉遗留实例")
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = self.config_dir
        env.pop("MINDFS_STATIC_DIR", None)
        logf = open(self.log_path, "ab")
        self.proc = subprocess.Popen(
            [self.binary, "--foreground"],
            env=env,
            stdout=logf,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        log(f"{self.name} 启动 pid={self.proc.pid} log={self.log_path}")

    def stop(self):
        if self.proc is None:
            return
        proc, self.proc = self.proc, None
        proc.terminate()
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=10)
        # 进程退出 ≠ 端口释放完毕；让后续场景从确定的状态开始。
        wait_port_free(self.port)
        log(f"{self.name} 已停止")

    def tail(self, lines=20):
        try:
            with open(self.log_path, encoding="utf-8", errors="replace") as fh:
                return "".join(fh.readlines()[-lines:])
        except OSError:
            return "(没有日志)"


# ---------------------------------------------------------------- certs/config


def make_certs(root):
    ca_key = os.path.join(root, "ca.key")
    ca_crt = os.path.join(root, "ca.crt")
    srv_key = os.path.join(root, "server.key")
    srv_csr = os.path.join(root, "server.csr")
    srv_crt = os.path.join(root, "server.crt")
    ext = os.path.join(root, "san.cnf")
    with open(ext, "w", encoding="utf-8") as fh:
        fh.write("basicConstraints=critical,CA:FALSE\n"
                 "keyUsage=critical,digitalSignature,keyEncipherment\n"
                 "extendedKeyUsage=serverAuth\n"
                 "subjectAltName=IP:127.0.0.1,DNS:localhost\n")
    run = lambda *args: subprocess.run(args, check=True, capture_output=True)  # noqa: E731
    run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2",
        "-keyout", ca_key, "-out", ca_crt, "-subj", "/CN=mindfs-e2e-ca",
        "-addext", "basicConstraints=critical,CA:TRUE",
        "-addext", "keyUsage=critical,keyCertSign,cRLSign")
    run("openssl", "req", "-newkey", "rsa:2048", "-nodes", "-days", "2",
        "-keyout", srv_key, "-out", srv_csr, "-subj", "/CN=127.0.0.1")
    run("openssl", "x509", "-req", "-days", "2", "-in", srv_csr,
        "-CA", ca_crt, "-CAkey", ca_key, "-CAcreateserial",
        "-out", srv_crt, "-extfile", ext)
    return ca_crt, srv_crt, srv_key


def write_config(path, cfg):
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(cfg, fh, indent=2)


def prepare(root, binary):
    for sub in ("primary", "edge"):
        os.makedirs(os.path.join(root, sub, "mindfs"), exist_ok=True)
    os.makedirs(os.path.join(root, "logs"), exist_ok=True)
    ca_crt, srv_crt, srv_key = make_certs(root)
    token_path = os.path.join(root, "sync-token")
    with open(token_path, "w", encoding="utf-8") as fh:
        fh.write("e2e-token-0123456789\n")
    os.chmod(token_path, 0o600)

    primary_xdg = os.path.join(root, "primary")
    edge_xdg = os.path.join(root, "edge")
    write_config(os.path.join(primary_xdg, "mindfs", "config.json"), {
        "addr": f"127.0.0.1:{PRIMARY_PORT}",
        "tls": True,
        "cert": srv_crt,
        "key": srv_key,
        "webPush": False,
        "control_sync": {
            "enabled": True,
            "role": "primary",
            "instance_id": "e2e-primary",
            "token_file": token_path,
        },
    })
    write_config(os.path.join(edge_xdg, "mindfs", "config.json"), {
        "addr": f"127.0.0.1:{EDGE_PORT}",
        "webPush": False,
        "control_sync": {
            "enabled": True,
            "role": "edge",
            "instance_id": "e2e-edge",
            "primary_url": f"https://127.0.0.1:{PRIMARY_PORT}",
            "ca_file": ca_crt,
            "token_file": token_path,
        },
    })

    primary = Node("primary", f"https://127.0.0.1:{PRIMARY_PORT}", cafile=ca_crt)
    edge = Node("edge", f"http://127.0.0.1:{EDGE_PORT}")
    primary_proc = Instance("primary", binary, primary_xdg,
                            os.path.join(root, "logs", "primary.log"), PRIMARY_PORT)
    edge_proc = Instance("edge", binary, edge_xdg,
                         os.path.join(root, "logs", "edge.log"), EDGE_PORT)
    return primary, edge, primary_proc, edge_proc, primary_xdg, edge_xdg


# ---------------------------------------------------------------- assertions


def mode_of(edge):
    status, body = edge.call("GET", "/api/control-sync/status", timeout=5)
    check(status == 200, f"边缘 status → {status} {body!r}")
    return body.get("mode")


def wait_mode(edge, want, timeout, note=""):
    deadline = time.time() + timeout
    seen = None
    while time.time() < deadline:
        try:
            seen = mode_of(edge)
        except Exception:  # noqa: BLE001 - 重启窗口内连不上是正常的
            seen = None
        if seen == want:
            log(f"模式到达 {want} {note}")
            return
        time.sleep(1)
    raise Fail(f"等待模式 {want} 超时（最后看到 {seen}）{note}")


def wait_mode_stable(edge, want, timeout, settle, note=""):
    """到达目标模式后继续盯 settle 秒，任何一拍离开都算失败。

    只在瞬间命中会漏掉「刚降到 offline-local 就被一拍迟到的追平收尾拉回 online」：
    现场整段离线期都停在 online，期间每次写入都被当成在线请求转发给不可达的主节点。
    """
    wait_mode(edge, want, timeout, note)
    deadline = time.time() + settle
    while time.time() < deadline:
        current = mode_of(edge)
        check(current == want,
              f"模式 {note} 到达 {want} 后 {settle}s 内又变成 {current}")
        time.sleep(0.3)


def read_prefs_file(xdg):
    path = os.path.join(xdg, "mindfs", "preferences.json")
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        return {}


def prefs_hours_on_disk(xdg):
    return read_prefs_file(xdg).get(PREF_KEY)


def wait_prefs_hours_on_disk(xdg, want, timeout, note=""):
    deadline = time.time() + timeout
    seen = None
    while time.time() < deadline:
        seen = prefs_hours_on_disk(xdg)
        if seen == want:
            return
        time.sleep(0.5)
    raise Fail(f"等待本地偏好 {want} 超时（最后看到 {seen}）{note}")


def conflict_cards(edge):
    status, body = edge.call("GET", "/api/control-sync/conflicts")
    check(status == 200, f"conflicts → {status} {body!r}")
    return body.get("conflicts") or []


def open_db_ro(xdg):
    """只读打开同步库：读路径不能回放 / 检查点残留的 WAL，否则会改掉现场。"""
    path = os.path.join(xdg, "mindfs", "control_sync.db")
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


def replica_domains(xdg):
    """副本里有哪些域。主节点启动即发布基线，边缘首次追平就该拿到 users 这类记录；
    只靠 15 秒兜底扫描的话，掉线早于第一拍的边缘会缺一批记录。"""
    db = open_db_ro(xdg)
    try:
        rows = db.execute("SELECT DISTINCT domain FROM records").fetchall()
    finally:
        db.close()
    return {row[0] for row in rows}


def pending_domains(xdg):
    db = open_db_ro(xdg)
    try:
        rows = db.execute("SELECT DISTINCT domain FROM pending").fetchall()
    finally:
        db.close()
    return {row[0] for row in rows}


def state_mode(xdg):
    """模式必须落库（重启后按它启动），内存与 state 表不许各说各话。"""
    db = open_db_ro(xdg)
    try:
        row = db.execute("SELECT mode FROM state").fetchone()
    finally:
        db.close()
    return row[0] if row else None


# ---------------------------------------------------------------- scenarios


def run(root, binary, keep):
    primary, edge, primary_proc, edge_proc, primary_xdg, edge_xdg = prepare(
        root, binary)

    failed = False
    try:
        # ---------------- 0. 两个实例都起来
        primary_proc.start()
        primary.wait_up()
        edge_proc.start()
        edge.wait_up()
        wait_mode(edge, "online", 30, "(初始)")
        check(edge.prefs_hours() == 72, "初始值应是默认 72")

        # ---------------- 1. 在线转发
        status, body = edge.call("PUT", PREF_PATH, {"hours": 12})
        check(status == 200, f"边缘在线 PUT → {status} {body!r}")
        check(primary.prefs_hours() == 12, "主节点没拿到边缘转发的写入")
        check(prefs_hours_on_disk(primary_xdg) == 12, "主节点文件没落 12")
        time.sleep(6)  # 至少一个副本间隔：副本只进索引库，不写权威存储
        check(prefs_hours_on_disk(edge_xdg) != 12,
              "在线时边缘把控制面写进了本机权威存储")
        # 主节点启动即发布整份基线，首次追平就该拿到 users 域；只靠 15 秒兜底扫描
        # 的话，比第一拍更早掉线的边缘会缺一批记录，恢复时全被误判成冲突。
        deadline = time.time() + 20
        while "users" not in replica_domains(edge_xdg):
            check(time.time() < deadline,
                  f"在线 20s 后副本仍没有 users 记录：{replica_domains(edge_xdg)!r}")
            time.sleep(0.5)
        log("场景 1 在线转发 PASS")

        # ---------------- 2. 断网降级 + 3. 离线写
        primary_proc.stop()
        wait_mode_stable(edge, "offline-local", 75, 5, "(主节点已停)")
        check(state_mode(edge_xdg) == "offline-local",
              f"状态接口报 offline-local，state 表却是 {state_mode(edge_xdg)!r}")
        # 交接发生在进入 offline-local 之后的下一次 tick（≤1s），不是切模式的那一瞬。
        wait_prefs_hours_on_disk(edge_xdg, 12, 15, "(交接)")
        status, body = edge.call("PUT", PREF_PATH, {"hours": 24})
        check(status == 200, f"离线 PUT → {status} {body!r}")
        check(prefs_hours_on_disk(edge_xdg) == 24, "离线写入没落本地")
        log("场景 2 断网降级 PASS；场景 3 离线写 PASS")

        # ---------------- 4. 重启保持
        edge_proc.stop()
        edge_proc.start()
        edge.wait_up()
        wait_mode(edge, "offline-local", 15, "(重启后)")
        check(state_mode(edge_xdg) == "offline-local",
              f"重启后 state 表模式 = {state_mode(edge_xdg)!r}，应仍是 offline-local")
        check(prefs_hours_on_disk(edge_xdg) == 24, "重启后本地值被改掉了")
        # 兜底扫描 15 秒一拍。交接没把「副本不认识的本地记录」（本机自己生成的
        # 默认账户）算进基线的话，它们会在这时候变成待回传，恢复后与主节点撞冲突。
        time.sleep(16)
        check("users" not in pending_domains(edge_xdg),
              f"交接后本机默认账户被当成待回传：{sorted(pending_domains(edge_xdg))!r}")
        log("场景 4 重启保持 PASS")

        # ---------------- 5. 恢复无冲突
        primary_proc.start()
        primary.wait_up()
        wait_mode(edge, "online", 90, "(主节点恢复)")
        check(primary.prefs_hours() == 24, "离线改动没推回主节点")
        check(prefs_hours_on_disk(primary_xdg) == 24, "主节点文件没落 24")
        check(conflict_cards(edge) == [], "无冲突恢复却出了冲突卡")
        log("场景 5 恢复无冲突 PASS")

        # ---------------- 6. 恢复有冲突
        primary_proc.stop()
        wait_mode_stable(edge, "offline-local", 75, 5, "(第二轮断网)")
        check(state_mode(edge_xdg) == "offline-local",
              f"第二轮断网：状态接口报 offline-local，state 表却是 {state_mode(edge_xdg)!r}")
        status, body = edge.call("PUT", PREF_PATH, {"hours": 30})
        check(status == 200, f"离线 PUT（冲突轮）→ {status} {body!r}")
        primary_proc.start()
        primary.wait_up()
        status, body = primary.call("PUT", PREF_PATH, {"hours": 36})
        check(status == 200, f"主节点直写 → {status} {body!r}")
        check(prefs_hours_on_disk(primary_xdg) == 36, "主节点直写没落 36")
        wait_mode(edge, "conflict-review", 90, "(两侧都改)")
        cards = conflict_cards(edge)
        check(len(cards) == 1, f"应有 1 张冲突卡，实际 {cards!r}")
        card = cards[0]
        check(card["domain"] == "preferences", f"冲突域不对：{card!r}")
        check(card["key"] == PREF_KEY, f"冲突键不对：{card!r}")
        check(card["local"] == 30 and card["remote"] == 36,
              f"冲突两侧取值不对：{card!r}")

        status, body = edge.call("PUT", PREF_PATH, {"hours": 40})
        check(status == 409, f"冲突域写入应被冻结，得到 {status} {body!r}")
        check("sync_conflict_pending" in json.dumps(body),
              f"冻结响应里没有 sync_conflict_pending：{body!r}")
        status, body = edge.call("PUT", PIN_PATH,
                                 {"root_id": "", "key": PIN_KEY, "pinned": True})
        check(status == 200, f"其它域应照常可写，得到 {status} {body!r}")

        status, body = edge.call(
            "POST", f"/api/control-sync/conflicts/{card['id']}/resolve",
            {"choice": "local"})
        check(status == 200, f"裁决 → {status} {body!r}")
        wait_mode(edge, "online", 90, "(裁决后)")
        check(primary.prefs_hours() == 30, "裁决 local 后主节点没收敛到本地值")
        check(prefs_hours_on_disk(primary_xdg) == 30, "主节点文件没落 30")
        check(prefs_hours_on_disk(edge_xdg) == 30, "边缘本地值被改掉了")
        check(conflict_cards(edge) == [], "裁决后还有未关闭的冲突卡")
        pins_path = os.path.join(primary_xdg, "mindfs", "session_pins.json")
        with open(pins_path, encoding="utf-8") as fh:
            check(PIN_KEY in fh.read(), "冻结期间写下的置顶没推回主节点")
        log("场景 6 恢复有冲突 PASS")

        log("全部 PASS")
    except Fail as exc:
        failed = True
        print(f"\n[e2e] FAIL: {exc}", file=sys.stderr)
        for inst in (primary_proc, edge_proc):
            print(f"\n--- {inst.name} 日志尾部 ---", file=sys.stderr)
            print(inst.tail(60), file=sys.stderr)
        raise
    finally:
        edge_proc.stop()
        primary_proc.stop()
        if not keep and not failed:
            shutil.rmtree(root, ignore_errors=True)
        else:
            log(f"保留现场：{root}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="",
                        help="mindfs 可执行文件；缺省用 go build 现编一个")
    parser.add_argument("--root", default="/tmp/mindfs-control-sync-e2e",
                        help="两个实例的 XDG_CONFIG_HOME 与证书都建在这里")
    parser.add_argument("--keep", action="store_true", help="结束后保留现场")
    args = parser.parse_args()

    root = os.path.abspath(args.root)
    if os.path.exists(root):
        shutil.rmtree(root)
    os.makedirs(root, exist_ok=True)

    binary = args.binary
    if not binary:
        binary = os.path.join(root, "mindfs")
        log("编译 bin（go build ./cli/cmd）")
        subprocess.run(["go", "build", "-o", binary, "./cli/cmd"], check=True)
    binary = os.path.abspath(binary)

    try:
        run(root, binary, args.keep)
    except Fail:
        sys.exit(1)


if __name__ == "__main__":
    main()