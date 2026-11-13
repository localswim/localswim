import type { createBrowserApi } from "../api.mjs";

const boardID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export type AppContext = Readonly<{
  api: ReturnType<typeof createBrowserApi>;
  signal: AbortSignal;
}>;

export type Route =
  | { kind: "workspace" }
  | { kind: "board"; id: string }
  | { kind: "missing" };


export function parseRoute(pathname: string): Route {
  if (pathname === "/") return { kind: "workspace" };
  const id = /^\/boards\/([^/]+)$/.exec(pathname)?.[1];
  return id && boardID.test(id) ? { kind: "board", id: id.toLowerCase() } : { kind: "missing" };
}

