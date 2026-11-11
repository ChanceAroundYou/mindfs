import assert from "node:assert";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

// pc-win 辅助脚本契约（G-AL）：
// - codex-switch 只通过本地 API 切换，复用服务端的校验/回滚/进程重启；
// - 脚本必须是纯 ASCII（Windows PowerShell 按 ANSI 代码页读 .ps1，非 ASCII 会吃掉换行）；
// - deploy-pcwin.sh 负责把脚本装进 bin 目录并幂等注册用户 PATH；
// - Windows 二进制是 windowsgui 子系统，不能用 `mindfs ...` 的 stdout 做判断。

const root = path.resolve(fileURLToPath(import.meta.url), "../../..");
const read = (p) => fs.readFileSync(path.join(root, p), "utf8");
const readBytes = (p) => fs.readFileSync(path.join(root, p));

test("pcwin helper scripts are ASCII only", () => {
  for (const file of [
    "scripts/pcwin/codex-switch.ps1",
    "scripts/pcwin/codex-switch.cmd",
    "scripts/pcwin/register-user-path.ps1",
  ]) {
    const bytes = readBytes(file);
    const bad = [...bytes].findIndex((b) => b > 0x7f);
    assert.equal(bad, -1, `${file} must stay pure ASCII (first non-ASCII byte at ${bad})`);
  }
});

test("codex-switch switches through the local API", () => {
  const ps1 = read("scripts/pcwin/codex-switch.ps1");
  assert.ok(ps1.includes("/api/agent-config/switch"), "must POST to the switch endpoint");
  assert.ok(ps1.includes('"shared:codex-default"'), "default target id");
  assert.ok(ps1.includes('"shared:codex-llmux"'), "llmux target id");
  assert.ok(ps1.includes('Invoke-RestMethod -Method Post'), "switch must be an HTTP POST");
  assert.ok(ps1.includes('"status"'), "status action");
  // 状态展示只读文件/API；不要把 GUI 子系统的 stdout 当作可捕获输出。
  assert.ok(!ps1.includes("agents-config-sync --check-runtime"), "must not rely on GUI-subsystem CLI stdout");
  // 凭据只判断存在性，绝不读取或打印值。
  assert.ok(
    ps1.includes('[Environment]::GetEnvironmentVariable("LLMUX_API_KEY", "User")'),
    "checks LLMUX_API_KEY presence only",
  );
  assert.ok(!ps1.includes("LLMUX_API_KEY)"), "must not echo the key value");
});

test("codex-switch.cmd calls the PowerShell script next to it", () => {
  const cmd = read("scripts/pcwin/codex-switch.cmd");
  assert.ok(cmd.includes("%~dp0codex-switch.ps1"), "cmd shim must resolve the script beside itself");
});

test("deploy-pcwin installs the helpers and registers the user PATH", () => {
  const deploy = read("scripts/deploy-pcwin.sh");
  assert.ok(deploy.includes("scripts/pcwin/codex-switch.ps1"), "deploy must push codex-switch.ps1");
  assert.ok(deploy.includes("scripts/pcwin/codex-switch.cmd"), "deploy must push codex-switch.cmd");
  assert.ok(deploy.includes("scripts/pcwin/register-user-path.ps1"), "deploy must push the PATH helper");
  assert.ok(
    deploy.includes("-ExecutionPolicy Bypass -File C:\\Users\\10178\\register-user-path.ps1"),
    "PATH registration must run with -File (stdin -Command - is silently ignored on Windows PowerShell 5.1)",
  );
  assert.ok(
    !deploy.includes("'powershell -NoProfile -Command -' <"),
    "do not pipe the PATH script through `-Command -`",
  );
});

test("register-user-path appends to the user PATH idempotently", () => {
  const ps1 = read("scripts/pcwin/register-user-path.ps1");
  assert.ok(ps1.includes('GetEnvironmentVariable("Path", "User")'), "reads the user PATH");
  assert.ok(ps1.includes('SetEnvironmentVariable("Path", $next, "User")'), "writes the user PATH");
  assert.ok(ps1.includes("already present"), "must be idempotent");
  assert.ok(ps1.includes("Programs\\mindfs\\bin"), "targets the mindfs bin directory");
});