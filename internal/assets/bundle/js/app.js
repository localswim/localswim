import { openLocalSession } from "./api/local-session.js";
import { isLocalOrigin } from "./api/client.js";
import { LocalSessionView } from "./session-ui/local-view.js";
import { openSessionClient } from "./api/w03-session.js";
import { SessionView } from "./session-ui/view.js";
import { SessionWorkspace } from "./session-ui/workspace.js";
import { readRuntimeMode } from "./api/runtime.js";
import { openWorkspaceSession } from "./api/workspace-session.js";
import { WindowBoardController } from "./session-ui/window-controller.js";
import { createWorkspaceCatalogClient } from "./api/catalog.js";
import { WorkspaceCatalogRefresh } from "./workspace-ui/refresh.js";
import { WorkspaceShell } from "./workspace-ui/view.js";
import { WindowState } from "./session-ui/window-state.js";
import { createWorkspacePreferencesClient } from "./api/preferences.js";
import { WorkspacePins } from "./workspace-ui/pins.js";
const mounted = new WeakMap();
const connections = new WeakMap();
const connectionClock = { schedule: (run, milliseconds) => setTimeout(run, milliseconds), cancel: handle => clearTimeout(handle) };
// One acquisition deadline spans every sequential phase. The fetch wrapper stays
// usable after success: clearing the timer does not abort the connected client.
export function initialConnection(acquire, cleanup, fetcher = globalThis.fetch.bind(globalThis), clock = connectionClock) {
    const abort = new AbortController();
    let active = true;
    const guarded = (input, init) => fetcher(input, { ...init,
        signal: init?.signal ? AbortSignal.any([abort.signal, init.signal]) : abort.signal });
    return new Promise((resolve, reject) => {
        const timer = clock.schedule(() => {
            if (!active)
                return;
            active = false;
            abort.abort();
            cleanup();
            reject(Error("Initial connection timed out"));
        }, 20_000);
        void acquire(guarded, () => active).then(value => {
            if (!active) {
                cleanup();
                return;
            }
            active = false;
            clock.cancel(timer);
            resolve(value);
        }, error => {
            if (!active) {
                cleanup();
                return;
            }
            active = false;
            clock.cancel(timer);
            abort.abort();
            cleanup();
            reject(error);
        });
    });
}
export async function connectLocalApplication(root, fetcher = globalThis.fetch.bind(globalThis), clock = connectionClock, expectedWorkspaceId) {
    if (mounted.has(root))
        throw Error("Application already mounted; use its navigation controller");
    const generation = (connections.get(root) ?? 0) + 1;
    connections.set(root, generation);
    const stage = document.createElement("section");
    let local;
    let workspace;
    let controller, view;
    let shell, refresh;
    let pins;
    let stopRoute, stopCommit;
    let closed = false;
    const cleanup = () => {
        stopRoute?.();
        stopCommit?.();
        refresh?.dispose();
        pins?.dispose();
        controller?.destroy();
        shell?.destroy();
        view?.destroy();
        local?.clear();
        local?.client.close();
        workspace?.clear();
    };
    try {
        const application = await initialConnection(async (guarded, live) => {
            const current = () => { if (!live() || connections.get(root) !== generation || mounted.has(root))
                throw Error("Initial connection superseded"); };
            const runtime = await readRuntimeMode(guarded);
            current();
            if (expectedWorkspaceId && (runtime.mode !== "workspace" || runtime.workspaceId !== expectedWorkspaceId))
                throw Error("Workspace identity changed");
            if (runtime.mode === "workspace") {
                workspace = await openWorkspaceSession(runtime.workspaceId, guarded);
                current();
                const state = new WindowState();
                let retrying = false;
                const retry = async () => {
                    if (retrying || closed || !controller || !refresh)
                        return;
                    retrying = true;
                    try {
                        const selectedUnavailable = controller.status.phase === "unavailable", requestedPath = controller.status.requestedPath;
                        if (selectedUnavailable || refresh.snapshot.phase === "unavailable") {
                            await refresh.reconnect();
                            if (!closed && selectedUnavailable && controller.status.phase === "unavailable" && controller.status.requestedPath === requestedPath)
                                await controller.retryRequested(false);
                        }
                        else
                            refresh.trigger();
                    }
                    catch { /* Both refresh and selection publish their own unavailable state. */ }
                    finally {
                        retrying = false;
                    }
                };
                shell = new WorkspaceShell(stage, { navigate: path => controller?.navigate(path) ?? Promise.resolve(false), retry: () => { void retry(); },
                    pinChanged: order => { void pins?.change(order); }, retryPins: () => { void pins?.refresh(true); }, inspectPins: () => { void pins?.refresh(true); },
                    layout: state.sidebarLayout, layoutChanged: layout => state.setSidebarLayout(layout), filter: state.sidebarFilter, filterChanged: filter => state.setSidebarFilter(filter) });
                pins = new WorkspacePins(createWorkspacePreferencesClient(workspace, guarded), snapshot => shell?.setPins(snapshot));
                controller = new WindowBoardController(shell.editorRoot, workspace, state);
                stopRoute = controller.subscribe(status => shell?.setRouteStatus(status));
                refresh = new WorkspaceCatalogRefresh(createWorkspaceCatalogClient(workspace, guarded), { onChange: snapshot => shell?.setCatalog(snapshot), refreshPreferences: () => pins?.refresh() ?? Promise.resolve() });
                stopCommit = controller.onCommitted(() => refresh?.trigger());
                await controller.navigate(location.pathname, false);
                current();
            }
            else {
                local = await openLocalSession(guarded);
                current();
                const state = new WindowState(), key = "standalone:" + local.initial.grant.boardId + ":" + local.initial.grant.incarnation;
                view = new LocalSessionView(stage, local, { editor: state.get(key).editor, recovery: state.get(key).recovery,
                    checkpoint: editor => state.put(key, { ...state.get(key), editor }), recoveryCheckpoint: recovery => state.put(key, { ...state.get(key), recovery }) });
                await view.ready;
                await view.flushPrivateInput();
                current();
            }
            const result = Object.freeze({ ...(controller ? { controller } : {}), get canLeave() { return controller?.canLeave ?? view?.canLeave ?? true; }, destroy() {
                    if (closed)
                        return;
                    closed = true;
                    cleanup();
                    if (mounted.get(root) === result) {
                        mounted.delete(root);
                        root.replaceChildren();
                    }
                } });
            return result;
        }, cleanup, fetcher, clock);
        if (connections.get(root) !== generation || mounted.has(root)) {
            application.destroy();
            throw Error("Initial connection superseded");
        }
        root.replaceChildren(stage);
        mounted.set(root, application);
        if (controller) {
            controller.activate();
            refresh?.start();
        }
        else
            view?.activate();
        return application;
    }
    catch (error) {
        cleanup();
        throw error;
    }
}
export function workspaceController(root) { return mounted.get(root)?.controller; }
export async function mountWorkspaceApplication(root, workspaceId) {
    return connectLocalApplication(root, undefined, undefined, workspaceId);
}
export async function mountApplication(root, profile) {
    if (mounted.get(root)?.canLeave === false)
        throw Error("Private input must be saved or explicitly discarded before replacing the application");
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
    if (!isLocalOrigin())
        unavailable(root);
    else {
        const status = document.createElement("p");
        status.setAttribute("role", "status");
        status.textContent = "Connecting to local service and checking private recovery storage…";
        root.replaceChildren(status);
    }
    if (isLocalOrigin()) {
        const previous = connections.get(root) ?? 0;
        try {
            await connectLocalApplication(root);
        }
        catch {
            if (!mounted.has(root) && connections.get(root) === previous + 1) {
                root.replaceChildren();
                const heading = document.createElement("h1"), status = document.createElement("p"), retry = document.createElement("button");
                heading.textContent = "Localswim";
                status.setAttribute("role", "status");
                status.textContent = "Local service connection unavailable. Start the service and reconnect. No board action was submitted.";
                retry.type = "button";
                retry.textContent = "Reconnect local service";
                retry.addEventListener("click", () => { retry.disabled = true; void bootstrap(root); });
                root.append(heading, status, retry);
            }
        }
        return;
    }
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
