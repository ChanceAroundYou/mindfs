import { appPath } from "../services/net/base";
import { protectedJSON } from "../services/net/api";
import { getNodes, isSelfNode, learnNodeInstanceId } from "../services/net/nodeRegistry";

// 启动时把每台非本机节点的控制面身份学一次（/health 的 instanceId）。
//
// 节点行是用户手工加的（id 是随机 uuid），而 relay 只知道设备的 instance_id；
// 不学身份，同一台机器就会以「直连行 + relay 行」两行并存 —— relay 接通后
// 共享节点表多出一条 `relay:pc` 就是这么来的。学到之后按 instanceId 合并，
// relay 只作为该行的兜底通道。每台机器成功一次就够：失败不重试、不报错，
// 打开节点面板时还会再探一次。
const probed = new Set<string>();

export async function learnNodeIdentitiesOnce(timeoutMs = 4000): Promise<void> {
  const targets = getNodes().filter((n) => !isSelfNode(n.id) && !n.instanceId && !probed.has(n.id));
  await Promise.all(
    targets.map(async (node) => {
      probed.add(node.id);
      let timer = 0;
      try {
        const payload = await Promise.race([
          protectedJSON<{ instanceId?: string }>(appPath("/health", node.id)),
          new Promise<null>((resolve) => {
            timer = window.setTimeout(() => resolve(null), timeoutMs);
          }),
        ]);
        const instanceId = String(payload?.instanceId || "").trim();
        if (instanceId) learnNodeInstanceId(node.id, instanceId);
      } catch {
        // 离线/旧版本/worker 不上报：保持无身份，等下次面板探活再试。
      } finally {
        if (timer) window.clearTimeout(timer);
      }
    }),
  );
}
