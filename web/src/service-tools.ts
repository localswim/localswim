import { ApiError } from "../api.mjs";
import type { createBrowserApi } from "../api.mjs";
import { waitForRetry } from "../board-updates.mjs";
import { newOperationId } from "../operation-id.mjs";
import { createScopedOperations, submitOperation } from "./operations.js";
import type { createWorkspaceState } from "./workspace-state.js";

type Api = Pick<
  ReturnType<typeof createBrowserApi>,
  "getService" | "listBuilds" | "stopService" | "activateBuild" | "getActivation"
>;
type ServiceReply = Awaited<ReturnType<Api["getService"]>>;
type Service = ServiceReply["data"];
type StopIntent = Readonly<{ instanceId: string; etag: string; acknowledged: boolean }>;
type ActivationIntent = Readonly<{
  id: string;
  buildId: string;
  instanceId: string;
  etag: string;
  port: number;
}>;
type StopSettlement =
  | { kind: "confirmed"; service: Service }
  | { kind: "refused"; message: string; refresh: boolean }
  | { kind: "unknown" | "journal-failed" };
type Lifecycle = {
  operations: ReturnType<typeof createScopedOperations>;
  settlement?: StopSettlement;
  activationMessage?: string;
  activationRefusal: number;
  activationRefreshRequired: boolean;
  observers: Set<() => void>;
};
type Options = Readonly<{
  api: Api;
  readWithSession: ReturnType<typeof createWorkspaceState>["readWithSession"];
  signal: AbortSignal;
  canManage: () => boolean;
  onRefreshIdentity?: () => Promise<void> | void;
  onReconnect?: (signal: AbortSignal) => Promise<void>;
  onPendingChange?: () => void;
  recoveryOnly?: boolean;
}>;

const stopKey = "localswim.service-stop.v1";
const activationKey = "localswim.service-activation.v1";
const lifecycles = new WeakMap<Document, Lifecycle>();
const instanceIdPattern = /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/;
const buildIdPattern = /^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/;
const serviceTagPattern = /^"[A-Za-z0-9_-]{16,128}"$/;
const activationCheckMs = 8_000;
const activationRetryMs = 750;

export function hasPendingServiceActivation(storage?: Pick<Storage, "getItem">) {
  try {
    return ((storage ?? globalThis.sessionStorage)?.getItem(activationKey) ?? null) !== null;
  } catch {
    return false;
  }
}

function readActivationIntent(storage: Storage): ActivationIntent | null {
  const raw = storage.getItem(activationKey);
  if (raw === null) return null;
  const value: unknown = JSON.parse(raw);
  if (
    typeof value !== "object" ||
    value === null ||
    !("id" in value) ||
    typeof value.id !== "string" ||
    !instanceIdPattern.test(value.id) ||
    !("buildId" in value) ||
    typeof value.buildId !== "string" ||
    !buildIdPattern.test(value.buildId) ||
    !("instanceId" in value) ||
    typeof value.instanceId !== "string" ||
    !instanceIdPattern.test(value.instanceId) ||
    !("etag" in value) ||
    typeof value.etag !== "string" ||
    !serviceTagPattern.test(value.etag) ||
    !("port" in value) ||
    typeof value.port !== "number" ||
    !Number.isInteger(value.port) ||
    value.port < 1 ||
    value.port > 65535
  ) {
    throw new Error("Invalid service activation record.");
  }
  const { id, buildId, instanceId, etag, port } = value;
  return { id, buildId, instanceId, etag, port };
}

function lifecycleFor(doc: Document): Lifecycle {
  const existing = lifecycles.get(doc);
  if (existing) return existing;
  const lifecycle = {
    operations: createScopedOperations(),
    observers: new Set<() => void>(),
    activationRefusal: 0,
    activationRefreshRequired: false,
  };
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
  {
    api,
    readWithSession,
    signal,
    canManage,
    onRefreshIdentity,
    onReconnect,
    onPendingChange,
    recoveryOnly = false,
  }: Options,
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
  const activate = doc.createElement("button");
  const choiceLabel = doc.createElement("label");
  const choice = doc.createElement("select");
  const activationOutcome = doc.createElement("p");
  heading.textContent = "Service";
  health.textContent = "Reading service status…";
  health.setAttribute("role", "status");
  outcome.id = "service-operation-status";
  outcome.setAttribute("role", "status");
  refresh.type = "button";
  refresh.textContent = "Refresh service status";
  stop.type = "button";
  stop.textContent = "Shut down service";
  choiceLabel.append("Staged build to activate ", choice);
  activate.type = "button";
  activate.textContent = "Activate staged build";
  activationOutcome.id = "service-activation-status";
  activationOutcome.setAttribute("role", "status");
  section.id = "service-controls";
  section.append(
    heading,
    health,
    buildStatus,
    builds,
    choiceLabel,
    activate,
    refresh,
    stop,
    outcome,
    activationOutcome,
  );
  host.append(section);

  let storage: Storage | undefined;
  let pending: StopIntent | null = null;
  let storageReady = false;
  let service: ServiceReply | undefined;
  let inspecting = false;
  let refreshAfterInspection = false;
  let managed = false;
  let buildRead: AbortController | undefined;
  let activationReady = false;
  let activation: ActivationIntent | null = null;
  let sawActivation = false;
  let pendingReported = false;
  let checkingActivation = false;
  let autoInspectedId: string | undefined;
  let reconnectedInstance: string | undefined;
  let buildChoices: Awaited<ReturnType<Api["listBuilds"]>>["data"]["builds"] = [];
  let selectedBuild = "";

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

  function loadActivation() {
    try {
      if (!storage) return false;
      activation = readActivationIntent(storage);
      return true;
    } catch {
      return false;
    }
  }

  function notifyLifecycle() {
    for (const observer of lifecycle.observers) observer();
  }

  function saveActivation(next: ActivationIntent | null) {
    if (!storage) return false;
    try {
      if (next) storage.setItem(activationKey, JSON.stringify(next));
      else storage.removeItem(activationKey);
      activation = next;
    } catch {
      return false;
    }
    return true;
  }

  function updateBuildChoices() {
    const eligible = buildChoices.filter(
      (build) => build.validation === "valid" && build.id !== service?.data.build_id,
    );
    const placeholder = doc.createElement("option");
    placeholder.value = "";
    placeholder.textContent = "Choose a staged build";
    choice.replaceChildren(placeholder);
    for (const build of eligible) {
      const option = doc.createElement("option");
      option.value = build.id;
      option.textContent = `${build.version} (${build.id})`;
      choice.append(option);
    }
    choice.value = eligible.some((build) => build.id === selectedBuild)
      ? selectedBuild
      : eligible.length === 1
        ? (eligible[0]?.id ?? "")
        : "";
  }

  function sync() {
    if (signal.aborted) return;
    const submitting = lifecycle.operations.state(stopKey) === "submitting";
    const activating = lifecycle.operations.state(activationKey) === "submitting";
    const showControls = managed && !recoveryOnly;
    section.hidden = recoveryOnly && (!managed || !sawActivation);
    stop.hidden = !showControls;
    outcome.hidden = !showControls;
    buildStatus.hidden = !showControls;
    builds.hidden = !showControls || builds.childElementCount === 0;
    activate.hidden = !showControls;
    choiceLabel.hidden = !showControls;
    activationOutcome.hidden = !managed || activationOutcome.textContent === "";
    refresh.disabled = inspecting || activating || checkingActivation;
    const cannotChangeService =
      inspecting ||
      activating ||
      checkingActivation ||
      activation !== null ||
      !activationReady ||
      lifecycle.activationRefreshRequired;
    stop.disabled =
      cannotChangeService ||
      submitting ||
      lifecycle.settlement?.kind === "unknown" ||
      pending !== null ||
      !storageReady ||
      service?.data.state !== "running";
    choice.disabled = cannotChangeService || service?.data.state !== "running";
    activate.disabled = stop.disabled || !choice.value || !onReconnect;
  }

  async function readBuilds() {
    buildRead?.abort();
    builds.replaceChildren();
    buildChoices = [];
    updateBuildChoices();
    buildStatus.textContent = "";
    if (!managed || signal.aborted || recoveryOnly) return;
    const controller = new AbortController();
    buildRead = controller;
    const readSignal = AbortSignal.any([signal, controller.signal]);
    try {
      const reply = await readWithSession(api.listBuilds, readSignal);
      if (readSignal.aborted || !canManage()) return;
      buildChoices = reply.data.builds;
      updateBuildChoices();
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

  function reportActivation() {
    activationOutcome.textContent =
      lifecycle.activationMessage ??
      (!activationReady
        ? "Activation is unavailable because this tab could not use its activation record."
        : activation
          ? `Inspect activation receipt ${activation.id} to check the outcome.`
          : "");
  }

  async function readActivationService(readSignal: AbortSignal) {
    try {
      const latest = await readWithSession(() => api.getService(readSignal), readSignal);
      readSignal.throwIfAborted();
      service = latest;
      health.textContent = serviceLabel(latest.data);
      return latest.data;
    } catch (error) {
      if (!signal.aborted) {
        service = undefined;
        health.textContent = "Service status is unavailable.";
      }
      throw error;
    }
  }

  async function inspectActivation(poll: boolean) {
    if (
      checkingActivation ||
      inspecting ||
      signal.aborted ||
      !canManage() ||
      lifecycle.operations.state(activationKey) === "submitting" ||
      !activation
    )
      return;
    const original = activation;
    checkingActivation = true;
    autoInspectedId = original.id;
    activationOutcome.textContent = `Checking activation receipt ${original.id} and service status…`;
    sync();
    const readSignal = AbortSignal.any([signal, AbortSignal.timeout(activationCheckMs)]);
    try {
      do {
        try {
          const {
            service_instance_id: instance,
            port,
            state,
          } = await readActivationService(readSignal);
          const replaced =
            state === "running" && port === original.port && instance !== original.instanceId;
          if (replaced && reconnectedInstance !== instance && onReconnect) {
            await onReconnect(readSignal);
            readSignal.throwIfAborted();
            reconnectedInstance = instance;
          }
          const receipt = await readWithSession(
            () => api.getActivation(original.id, original.buildId, readSignal),
            readSignal,
          );
          readSignal.throwIfAborted();
          let activated = false;
          if (receipt.state === "activated" && replaced && instance === reconnectedInstance) {
            const confirmed = await readActivationService(readSignal);
            activated =
              confirmed.service_instance_id === instance &&
              confirmed.port === original.port &&
              confirmed.state === "running" &&
              confirmed.build_id === original.buildId;
          }
          if (!canManage() || readActivationIntentIfAvailable()?.id !== original.id) return;
          if (activated || receipt.state === "failed") {
            const message = activated
              ? `Build activated. Reconnected on the same port. Receipt ${original.id}.`
              : `Build activation failed${receipt.error_code ? ` (${receipt.error_code})` : ""}. Receipt ${original.id}.`;
            if (saveActivation(null)) {
              lifecycle.activationMessage = message;
            } else {
              lifecycle.activationMessage = `${message} This tab could not clear the activation record.`;
            }
            return;
          }
        } catch {
          // A missing receipt or an unavailable service can be temporary during handoff.
        }
        if (poll) await waitForRetry(readSignal, activationRetryMs);
      } while (poll && !readSignal.aborted && canManage());
      if (!signal.aborted) {
        lifecycle.activationMessage = `Activation outcome is unconfirmed. Refresh service status to inspect receipt ${original.id}.`;
      }
    } finally {
      checkingActivation = false;
      if (!signal.aborted) {
        notifyLifecycle();
        void readBuilds();
      }
    }
  }

  function readActivationIntentIfAvailable() {
    try {
      return storage && readActivationIntent(storage);
    } catch {
      return undefined;
    }
  }

  function resumeActivation() {
    if (
      activation &&
      activation.id !== autoInspectedId &&
      canManage() &&
      !inspecting &&
      lifecycle.operations.state(activationKey) !== "submitting"
    ) {
      void inspectActivation(true);
    }
  }

  async function requestActivation() {
    if (
      signal.aborted ||
      recoveryOnly ||
      !canManage() ||
      activate.disabled ||
      !onReconnect ||
      service?.data.state !== "running"
    )
      return;
    const prior = service;
    const buildId = choice.value;
    if (!buildChoices.some((build) => build.id === buildId && build.validation === "valid")) return;
    await lifecycle.operations.run(
      activationKey,
      "submitting",
      async () => {
        storageReady = loadIntent();
        activationReady = loadActivation();
        if (!storageReady || pending || !activationReady || activation) {
          reportActivation();
          return;
        }
        const intent = {
          id: newOperationId(),
          buildId,
          instanceId: prior.data.service_instance_id,
          etag: prior.etag,
          port: prior.data.port,
        };
        lifecycle.activationMessage = `Requesting build activation. Receipt ${intent.id}.`;
        const result = await submitOperation(
          () => saveActivation(intent),
          () => {
            notifyLifecycle();
            return api.activateBuild(intent.id, intent.buildId, intent.etag);
          },
        );
        switch (result.kind) {
          case "confirmed":
            lifecycle.activationMessage = `Activation accepted. Reconnecting to inspect receipt ${intent.id}…`;
            break;
          case "unknown":
            lifecycle.activationMessage = `Activation outcome is uncertain. Checking receipt ${intent.id}…`;
            break;
          case "stale":
          case "refused":
            lifecycle.activationRefusal++;
            lifecycle.activationRefreshRequired = true;
            lifecycle.activationMessage =
              result.kind === "stale"
                ? "Service status changed. Refresh it before requesting activation."
                : "The service refused build activation. Refresh service status before trying again.";
            if (!saveActivation(null))
              lifecycle.activationMessage += " This tab could not clear the activation record.";
            break;
          case "journal-failed":
            lifecycle.activationMessage =
              "Activation is unavailable because this tab could not save its activation record.";
            break;
        }
      },
      notifyLifecycle,
    );
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
    if (inspecting || checkingActivation || signal.aborted) return;
    if (explicit && lifecycle.operations.state(stopKey) === "submitting") {
      outcome.textContent =
        "A shutdown request is still in progress. Refresh service status after it settles.";
      return;
    }
    inspecting = true;
    const previousSettlement = lifecycle.settlement;
    const previousRefusal = lifecycle.activationRefusal;
    const stillCurrent = () =>
      !signal.aborted &&
      lifecycle.settlement === previousSettlement &&
      lifecycle.activationRefusal === previousRefusal;
    sync();
    try {
      const latest = await readWithSession(() => api.getService(signal), signal);
      if (stillCurrent()) {
        service = latest;
        if (explicit) lifecycle.activationRefreshRequired = false;
        updateBuildChoices();
        health.textContent = serviceLabel(latest.data);
      }
    } catch {
      if (stillCurrent()) {
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
      resumeActivation();
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
      recoveryOnly ||
      !canManage() ||
      stop.disabled ||
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
        activationReady = loadActivation();
        if (!storageReady || pending || !activationReady || activation) {
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
      notifyLifecycle,
    );
  }

  function update() {
    if (signal.aborted) return;
    const next = canManage();
    if (next !== managed) {
      managed = next;
      if (managed && !recoveryOnly) void readBuilds();
      else buildRead?.abort();
    }
    sync();
    resumeActivation();
  }

  refresh.addEventListener(
    "click",
    () => {
      if (activation && canManage()) {
        void inspectActivation(false);
      } else {
        const refusal = lifecycle.activationRefusal;
        void Promise.resolve(onRefreshIdentity?.()).then(() => {
          if (refusal === lifecycle.activationRefusal) void inspect(true);
        });
      }
    },
    { signal },
  );
  stop.addEventListener("click", () => void requestStop(), { signal });
  choice.addEventListener(
    "change",
    () => {
      selectedBuild = choice.value;
      sync();
    },
    { signal },
  );
  activate.addEventListener("click", () => void requestActivation(), { signal });
  let lastStopSettlement = lifecycle.settlement;
  let lastActivationRefusal = lifecycle.activationRefusal;
  const onSettlement = () => {
    if (lastActivationRefusal !== lifecycle.activationRefusal) {
      lastActivationRefusal = lifecycle.activationRefusal;
      service = undefined;
      health.textContent = "Refresh service status to check the current instance.";
    }
    if (lastStopSettlement !== lifecycle.settlement) {
      lastStopSettlement = lifecycle.settlement;
      showSettlement();
    }
    activationReady = loadActivation();
    const hasPending = hasPendingServiceActivation(storage);
    sawActivation ||= hasPending;
    if (pendingReported !== hasPending) {
      pendingReported = hasPending;
      onPendingChange?.();
    }
    reportActivation();
    sync();
    resumeActivation();
  };
  if (!signal.aborted) lifecycle.observers.add(onSettlement);
  signal.addEventListener(
    "abort",
    () => {
      lifecycle.observers.delete(onSettlement);
    },
    { once: true },
  );
  showSettlement(false);
  activationReady = loadActivation();
  sawActivation = pendingReported = hasPendingServiceActivation(storage);
  reportActivation();
  update();
  if (!recoveryOnly) void inspect(false);
  return { update };
}
