interface SidebarDom {
  readonly root: HTMLElement;
  readonly body: HTMLElement;
  readonly sidebar: HTMLElement;
  readonly title: HTMLElement;
  readonly toggle: HTMLButtonElement;
  readonly grip: HTMLElement;
}

interface SidebarPreference {
  readonly expandedWidth: number;
  readonly collapsed: boolean;
}

const preferenceKey = "localswim.workspace.sidebar";
const defaultWidth = 312;
const minBoardWidth = 320;
const keyboardStep = 16;
const largeKeyboardStep = 64;
const widthProperty = "--lsw-workspace-sidebar-width";
const collapsedClass = "lsw-workspace-sidebar-collapsed";
const resizingClass = "lsw-workspace-sidebar-resizing";

function readPreference(view: Window): SidebarPreference {
  const fallback = { expandedWidth: defaultWidth, collapsed: false };
  try {
    const value = view.sessionStorage.getItem(preferenceKey);
    if (value === null) return fallback;
    const parsed: unknown = JSON.parse(value);
    if (typeof parsed !== "object" || parsed === null) return fallback;
    return {
      expandedWidth:
        "expandedWidth" in parsed &&
        typeof parsed.expandedWidth === "number" &&
        Number.isSafeInteger(parsed.expandedWidth) &&
        parsed.expandedWidth >= defaultWidth
          ? parsed.expandedWidth
          : defaultWidth,
      collapsed: "collapsed" in parsed && parsed.collapsed === true,
    };
  } catch {
    return fallback;
  }
}

function writePreference(view: Window, preference: SidebarPreference): void {
  try {
    view.sessionStorage.setItem(preferenceKey, JSON.stringify(preference));
  } catch {
    // The current window can still use the controls when storage is unavailable.
  }
}

export function mountSidebar({ root, body, sidebar, title, toggle, grip }: SidebarDom): {
  dispose(): void;
} {
  const ownerWindow = sidebar.ownerDocument.defaultView;
  if (ownerWindow === null) return { dispose() {} };
  const view = ownerWindow;

  const listeners = new AbortController();
  const options = { signal: listeners.signal };
  let preference = readPreference(view);
  let drag: { id: number; x: number; initialWidth: number; width: number } | null = null;

  function upperWidth(): number {
    return Math.max(defaultWidth, body.clientWidth - minBoardWidth);
  }

  function clamp(width: number): number {
    return Math.max(defaultWidth, Math.min(upperWidth(), Math.round(width)));
  }

  function renderWidth(): void {
    const width = clamp(drag?.width ?? preference.expandedWidth);
    body.style.setProperty(widthProperty, `${width}px`);
    grip.setAttribute("aria-valuemax", String(upperWidth()));
    grip.setAttribute("aria-valuenow", String(width));
  }

  function renderCollapse(): void {
    sidebar.classList.toggle(collapsedClass, preference.collapsed);
    title.hidden = preference.collapsed;
    grip.hidden = preference.collapsed;
    const label = preference.collapsed ? "Expand boards" : "Collapse boards";
    toggle.title = label;
    toggle.setAttribute("aria-label", label);
    toggle.setAttribute("aria-expanded", String(!preference.collapsed));
  }

  function setWidth(expandedWidth: number): void {
    if (expandedWidth !== preference.expandedWidth) {
      preference = { ...preference, expandedWidth };
      writePreference(view, preference);
    }
    renderWidth();
  }

  function finishDrag(commit: boolean): void {
    if (drag === null) return;
    const { id, initialWidth, width } = drag;
    drag = null;
    root.classList.remove(resizingClass);
    if (grip.hasPointerCapture(id)) grip.releasePointerCapture(id);
    if (commit && width !== initialWidth) setWidth(clamp(width));
    else renderWidth();
  }

  toggle.disabled = false;
  sidebar.id ||= "workspace-boards-sidebar";
  toggle.setAttribute("aria-controls", sidebar.id);
  grip.removeAttribute("aria-hidden");
  grip.tabIndex = 0;
  grip.title = "Drag to resize · Left/Right arrows, Home/End · Double-click to reset";
  grip.setAttribute("role", "separator");
  grip.setAttribute("aria-label", "Resize boards sidebar");
  grip.setAttribute("aria-orientation", "vertical");
  grip.setAttribute("aria-controls", sidebar.id);
  grip.setAttribute("aria-valuemin", String(defaultWidth));

  toggle.addEventListener(
    "click",
    () => {
      finishDrag(false);
      preference = { ...preference, collapsed: !preference.collapsed };
      renderCollapse();
      writePreference(view, preference);
    },
    options,
  );
  grip.addEventListener(
    "keydown",
    (event) => {
      if (drag !== null || event.altKey || event.ctrlKey || event.metaKey) return;
      const current = clamp(preference.expandedWidth);
      const step = event.shiftKey ? largeKeyboardStep : keyboardStep;
      let width: number;
      switch (event.key) {
        case "ArrowLeft":
          width = current - step;
          break;
        case "ArrowRight":
          width = current + step;
          break;
        case "Home":
          width = defaultWidth;
          break;
        case "End":
          width = upperWidth();
          break;
        default:
          return;
      }
      event.preventDefault();
      const next = clamp(width);
      if (next !== current || event.key === "Home" || event.key === "End") setWidth(next);
    },
    options,
  );
  grip.addEventListener("dblclick", () => setWidth(defaultWidth), options);
  grip.addEventListener(
    "pointerdown",
    (event) => {
      if (drag !== null || event.button !== 0 || !event.isPrimary || preference.collapsed) return;
      const width = clamp(preference.expandedWidth);
      grip.setPointerCapture(event.pointerId);
      drag = { id: event.pointerId, x: event.clientX, initialWidth: width, width };
      root.classList.add(resizingClass);
    },
    options,
  );
  grip.addEventListener(
    "pointermove",
    (event) => {
      if (drag === null || drag.id !== event.pointerId) return;
      drag.width = clamp(drag.initialWidth + event.clientX - drag.x);
      renderWidth();
    },
    options,
  );
  grip.addEventListener(
    "pointerup",
    (event) => {
      if (drag === null || drag.id !== event.pointerId) return;
      drag.width = clamp(drag.initialWidth + event.clientX - drag.x);
      finishDrag(true);
    },
    options,
  );
  for (const type of ["pointercancel", "lostpointercapture"] as const) {
    grip.addEventListener(
      type,
      (event) => {
        if (drag?.id === event.pointerId) finishDrag(false);
      },
      options,
    );
  }
  view.addEventListener("resize", renderWidth, options);
  view.addEventListener(
    "keydown",
    (event) => {
      if (event.key !== "Escape" || drag === null) return;
      event.preventDefault();
      finishDrag(false);
    },
    options,
  );
  renderWidth();
  renderCollapse();

  return {
    dispose() {
      listeners.abort();
      finishDrag(false);
    },
  };
}
