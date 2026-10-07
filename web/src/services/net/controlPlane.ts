// 控制面（账户/偏好/提示词/看板模板/节点表/WebPush 订阅）**只有主节点一份真相**。
//
// 这些请求必须打**页面服务器**，绝不能跟随当前选中节点 —— 否则选中 PC 时改偏好会写进
// PC，那台机器从此有一份分叉的配置。实测这正是今天的状态：两个节点的 nodes.json
// 互相矛盾（同一台机器在两边拿到不同 id），task_template.json 两边各有一半模板。
//
// 复用 pageServerPath（base.ts）而不是另写一套：它已经被账户管理六个调用点验证过，
// 且 multi-account-partition.test.mjs 钉住了「账户管理走页面服务器」这条契约。
//
// 单节点部署下 controlPath 与 appPath 解析到**同一地址**（同源）——所以这次改绑
// 对单机是零行为变化，只在多节点场景下才与「跟随节点」分道扬镳。
import { appendQuery, pageServerPath } from "./base";

/**
 * 打页面服务器的控制面路径。
 *
 * 不接受 nodeId —— 给了也没用：控制面不属于任何节点。调用方如果发现自己想传 nodeId，
 * 那说明它要的多半是数据面请求，应该用 appPath/appURL。
 */
export function controlPath(path: string, params?: URLSearchParams): string {
  return appendQuery(pageServerPath(path), params);
}
