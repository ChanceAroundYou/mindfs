import { useCallback, useEffect, useState } from "react";
import { errorService } from "../services/net/error";
import type { AppError } from "../shared/errorTypes";

type ToastItem = {
  id: string;
  error: AppError;
  expiresAt: number;
};

export function useToastErrors() {
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  useEffect(() => {
    const unsubscribe = errorService.subscribe((error) => {
      if (error.severity === "fatal") return;
      const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const duration = error.severity === "error" ? 5000 : 3000;
      setToasts((prev) => [...prev, { id, error, expiresAt: Date.now() + duration }]);
    });
    return unsubscribe;
  }, []);

  useEffect(() => {
    if (toasts.length === 0) return undefined;
    const timer = setInterval(() => {
      const now = Date.now();
      setToasts((prev) => prev.filter((toast) => toast.expiresAt > now));
    }, 500);
    return () => clearInterval(timer);
  }, [toasts.length]);

  const removeToast = useCallback((id: string) => {
    setToasts((prev) => prev.filter((toast) => toast.id !== id));
  }, []);

  const handleRetry = useCallback(async (toast: ToastItem) => {
    if (!toast.error.retryAction) return;
    removeToast(toast.id);
    try {
      await toast.error.retryAction();
    } catch (error) {
      console.error("Retry failed:", error);
    }
  }, [removeToast]);

  return { toasts, removeToast, handleRetry };
}
