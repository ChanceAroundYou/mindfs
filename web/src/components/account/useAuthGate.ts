import { useEffect, useRef, useState } from "react";
import { currentUser, fetchAuthStatus, loginWithPassword } from "../../services/net/authGate";
import type { I18nContextValue } from "../../i18n/index";

export function useAuthGate(t: I18nContextValue["t"]) {
  const [state, setState] = useState<"checking" | "locked" | "open">("checking");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const aliveRef = useRef(true);

  useEffect(() => {
    aliveRef.current = true;
    if (currentUser()) {
      setState("open");
      return () => {
        aliveRef.current = false;
      };
    }
    void fetchAuthStatus().then((status) => {
      if (aliveRef.current) setState(status.required ? "locked" : "open");
    });
    return () => {
      aliveRef.current = false;
    };
  }, []);

  const submit = async () => {
    const name = username.trim();
    if (!name) {
      setError(t("login.usernameRequired"));
      return;
    }
    if (!password) {
      setError(t("login.passwordRequired"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await loginWithPassword(name, password);
      if (aliveRef.current) {
        setPassword("");
        setState("open");
      }
    } catch (err) {
      if (!aliveRef.current) return;
      const code = err instanceof Error ? err.message : "";
      if (code === "invalid_credentials") setError(t("login.invalidCredentials"));
      else if (code === "user_disabled") setError(t("login.userDisabled"));
      else setError(t("login.failed"));
    } finally {
      if (aliveRef.current) setBusy(false);
    }
  };

  return { state, username, setUsername, password, setPassword, error, setError, busy, submit };
}
