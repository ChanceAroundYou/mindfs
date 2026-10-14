import { useCallback } from "react";
import { reportError } from "../services/net/error";
import type { AppError, ErrorCode } from "../shared/errorTypes";

type ReportErrorOptions = Partial<Omit<AppError, "code" | "message">>;

export function useReportError() {
  return useCallback((code: ErrorCode, message?: string, options?: ReportErrorOptions) => {
    reportError(code, message, options);
  }, []);
}
