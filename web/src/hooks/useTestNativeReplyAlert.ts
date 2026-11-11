import { useCallback, useState } from "react";
import { testNativeReplyAlert } from "../services/net/replyPoller";
import { supportsCustomNativePlugins } from "../services/platform/runtime";

// 组件层不许直接 import services（守卫测试 reviewer-architecture-rules），
// 由 hook 这一层代持；仓库里 useReportError 同例。
// supported = false（iOS 壳）时按钮不许出现：那边没有后台轮询服务可测。
export function useTestNativeReplyAlert(): { message: string; run: () => Promise<void>; supported: boolean } {
  const [message, setMessage] = useState("");
  const run = useCallback(async () => {
    const error = await testNativeReplyAlert();
    setMessage(error || "测试提醒已发送（10 秒后还会再发一条，届时请切到后台看是否会弹出横幅）");
  }, []);
  return { message, run, supported: supportsCustomNativePlugins() };
}
