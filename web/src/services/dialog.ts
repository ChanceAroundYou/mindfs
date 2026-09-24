/**
 * 应用内确认/输入弹窗服务：替代 window.confirm / window.prompt / window.alert。
 *
 * 走服务而不是组件 state，是为了不改所有调用点的控制流——`await confirmDialog(...)`
 * 和 `window.confirm(...)` 在调用点读起来一样，但渲染的是 MindFS 自己的弹窗。
 * Alert 刻意不 await：调用点原来是 fire-and-forget（不中断后续逻辑）。
 */

type ConfirmRequest = {
  kind: "confirm";
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  resolve: (ok: boolean) => void;
};

type PromptRequest = {
  kind: "prompt";
  message: string;
  defaultValue?: string;
  placeholder?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  resolve: (value: string | null) => void;
};

type AlertRequest = {
  kind: "alert";
  message: string;
};

export type DialogRequest = ConfirmRequest | PromptRequest | AlertRequest;

type Listener = (request: DialogRequest | null) => void;

const listeners = new Set<Listener>();
let current: DialogRequest | null = null;

function notify(): void {
  // 宿主只认这一个入口：开弹窗和关弹窗都必须推一次，否则视图会停在旧请求上不消失。
  for (const listener of listeners) {
    listener(current);
  }
}

function emit(request: DialogRequest): void {
  // 同一时刻只允许一个弹窗。覆盖前先把上一个结算掉（当作取消），
  // 否则它的调用点会永远 await 下去。
  if (current) {
    settle(false);
  }
  current = request;
  notify();
}

/** 结束当前弹窗：先 resolve 调用点，再把 null 推给宿主让它消失。顺序不能反。 */
function settle(value: boolean | string | null): void {
  const request = current;
  if (!request) {
    return;
  }
  current = null;
  if (request.kind === "confirm") {
    request.resolve(value === true);
  } else if (request.kind === "prompt") {
    request.resolve(value === null ? null : String(value));
  }
  notify();
}

export const dialogService = {
  subscribe(listener: Listener): () => void {
    listeners.add(listener);
    listener(current);
    return () => {
      listeners.delete(listener);
    };
  },
  dismiss(): void {
    // 取消的语义按弹窗类型走：confirm→false，prompt→null（别把 false 塞进 prompt，
    // 否则调用点会收到字符串 "false" 并当成用户填的内容）。
    settle(current?.kind === "prompt" ? null : false);
  },
  resolve(value: boolean | string | null): void {
    settle(value);
  },
  get current(): DialogRequest | null {
    return current;
  },
};

/** 确认弹窗；取消（Esc / 点外 / 取消按钮）resolve(false)。 */
export function confirmDialog(options: {
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}): Promise<boolean> {
  return new Promise((resolve) => {
    emit({
      kind: "confirm",
      message: options.message,
      confirmLabel: options.confirmLabel,
      cancelLabel: options.cancelLabel,
      danger: options.danger,
      resolve,
    });
  });
}

/** 输入弹窗；取消 resolve(null)（与 window.prompt 一致）。 */
export function promptDialog(options: {
  message: string;
  defaultValue?: string;
  placeholder?: string;
  confirmLabel?: string;
  cancelLabel?: string;
}): Promise<string | null> {
  return new Promise((resolve) => {
    emit({
      kind: "prompt",
      message: options.message,
      defaultValue: options.defaultValue,
      placeholder: options.placeholder,
      confirmLabel: options.confirmLabel,
      cancelLabel: options.cancelLabel,
      resolve,
    });
  });
}

/** 提示弹窗；不阻塞调用点后续逻辑。 */
export function alertDialog(message: string): void {
  emit({ kind: "alert", message });
}
