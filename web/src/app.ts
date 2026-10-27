import { openLocalSession } from "./api/local-session.js";
import { isLocalOrigin } from "./api/client.js";
import { LocalSessionView } from "./session-ui/local-view.js";
import { openSessionClient } from "./api/w03-session.js";
import type { ProfileQualification } from "./recovery/types.js";
import { SessionView } from "./session-ui/view.js";
import { SessionWorkspace } from "./session-ui/workspace.js";

// Retained explicit HTTPS component mode only: trusted composition supplies
// its profile proof. No URL, storage value or inline script grants
// qualification. An absent proof reaches the shared provider's refusal gate.
export type ApplicationProfile = Readonly<{
  installationId: string;
  profileId: string;
  qualification?: ProfileQualification;
}>;

type Application = Readonly<{ destroy(): void }>;
const mounted = new WeakMap<HTMLElement, Application>();

export async function mountApplication(root: HTMLElement, profile: ApplicationProfile): Promise<Application> {
  mounted.get(root)?.destroy();
  let destroyed = false;
  let access: Awaited<ReturnType<typeof openSessionClient>> | undefined;
  let view: SessionView | undefined;
  const application = Object.freeze({ destroy(): void {
    if (destroyed) return; destroyed = true;
    view?.destroy(); access?.clear(); access?.client.close();
    if (mounted.get(root) === application) mounted.delete(root);
  } });
  mounted.set(root, application);
  try {
    access = await openSessionClient({ installationId: profile.installationId, profileId: profile.profileId,
      ...(profile.qualification ? { qualification: profile.qualification } : {}) });
    if (destroyed) { access.clear(); access.client.close(); return application; }
    view = new SessionView(root, access, (content, client, session) => new SessionWorkspace(content, client, session));
    return application;
  } catch (error) { application.destroy(); throw error; }
}

function unavailable(root: HTMLElement): void {
  const heading = document.createElement("h1"); heading.textContent = "Localswim";
  const status = document.createElement("p"); status.setAttribute("role", "status");
  status.textContent = "Trusted installation configuration is unavailable. Session and save controls remain unavailable until configured.";
  root.replaceChildren(heading, status);
}

async function bootstrap(root: HTMLElement): Promise<void> {
  if (!isLocalOrigin()) unavailable(root);
  else {
    const status = document.createElement("p"); status.setAttribute("role", "status");
    status.textContent = "Connecting to local service and checking private recovery storage…"; root.replaceChildren(status);
  }
  if (isLocalOrigin()) {
    let access: Awaited<ReturnType<typeof openLocalSession>> | undefined;
    try {
      access = await openLocalSession();
      if (mounted.has(root)) { access.clear(); access.client.close(); return; }
      const view = new LocalSessionView(root, access), local = access;
      const application = Object.freeze({ destroy(): void {
        view.destroy(); local.clear(); local.client.close();
        if (mounted.get(root) === application) mounted.delete(root);
      } });
      mounted.set(root, application);
    } catch {
      access?.clear(); access?.client.close();
      if (!mounted.has(root)) {
        root.replaceChildren();
        const heading = document.createElement("h1"), status = document.createElement("p"), retry = document.createElement("button");
        heading.textContent = "Localswim"; status.setAttribute("role", "status");
        status.textContent = "Local service connection unavailable. Start the service and reconnect. No board action was submitted.";
        retry.type = "button"; retry.textContent = "Reconnect local service";
        retry.addEventListener("click", () => { retry.disabled = true; void bootstrap(root); });
        root.append(heading, status, retry);
      }
    }
    return;
  }
  if (location.protocol !== "https:") return;
  try {
    // The active native server exposes only its installation UUID on this
    // same-origin GET. The verified HTML body remains unchanged. This metadata
    // neither qualifies the browser profile nor conveys session authority.
    const response = await fetch("/", { method: "GET", credentials: "omit", cache: "no-store", redirect: "error" });
    const installationId = response.headers.get("X-Localswim-Installation");
    if (response.status !== 200 || response.redirected || response.url !== new URL("/", location.origin).href ||
      !installationId || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(installationId) ||
      installationId === "00000000-0000-0000-0000-000000000000") { await response.body?.cancel(); return; }
    await response.body?.cancel();
    if (mounted.has(root)) return;
    await mountApplication(root, { installationId, profileId: "unqualified" });
  } catch { if (!mounted.has(root)) unavailable(root); }
}

const root = document.getElementById("app");
if (root) void bootstrap(root);
