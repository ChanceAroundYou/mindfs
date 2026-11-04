import { useCallback } from "react";
import { testNativeReplyAlert } from "../services/net/replyPoller";

// 组件层不许直接 import services（守卫测试 reviewer-architecture-rules），
// 由 hook 这一层代持；仓库里 useReportError / useTaskTemplateSave 同例。
export function useTestNativeReplyAlert(): () => void {
  return useCallback(() => {
    void testNativeReplyAlert();
  }, []);
}
