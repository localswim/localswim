type Options = Readonly<{
  signal: AbortSignal;
  scope: "board" | "workspace";
  refresh: () => Promise<boolean>;
}>;

export function mountManualRefresh(host: HTMLElement, { signal, scope, refresh }: Options): void {
  if (signal.aborted) return;
  const doc = host.ownerDocument;
  const button = doc.createElement("button");
  const status = doc.createElement("span");
  const label = scope === "board" ? "Board" : "Workspace";
  button.type = "button";
  button.textContent = "Refresh";
  button.dataset.manualRefresh = scope;
  status.setAttribute("role", "status");
  host.append(button, " ", status);

  let pending = false;
  async function run() {
    if (pending || signal.aborted) return;
    pending = true;
    // Keep the button focusable while its read is in flight.
    button.setAttribute("aria-disabled", "true");
    status.textContent = `Refreshing ${scope}…`;

    let confirmed: boolean;
    try {
      confirmed = await refresh();
    } catch {
      confirmed = false;
    }
    if (signal.aborted) return;
    status.textContent = confirmed ? `${label} refreshed.` : `Could not refresh ${scope}.`;
    button.removeAttribute("aria-disabled");
    pending = false;
  }

  button.addEventListener("click", () => void run(), { signal });
}
