import type { ExchangeSecret, SessionRead } from "../../generated/transport/contracts.js";
import { canonical, parse, validate } from "./boundary.js";
import { BoardClient } from "./client.js";
import type { ClientOptions } from "./client.js";

export type SessionAccess = Readonly<{
  client: BoardClient;
  exchange(code: string): Promise<SessionRead>;
  logout(): Promise<boolean>;
  clear(): void;
  onUnavailable(callback: () => void): () => void;
}>;
// The only bearer holder is this ephemeral closure. No secret is returned to UI.
export async function openSessionClient(options: Omit<ClientOptions, "credential" | "authenticationUnavailable">): Promise<SessionAccess> {
  let credential: string | undefined, epoch = 0;
  const callbacks = new Set<() => void>();
  const fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
  const clear = (): void => {
    credential = undefined; epoch++;
    for (const callback of callbacks) { try { callback(); } catch { /* All views still lose authority. */ } }
  };
  const client = await BoardClient.open({ ...options, credential: () => credential,
    authenticationUnavailable: presented => { if (credential === presented) clear(); } });
  return Object.freeze({ client, clear,
    onUnavailable(callback: () => void): () => void { callbacks.add(callback); return () => { callbacks.delete(callback); }; },
    async exchange(code: string): Promise<SessionRead> {
      // Clear visible/current authority before any potentially asynchronous exchange.
      clear(); const started = epoch;
      validate("transport:ExchangeRequest", parse(JSON.stringify({ code })));
      if (location.protocol !== "https:") throw Error("HTTPS required");
      try {
        const response = await fetcher("/api/v1/session/exchange", { method: "POST", body: JSON.stringify({ code }),
          credentials: "omit", cache: "no-store", redirect: "error", headers: { "Content-Type": "application/json" } });
        code = "";
        if (response.status !== 200 || response.headers.get("Cache-Control") !== "no-store") throw Error("Session exchange unavailable");
        const value = parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(await response.arrayBuffer()));
        validate("transport:ExchangeSecret", value);
        const secret = value as unknown as ExchangeSecret;
        const issued = Date.parse(secret.issuedAt);
        if (Date.parse(secret.idleExpiresAt) !== issued + 1800000 || Date.parse(secret.expiresAt) !== issued + 28800000 || issued > Date.now() || Date.now() >= Date.parse(secret.idleExpiresAt) || Date.now() >= Date.parse(secret.expiresAt) || epoch !== started) throw Error("Session exchange unavailable");
        credential = secret.token;
        // Independently derive actor and complete grant identity from the real bearer.
        const current = await client.session();
        if (epoch !== started || canonical(current as unknown as Parameters<typeof canonical>[0]) !== canonical(secret.session as unknown as Parameters<typeof canonical>[0])) throw Error("Session identity mismatch");
        return current;
      } catch {
        if (epoch === started) clear();
        throw Error("Session exchange unavailable; deliberately request a new code.");
      } finally { code = ""; }
    },
    async logout(): Promise<boolean> {
      // Capture and clear synchronously, before the DELETE or any caller await.
      const presented = credential; clear();
      if (!presented || location.protocol !== "https:") return false;
      try {
        const response = await fetcher("/api/v1/session", { method: "DELETE", credentials: "omit", cache: "no-store", redirect: "error",
          headers: { "Authorization": "Bearer " + presented } });
        if (response.status !== 200) return false;
        validate("transport:SessionRevoked", parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(await response.arrayBuffer())));
        return true;
      } catch { return false; }
    },
  });
}
