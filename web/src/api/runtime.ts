import type { RuntimeMode } from "../../generated/transport/contracts.js";
import { parse, validate } from "./boundary.js";
import { isLocalOrigin } from "./client.js";

// Nonsecret discriminator; every ordinary local composition reads this before
// choosing a bootstrap route. Invalid/unavailable mode never selects a default.
export async function readRuntimeMode(fetcher: typeof fetch = globalThis.fetch.bind(globalThis)): Promise<RuntimeMode> {
  if (!isLocalOrigin()) throw Error("Local service origin required");
  const origin = location.origin;
  const response = await fetcher("/api/v1/runtime-mode", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error",
    signal: AbortSignal.timeout(20_000), headers: { "X-Localswim-Client": "browser" } });
  return readLocalBoundary<RuntimeMode>(response, "transport:RuntimeMode", origin);
}

// Shared strict decoder; callers derive their own fixed relative endpoint.
// Cookie values stay HttpOnly and never enter private recovery.
export async function readLocalBoundary<T>(response: Response, schema: string, origin: string): Promise<T> {
  if (response.status !== 200 || response.redirected || response.headers.get("Cache-Control") !== "no-store" || location.origin !== origin || !isLocalOrigin()) {
    await response.body?.cancel().catch(() => {}); throw Error("Local connection unavailable");
  }
  const bytes = await response.arrayBuffer();
  if (location.origin !== origin || !isLocalOrigin()) throw Error("Local origin changed");
  const value = parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes));
  validate(schema, value);
  return structuredClone(value) as T;
}
