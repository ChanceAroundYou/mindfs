import { useCallback, useState } from "react";
import { testNativeReplyAlert } from "../services/net/replyPoller";

// 组件层不许直接 import services（守卫测试 reviewer-architecture-rules），
// 由 hook 这一层代持；仓库里 useReportError / useTaskTemplateSave 同例。
export function useTestNativeReplyAlert(): { message: string; run: () => Promise<void> } {
  const [message, setMessage] = useState("");
  const run = useCallback(async () => {
    const error = await testNativeReplyAlert();
    setMessage(error || "测试提醒已发送（10 秒后还会再发一条，届时请切到后台看是否会弹出横幅）");
  }, []);
  return { message, run };
}
