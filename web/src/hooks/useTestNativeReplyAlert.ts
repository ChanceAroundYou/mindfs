import { useCallback, useState } from "react";
import { testNativeReplyAlert } from "../services/net/replyPoller";

// 组件层不许直接 import services（守卫测试 reviewer-architecture-rules），
// 由 hook 这一层代持；仓库里 useReportError / useTaskTemplateSave 同例。
export function useTestNativeReplyAlert(): { message: string; run: () => Promise<void> } {
  const [message, setMessage] = useState("");
  const run = useCallback(async () => {
    const error = await testNativeReplyAlert();
    setMessage(error || "测试提醒已发送");
  }, []);
  return { message, run };
}
