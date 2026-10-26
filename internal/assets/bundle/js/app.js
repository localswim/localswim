import { openSessionClient } from "./api/w03-session.js";
import { SessionView } from "./session-ui/view.js";
import { SessionWorkspace } from "./session-ui/workspace.js";
const mounted = new WeakMap();
export async function mountApplication(root, profile) {
    mounted.get(root)?.destroy();
    let destroyed = false;
    let access;
    let view;
    const application = Object.freeze({ destroy() {
            if (destroyed)
                return;
            destroyed = true;
            view?.destroy();
            access?.clear();
            access?.client.close();
            if (mounted.get(root) === application)
                mounted.delete(root);
        } });
    mounted.set(root, application);
    try {
        access = await openSessionClient({ installationId: profile.installationId, profileId: profile.profileId,
            ...(profile.qualification ? { qualification: profile.qualification } : {}) });
        if (destroyed) {
            access.clear();
            access.client.close();
            return application;
        }
        view = new SessionView(root, access, (content, client, session) => new SessionWorkspace(content, client, session));
        return application;
    }
    catch (error) {
        application.destroy();
        throw error;
    }
}
function unavailable(root) {
    const heading = document.createElement("h1");
    heading.textContent = "Localswim";
    const status = document.createElement("p");
    status.setAttribute("role", "status");
    status.textContent = "Trusted installation configuration is unavailable. Session and save controls remain unavailable until configured.";
    root.replaceChildren(heading, status);
}
async function bootstrap(root) {
    unavailable(root);
    if (location.protocol !== "https:")
        return;
    try {
        // The active native server exposes only its installation UUID on this
        // same-origin GET. The verified HTML body remains unchanged. This metadata
        // neither qualifies the browser profile nor conveys session authority.
        const response = await fetch("/", { method: "GET", credentials: "omit", cache: "no-store", redirect: "error" });
        const installationId = response.headers.get("X-Localswim-Installation");
        if (response.status !== 200 || response.redirected || response.url !== new URL("/", location.origin).href ||
            !installationId || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(installationId) ||
            installationId === "00000000-0000-0000-0000-000000000000") {
            await response.body?.cancel();
            return;
        }
        await response.body?.cancel();
        if (mounted.has(root))
            return;
        await mountApplication(root, { installationId, profileId: "unqualified" });
    }
    catch {
        if (!mounted.has(root))
            unavailable(root);
    }
}
const root = document.getElementById("app");
if (root)
    void bootstrap(root);
