import type { Timestamp, Uuid } from "../api-types.js";
import type { ReadState } from "../contracts.js";
import type { LiveConnection } from "./controller.js";

const disconnectGraceMs = 10_000;

interface StatusElements {
  readonly build: HTMLElement;
  readonly latest: HTMLElement;
  readonly clock: HTMLTimeElement;
  readonly status: HTMLElement;
}

interface WorkspaceStatus {
  readonly connection: LiveConnection;
  readonly buildId: Uuid | null;
  readonly lastCardEdit: Timestamp | null;
  readonly title: string;
}

const localTime = new Intl.DateTimeFormat("en-US", {
  hour: "numeric",
  minute: "2-digit",
  second: "2-digit",
  hour12: true,
});
const relativeTime = new Intl.RelativeTimeFormat("en-US");

function cardAge(timestamp: Timestamp, now: number): string {
  const elapsed = now - Date.parse(timestamp.replace(" ", "T"));
  const seconds = Math.max(0, Math.floor(elapsed / 1000));
  if (seconds < 60) return relativeTime.format(-seconds, "second");
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return relativeTime.format(-minutes, "minute");
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return relativeTime.format(-hours, "hour");
  return relativeTime.format(-Math.floor(hours / 24), "day");
}

export function workspaceTabTitle(
  workspaceStatus: ReadState<unknown>["status"],
  boardStatus: ReadState<unknown>["status"] | undefined,
  boardName: string,
  needsTerry: number,
): string {
  if (boardStatus === "loading") return `${boardName} · Loading`;
  if (boardStatus === "ready") return needsTerry > 0 ? `(${needsTerry}) ${boardName}` : boardName;
  return workspaceStatus === "loading" ? "localswim" : "Board unavailable · localswim";
}

export function mountWorkspaceStatus(root: HTMLElement, elements: StatusElements) {
  const previousTitle = document.title;
  let lastCardEdit: Timestamp | null = null;
  let visibleConnection: LiveConnection = "connecting";
  let latestConnection: LiveConnection = visibleConnection;
  let disconnectTimer: number | undefined;

  function showConnection(connection: LiveConnection): void {
    visibleConnection = connection;
    root.classList.toggle("lsw-workspace-connected", connection === "connected");
    root.dataset.connection = connection;
    const label = connection.toUpperCase();
    if (elements.status.textContent !== label) elements.status.textContent = label;
  }

  function updateConnection(connection: LiveConnection): void {
    latestConnection = connection;
    if (visibleConnection === "connected" && connection !== "connected") {
      disconnectTimer ??= window.setTimeout(() => {
        disconnectTimer = undefined;
        showConnection(latestConnection);
      }, disconnectGraceMs);
      return;
    }
    window.clearTimeout(disconnectTimer);
    disconnectTimer = undefined;
    showConnection(connection);
  }

  function updateClock(): void {
    const now = Date.now();
    elements.clock.textContent = localTime.format(now).replace(/\s/g, "").toLowerCase();
    elements.latest.textContent =
      lastCardEdit === null ? "" : `· Last card update: ${cardAge(lastCardEdit, now)}`;
  }

  const clockTimer = window.setInterval(updateClock, 1000);
  return {
    update({ connection, buildId, lastCardEdit: nextLastCardEdit, title }: WorkspaceStatus) {
      updateConnection(connection);
      elements.build.textContent = buildId === null ? "" : `Build ${buildId.slice(0, 7)}`;
      if (document.title !== title) document.title = title;
      lastCardEdit = nextLastCardEdit;
      updateClock();
    },
    dispose() {
      window.clearInterval(clockTimer);
      window.clearTimeout(disconnectTimer);
      document.title = previousTitle;
    },
  };
}
