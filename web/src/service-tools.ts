import { ApiError } from "../api.mjs";
import type { createBrowserApi } from "../api.mjs";
import { createScopedOperations, submitOperation } from "./operations.js";
import type { createWorkspaceState } from "./workspace-state.js";

type Api = Pick<ReturnType<typeof createBrowserApi>, "getService" | "listBuilds" | "stopService">;
type ServiceReply = Awaited<ReturnType<Api["getService"]>>;
type Service = ServiceReply["data"];
type StopIntent = Readonly<{ instanceId: string; etag: string; acknowledged: boolean }>;
type StopSettlement =
  | { kind: "confirmed"; service: Service }
  | { kind: "refused"; message: string; refresh: boolean }
  | { kind: "unknown" | "journal-failed" };
type Lifecycle = {
  operations: ReturnType<typeof createScopedOperations>;
  settlement?: StopSettlement;
  observers: Set<() => void>;
};
type Options = Readonly<{
  api: Api;
  readWithSession: ReturnType<typeof createWorkspaceState>["readWithSession"];
  signal: AbortSignal;
  canManage: () => boolean;
  onRefreshIdentity?: () => Promise<void> | void;
}>;

const stopKey = "localswim.service-stop.v1";
const lifecycles = new WeakMap<Document, Lifecycle>();
const instanceIdPattern = /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/;
const serviceTagPattern = /^"[A-Za-z0-9_-]{16,128}"$/;

function lifecycleFor(doc: Document): Lifecycle {
  const existing = lifecycles.get(doc);
  if (existing) return existing;
  const lifecycle = { operations: createScopedOperations(), observers: new Set<() => void>() };
  lifecycles.set(doc, lifecycle);
  return lifecycle;
}

function readStopIntent(storage: Storage): StopIntent | null {
  const raw = storage.getItem(stopKey);
  if (raw === null) return null;
  const value: unknown = JSON.parse(raw);
  if (
    typeof value !== "object" ||
    value === null ||
    !("instanceId" in value) ||
    typeof value.instanceId !== "string" ||
    !instanceIdPattern.test(value.instanceId) ||
    !("etag" in value) ||
    typeof value.etag !== "string" ||
    !serviceTagPattern.test(value.etag) ||
    !("acknowledged" in value) ||
    typeof value.acknowledged !== "boolean"
  ) {
    throw new Error("Invalid service shutdown record.");
  }
  return { instanceId: value.instanceId, etag: value.etag, acknowledged: value.acknowledged };
}

function serviceLabel(service: Service) {
  const error = service.restart_error_code;
  return `Service is ${service.state} on port ${service.port}. Running build ${service.build_id}. Service instance ${service.service_instance_id}.${error ? ` Last lifecycle error: ${error}.` : ""}`;
}

export function mountServiceTools(
  host: HTMLElement,
  { api, readWithSession, signal, canManage, onRefreshIdentity }: Options,
) {
  const doc = host.ownerDocument;
  const lifecycle = lifecycleFor(doc);
  const section = doc.createElement("section");
  const heading = doc.createElement("h2");
  const health = doc.createElement("p");
  const builds = doc.createElement("ul");
  const buildStatus = doc.createElement("p");
  const outcome = doc.createElement("p");
  const refresh = doc.createElement("button");
  const stop = doc.createElement("button");
  heading.textContent = "Service";
  health.textContent = "Reading service status…";
  health.setAttribute("role", "status");
  outcome.id = "service-operation-status";
  outcome.setAttribute("role", "status");
  refresh.type = "button";
  refresh.textContent = "Refresh service status";
  stop.type = "button";
  stop.textContent = "Shut down service";
  section.id = "service-controls";
  section.append(heading, health, buildStatus, builds, refresh, stop, outcome);
  host.append(section);

  let storage: Storage | undefined;
  let pending: StopIntent | null = null;
  let storageReady = false;
  let service: ServiceReply | undefined;
  let inspecting = false;
  let refreshAfterInspection = false;
  let managed = false;
  let buildRead: AbortController | undefined;

  function loadIntent() {
    try {
      storage = doc.defaultView?.sessionStorage;
      if (!storage) return false;
      pending = readStopIntent(storage);
      return true;
    } catch {
      return false;
    }
  }

  function saveIntent(next: StopIntent | null) {
    if (!storage) return false;
    try {
      if (next) storage.setItem(stopKey, JSON.stringify(next));
      else storage.removeItem(stopKey);
      pending = next;
      return true;
    } catch {
      return false;
    }
  }

  function sync() {
    if (signal.aborted) return;
    const submitting = lifecycle.operations.state(stopKey) === "submitting";
    stop.hidden = !managed;
    outcome.hidden = !managed;
    buildStatus.hidden = !managed;
    builds.hidden = !managed || builds.childElementCount === 0;
    refresh.disabled = inspecting;
    stop.disabled =
      inspecting ||
      submitting ||
      lifecycle.settlement?.kind === "unknown" ||
      pending !== null ||
      !storageReady ||
      service?.data.state !== "running";
  }

  async function readBuilds() {
    buildRead?.abort();
    builds.replaceChildren();
    buildStatus.textContent = "";
    if (!managed || signal.aborted) return;
    const controller = new AbortController();
    buildRead = controller;
    const readSignal = AbortSignal.any([signal, controller.signal]);
    try {
      const reply = await readWithSession(api.listBuilds, readSignal);
      if (readSignal.aborted || !canManage()) return;
      for (const build of reply.data.builds) {
        const item = doc.createElement("li");
        item.textContent = `Staged build ${build.version} is ${build.validation}${build.error_code ? ` (${build.error_code})` : ""}.`;
        builds.append(item);
      }
      if (!reply.data.builds.length) buildStatus.textContent = "No staged builds.";
    } catch {
      if (!readSignal.aborted && canManage())
        buildStatus.textContent = "Staged build status is unavailable.";
    } finally {
      sync();
    }
  }

  function reportPending() {
    if (pending?.acknowledged) {
      outcome.textContent = "Shutdown accepted. Refresh service status to check progress.";
    } else if (pending) {
      outcome.textContent = "Shutdown outcome is uncertain. Refresh service status to inspect it.";
    } else if (!storageReady) {
      outcome.textContent =
        "Shutdown is unavailable because this tab could not use its shutdown record.";
    }
  }

  function reconcile(latest: ServiceReply | undefined) {
    if (!pending) return;
    if (!latest || latest.data.state === "draining") {
      const accepted = pending.acknowledged || lifecycle.settlement?.kind === "confirmed";
      outcome.textContent = accepted
        ? `Shutdown accepted. ${latest ? "The service is draining." : "The service is unreachable."}`
        : latest
          ? "The service is draining. The original shutdown outcome is uncertain."
          : "The service is unreachable. The shutdown outcome is uncertain.";
      return;
    }
    const { data, etag } = latest;
    const differentInstance = data.service_instance_id !== pending.instanceId;
    const lifecycleError = etag !== pending.etag && data.restart_error_code;
    outcome.textContent = differentInstance
      ? "The service is running on a new instance."
      : lifecycleError
        ? "The original service is running and reports a lifecycle error."
        : "The original service is running. Shutdown could not be confirmed.";
    if (!saveIntent(null)) {
      storageReady = false;
      reportPending();
    } else {
      lifecycle.settlement = undefined;
    }
  }

  async function inspect(explicit: boolean) {
    if (inspecting || signal.aborted) return;
    if (explicit && lifecycle.operations.state(stopKey) === "submitting") {
      outcome.textContent =
        "A shutdown request is still in progress. Refresh service status after it settles.";
      return;
    }
    inspecting = true;
    const previousSettlement = lifecycle.settlement;
    sync();
    try {
      const latest = await readWithSession(() => api.getService(signal), signal);
      if (!signal.aborted && lifecycle.settlement === previousSettlement) {
        service = latest;
        health.textContent = serviceLabel(latest.data);
      }
    } catch {
      if (!signal.aborted && lifecycle.settlement === previousSettlement) {
        service = undefined;
        health.textContent = "Service status is unavailable.";
      }
    } finally {
      inspecting = false;
      if (refreshAfterInspection) {
        refreshAfterInspection = false;
        void inspect(false);
      } else if (!signal.aborted && explicit) {
        if (canManage()) {
          storageReady = loadIntent();
          if (storageReady && pending) reconcile(service);
          else reportPending();
        }
        void readBuilds();
      }
      sync();
    }
  }

  function showSettlement(refresh = true) {
    if (signal.aborted) return;
    storageReady = loadIntent();
    const settlement = lifecycle.settlement;
    switch (settlement?.kind) {
      case "confirmed":
        service = undefined;
        health.textContent = serviceLabel(settlement.service);
        outcome.textContent = "Shutdown accepted. The service is draining.";
        break;
      case "refused":
        service = undefined;
        outcome.textContent = settlement.message;
        if (refresh && settlement.refresh) {
          if (inspecting) refreshAfterInspection = true;
          else void inspect(false);
        }
        break;
      case "unknown":
        service = undefined;
        health.textContent = "Refresh service status to check the current instance.";
        reportPending();
        break;
      case "journal-failed":
        storageReady = false;
        reportPending();
        break;
      case undefined:
        reportPending();
        break;
    }
    sync();
  }

  async function requestStop() {
    if (
      signal.aborted ||
      !canManage() ||
      inspecting ||
      lifecycle.settlement?.kind === "unknown" ||
      service?.data.state !== "running"
    )
      return;
    const prior = service;
    await lifecycle.operations.run(
      stopKey,
      "submitting",
      async () => {
        storageReady = loadIntent();
        if (!storageReady || pending) {
          reportPending();
          return;
        }
        const intent: StopIntent = {
          instanceId: prior.data.service_instance_id,
          etag: prior.etag,
          acknowledged: false,
        };
        outcome.textContent = "Requesting graceful shutdown…";
        lifecycle.settlement = undefined;
        let flushRefused = false;
        const result = await submitOperation(
          () => saveIntent(intent),
          async () => {
            sync();
            try {
              return await api.stopService(prior.data, intent.etag);
            } catch (error) {
              flushRefused =
                error instanceof ApiError &&
                error.code === "final_publication_failed" &&
                !error.outcomeUnknown;
              throw error;
            }
          },
        );
        switch (result.kind) {
          case "confirmed":
            saveIntent({ ...intent, acknowledged: true });
            lifecycle.settlement = { kind: "confirmed", service: result.result };
            break;
          case "unknown":
            lifecycle.settlement = { kind: "unknown" };
            break;
          case "stale":
          case "refused":
            saveIntent(null);
            lifecycle.settlement = {
              kind: "refused",
              refresh: flushRefused,
              message: flushRefused
                ? "Final publication failed. The service remains running."
                : result.kind === "stale"
                  ? "Service status changed. Refresh it before requesting shutdown."
                  : "The service refused shutdown. Refresh service status before trying again.",
            };
            break;
          case "journal-failed":
            lifecycle.settlement = { kind: "journal-failed" };
            break;
        }
      },
      () => {
        for (const observer of lifecycle.observers) observer();
      },
    );
  }

  function update() {
    if (signal.aborted) return;
    const next = canManage();
    if (next !== managed) {
      managed = next;
      void readBuilds();
    }
    sync();
  }

  refresh.addEventListener(
    "click",
    () => {
      void Promise.resolve(onRefreshIdentity?.()).then(() => inspect(true));
    },
    { signal },
  );
  stop.addEventListener("click", () => void requestStop(), { signal });
  const onSettlement = () => showSettlement();
  if (!signal.aborted) lifecycle.observers.add(onSettlement);
  signal.addEventListener(
    "abort",
    () => {
      lifecycle.observers.delete(onSettlement);
    },
    { once: true },
  );
  showSettlement(false);
  update();
  void inspect(false);
  return { update };
}
