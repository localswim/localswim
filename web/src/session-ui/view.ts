import type { SessionRead } from "../../generated/transport/contracts";
import { BoardClient } from "../api/client";
import { button, element } from "../recovery-ui/view";

// Secret exchange and validation live in the shared provider. UI receives only
// current, non-secret scope/actor facts and a memory-only credential lifecycle.
export interface SessionAccess {
  readonly client: BoardClient;
  exchange(code: string): Promise<SessionRead>;
  logout(): Promise<boolean>;
  clear(): void;
  onUnavailable(callback: () => void): () => void;
}
export type SessionContent = Readonly<{ destroy(): void }>;
export class SessionView {
  private readonly input = element("input");
  private readonly state = element("p", "Signed out. Enter a fresh one-use code from the owner CLI to reauthenticate.");
  private readonly content = element("section");
  private readonly login: HTMLButtonElement;
  private readonly logout: HTMLButtonElement;
  private current: SessionContent | undefined;
  private readonly unsubscribe: () => void;
  private generation = 0;
  private busy = false;
  private composing = false;
  private destroyed = false;
  constructor(private readonly root: HTMLElement, readonly access: SessionAccess,
    private readonly mount: (root: HTMLElement, client: BoardClient, session: SessionRead) => SessionContent) {
    const form = element("form"), label = element("label", "One-use session code ");
    this.input.type = "password"; this.input.autocomplete = "off"; this.input.spellcheck = false; this.input.name = "session-code"; label.append(this.input);
    this.login = button("Reauthenticate with code", () => { void this.authenticate(); });
    this.logout = button("Log out and clear visible content", () => { void this.signOut(); });
    form.append(label, this.login); form.addEventListener("submit", event => event.preventDefault());
    form.addEventListener("compositionstart", () => { this.composing = true; this.update(); });
    form.addEventListener("compositionend", () => { this.composing = false; this.update(); });
    form.addEventListener("keydown", event => { if (event.isComposing && event.key === "Enter") event.preventDefault(); });
    this.state.setAttribute("role", "status"); this.content.setAttribute("aria-label", "Authenticated workspace");
    root.replaceChildren(element("h1", "Localswim session"), form, this.logout, this.state, this.content);
    this.unsubscribe = access.onUnavailable(() => { if (!this.destroyed) { this.clearVisible(); this.state.textContent = "Authentication unavailable or revoked. Credentials and visible content cleared; private recovery retained. Reauthenticate deliberately."; } });
    this.update();
  }
  private update(): void { this.login.disabled = this.destroyed || this.busy || this.composing; this.logout.disabled = this.destroyed; }
  private clearVisible(): void { this.generation++; this.current?.destroy(); this.current = undefined; this.content.replaceChildren(); this.input.value = ""; this.busy = false; this.update(); }
  private async authenticate(): Promise<void> {
    if (this.destroyed || this.busy || this.composing) return;
    const code = this.input.value; this.input.value = "";
    // An explicit new login clears the previous view before awaiting exchange.
    this.clearVisible();
    // exchange synchronously invalidates any prior authority and notifies views.
    // Capture the UI epoch after that notification, before awaiting its result.
    const exchange = this.access.exchange(code), generation = this.generation;
    this.busy = true; this.state.textContent = "Exchanging one-use code; no board action is submitted."; this.update();
    try {
      const session = await exchange;
      if (this.destroyed || generation !== this.generation) return;
      this.current = this.mount(this.content, this.access.client, session);
      this.state.textContent = "Authenticated actor " + session.actorId + " (" + session.kind + ") · board " + session.grant.boardId + " · incarnation " + session.grant.incarnation + ". Private originals remain bound to their saved identities.";
    } catch { if (!this.destroyed && generation === this.generation) this.state.textContent = "Reauthentication unavailable. Code cleared; obtain a fresh one-use code deliberately. Private recovery retained."; }
    finally { if (!this.destroyed && generation === this.generation) { this.busy = false; this.update(); } }
  }
  private async signOut(): Promise<void> {
    if (this.destroyed) return;
    // Provider captures the old token and clears it synchronously, then attempts
    // exactly one DELETE. No retry can accidentally revoke a later login.
    const result = this.access.logout(); this.clearVisible(); const generation = this.generation;
    this.state.textContent = "Signed out locally. Visible content cleared; private recovery retained. Server revocation is unverified.";
    try { const revoked = await result; if (!this.destroyed && generation === this.generation) this.state.textContent = revoked ? "Signed out; presented session revoked. Private recovery retained." : "Signed out locally; server revocation unverified. Private recovery retained."; }
    catch { /* Local logout remains complete even if delivery is unavailable. */ }
  }
  destroy(): void { if (this.destroyed) return; this.unsubscribe(); this.clearVisible(); this.destroyed = true; this.access.clear(); this.root.replaceChildren(); }
}
