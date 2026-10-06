import { appURL } from "./base";
import { getRootNodeId } from "./rootNode";
import { e2eeService } from "./e2ee";

export type UploadedFile = {
  path: string;
  agent_path?: string;
  name: string;
  mime: string;
  size: number;
};

export type UploadProgress = {
  loaded: number;
  total: number;
  percent: number;
  speedBps: number;
};

type UploadResponse = {
  files?: UploadedFile[];
};

export async function uploadFiles(params: {
  rootId: string;
  files: File[];
  dir?: string;
  onProgress?: (progress: UploadProgress) => void;
  signal?: AbortSignal;
  nodeId?: string;
}): Promise<UploadedFile[]> {
  params.nodeId = params.nodeId || getRootNodeId(params.rootId);
  const formData = new FormData();
  params.files.forEach((file) => {
    formData.append("files", file);
  });
  if (params.dir) {
    formData.append("dir", params.dir);
  }

  const query = new URLSearchParams({ root: params.rootId });
  const requestURL = appURL("/api/upload", query, params.nodeId);
  const headers = e2eeService.isRequired()
    ? await e2eeService.fileProofHeaders("POST", requestURL)
    : undefined;
  const response = await uploadFormData(requestURL, formData, headers, params.onProgress, params.signal);
  if (!response.ok) {
    if (response.status === 401 && e2eeService.isRequired()) {
      const payload = (await response.json().catch(() => ({}))) as { error?: string };
      if (e2eeService.handleServerError(String(payload.error || ""))) {
        return uploadFiles(params);
      }
      throw new Error(payload.error || `Upload failed: ${response.status}`);
    }
    let message = `Upload failed: ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string };
      if (payload?.error) {
        message = payload.error;
      }
    } catch {
    }
    throw new Error(message);
  }
  const payload = await e2eeService.parseProtectedJSONResponse<UploadResponse>(response);
  return Array.isArray(payload.files) ? payload.files : [];
}

export function isUploadAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError";
}

function uploadFormData(
  requestURL: string,
  formData: FormData,
  headers?: Headers,
  onProgress?: (progress: UploadProgress) => void,
  signal?: AbortSignal,
): Promise<Response> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(uploadAbortError());
      return;
    }
    const xhr = new XMLHttpRequest();
    const startTime = performance.now();
    let lastLoaded = 0;
    let lastTime = startTime;
    let lastSpeedBps = 0;
    xhr.open("POST", requestURL, true);
    headers?.forEach((value, key) => {
      xhr.setRequestHeader(key, value);
    });
    xhr.upload.onprogress = (event) => {
      if (!event.lengthComputable || event.total <= 0) {
        return;
      }
      const now = performance.now();
      const elapsedSeconds = Math.max((now - lastTime) / 1000, 0.001);
      lastSpeedBps = Math.max(0, (event.loaded - lastLoaded) / elapsedSeconds);
      lastLoaded = event.loaded;
      lastTime = now;
      onProgress?.({
        loaded: event.loaded,
        total: event.total,
        speedBps: lastSpeedBps,
        percent: Math.min(100, Math.max(0, Math.round((event.loaded / event.total) * 100))),
      });
    };
    const cleanup = () => {
      signal?.removeEventListener("abort", handleAbort);
    };
    const handleAbort = () => {
      xhr.abort();
    };
    signal?.addEventListener("abort", handleAbort, { once: true });
    xhr.onerror = () => {
      cleanup();
      reject(new Error("Upload failed: network error"));
    };
    xhr.ontimeout = () => {
      cleanup();
      reject(new Error("Upload failed: timeout"));
    };
    xhr.onabort = () => {
      cleanup();
      reject(uploadAbortError());
    };
    xhr.onload = () => {
      cleanup();
      onProgress?.({ loaded: 1, total: 1, percent: 100, speedBps: lastSpeedBps });
      resolve(new Response(xhr.responseText, {
        status: xhr.status,
        statusText: xhr.statusText,
        headers: parseXHRHeaders(xhr.getAllResponseHeaders()),
      }));
    };
    xhr.send(formData);
  });
}

function parseXHRHeaders(rawHeaders: string): Headers {
  const headers = new Headers();
  rawHeaders.trim().split(/[\r\n]+/).forEach((line) => {
    const index = line.indexOf(":");
    if (index <= 0) {
      return;
    }
    headers.append(line.slice(0, index).trim(), line.slice(index + 1).trim());
  });
  return headers;
}

function uploadAbortError(): Error {
  const error = new Error("Upload aborted");
  error.name = "AbortError";
  return error;
}

/**
 * 消息正文里引用一个上传文件的 **token 组装点**。
 *
 * 这个形状此前在 4 个文件里各写了一遍（`App.tsx` 富文本发送、`TaskDetailPanel` 任务编辑、
 * `ActionBar` 聊天发送、`tokenEditorUtils` 编辑器序列化）—— 改一处必漏三处（冲突⑮a）。
 * 收敛到一处之后，扫描 `` `[file: ${ `` 只应命本函数。
 */
export function formatFileToken(path: string): string {
  return `[file: ${path}]`;
}

/** 取一个上传文件用于 token 的路径：agent 侧路径优先（见 `UploadedFile.agent_path`）。 */
export function fileTokenPath(file: { agent_path?: string; path?: string } | null | undefined): string {
  return String(file?.agent_path || file?.path || "").trim();
}
