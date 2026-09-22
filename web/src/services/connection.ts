export type ConnectionOptions = {
  baseUrl: string;
};

export function connectToServer({ baseUrl }: ConnectionOptions): WebSocket {
  const url = new URL(baseUrl);
  if (url.protocol === "http:") {
    url.protocol = "ws:";
  } else if (url.protocol === "https:") {
    url.protocol = "wss:";
  }
  if (!url.pathname || url.pathname === "/") {
    url.pathname = "/ws";
  }
  return new WebSocket(url.toString());
}
