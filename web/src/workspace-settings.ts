import { maxRecentBoardBudgetMiB } from "./recent-boards.js";
import type { RecentBoards } from "./recent-boards.js";

export function mountWorkspaceSettings(
  host: HTMLElement,
  cache: RecentBoards,
  signal: AbortSignal,
): void {
  if (signal.aborted) return;
  const doc = host.ownerDocument;
  const trigger = doc.createElement("button");
  trigger.type = "button";
  trigger.className = "workspace-settings-trigger";
  trigger.textContent = "Workspace settings";
  trigger.setAttribute("aria-haspopup", "dialog");

  const dialog = doc.createElement("dialog");
  dialog.setAttribute("aria-labelledby", "workspace-settings-title");
  const title = doc.createElement("h2");
  title.id = "workspace-settings-title";
  title.textContent = "Workspace settings";

  const form = doc.createElement("form");
  const field = doc.createElement("p");
  const label = doc.createElement("label");
  label.htmlFor = "recent-board-budget";
  label.textContent = "Recent board cache budget (MiB)";
  const budget = doc.createElement("input");
  budget.id = label.htmlFor;
  budget.type = "number";
  budget.min = "0";
  budget.max = String(maxRecentBoardBudgetMiB);
  budget.step = "1";
  budget.required = true;
  budget.autofocus = true;
  budget.setAttribute("aria-describedby", "recent-board-budget-help");
  field.append(label, doc.createElement("br"), budget);

  const help = doc.createElement("p");
  help.id = "recent-board-budget-help";
  help.textContent = `Choose 0–${maxRecentBoardBudgetMiB} MiB for this tab. Set 0 to turn the cache off.`;
  const usage = doc.createElement("p");
  usage.setAttribute("role", "status");
  const actions = doc.createElement("div");
  const apply = doc.createElement("button");
  apply.type = "submit";
  apply.textContent = "Apply";
  const close = doc.createElement("button");
  close.type = "button";
  close.textContent = "Close";
  actions.append(apply, " ", close);
  form.append(field, help, usage, actions);
  dialog.append(title, form);

  function showUsage(message = "") {
    const count = `${cache.size} retained ${cache.size === 1 ? "board" : "boards"}`;
    usage.textContent = `${count} · ${cache.byteSize.toLocaleString()} bytes${message}`;
  }

  trigger.addEventListener(
    "click",
    () => {
      budget.value = String(cache.budgetMiB);
      showUsage();
      dialog.showModal();
    },
    { signal },
  );
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      if (!form.reportValidity()) return;
      const saved = cache.setBudgetMiB(budget.valueAsNumber);
      budget.value = String(cache.budgetMiB);
      showUsage(saved ? ". Budget saved." : ". Budget applied until this page reloads.");
    },
    { signal },
  );
  close.addEventListener("click", () => dialog.close(), { signal });
  signal.addEventListener(
    "abort",
    () => {
      dialog.close();
      trigger.remove();
      dialog.remove();
    },
    { once: true },
  );
  host.append(trigger, dialog);
}
