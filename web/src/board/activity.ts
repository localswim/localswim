import type { Identity } from "../../generated/archive/contracts.js";
import type { ActivityView } from "../../generated/transport/contracts.js";
import { ActivityCursorStale } from "../api/activity.js";
import type { BoardClient } from "../api/client.js";
import { button, element } from "../recovery-ui/view.js";

const origin = (identity: Identity): string => identity.boardId + " / " + identity.incarnation;

export class BoardActivity {
  readonly host = element("div");
  private readonly open = button("Activity", () => {
    if (!this.identity || !this.panel.hidden) return;
    this.panel.hidden = false;
    this.open.setAttribute("aria-expanded", "true");
    void this.read();
  });
  private readonly panel = element("section");
  private readonly selection = element("p");
  private readonly interval = element("p");
  private readonly status = element("p");
  private readonly results = element("div");
  private readonly next = button("Next page", () => {
    if (this.page?.nextCursor) void this.read(this.page);
  });
  private identity?: Identity;
  private request?: AbortController;
  private page?: ActivityView;

  constructor(private readonly client: Pick<BoardClient, "activity">) {
    this.open.disabled = true;
    this.open.setAttribute("aria-expanded", "false");
    this.panel.hidden = true;
    this.panel.setAttribute("aria-label", "Activity");
    this.status.setAttribute("role", "status");
    const close = button("Close activity", () => { this.reset(); this.open.focus(); });
    this.panel.append(element("h2", "Activity"), this.selection, this.interval, this.status,
      this.results, this.next, button("Start fresh", () => { void this.read(); }), close);
    this.host.append(this.open, this.panel);
  }

  boardRead(identity: Identity): void {
    this.reset();
    this.identity = identity;
    this.open.disabled = false;
  }

  reset(): void {
    this.request?.abort();
    this.request = undefined;
    this.page = undefined;
    this.panel.hidden = true;
    this.open.setAttribute("aria-expanded", "false");
    this.selection.textContent = this.interval.textContent = this.status.textContent = "";
    this.results.replaceChildren();
    this.next.hidden = true;
  }

  private async read(previous?: ActivityView): Promise<void> {
    const identity = this.identity;
    if (!identity || this.panel.hidden) return;
    this.request?.abort();
    const request = new AbortController();
    this.request = request;
    this.page = undefined;
    this.next.hidden = true;
    this.selection.textContent = "Board " + origin(identity);
    this.interval.textContent = "";
    this.status.textContent = "Reading activity…";
    this.results.replaceChildren();
    try {
      const page = await this.client.activity(identity, request.signal, previous);
      if (this.request !== request) return;
      this.page = page;
      this.show(page);
    } catch (error) {
      if (this.request !== request) return;
      this.status.textContent = error instanceof ActivityCursorStale
        ? "Activity cursor is stale. Choose Start fresh."
        : "Activity unavailable. Choose Start fresh to retry.";
    } finally {
      if (this.request === request) this.request = undefined;
    }
  }

  private show(page: ActivityView): void {
    this.interval.textContent = "Interval (" + page.from + ", " + page.through + "]";
    this.status.textContent = "Coverage " + page.coverage.status + ".";
    if (page.coverage.activityFloor !== null) {
      this.results.append(element("p", "Activity floor " + page.coverage.activityFloor));
    }
    if (page.coverage.gaps.length) {
      const gaps = element("ul");
      gaps.setAttribute("aria-label", "Activity coverage gaps");
      for (const gap of page.coverage.gaps) {
        gaps.append(element("li", origin(gap.origin) + " · " + gap.reason +
          " · (" + (gap.fromExclusive ?? "unbounded") + ", " + gap.throughInclusive + "]"));
      }
      this.results.append(gaps);
    }
    if (page.events.length) {
      const events = element("ol");
      events.setAttribute("aria-label", "Activity events");
      for (const event of page.events) {
        events.append(element("li", event.at + " · " + event.type + " · origin " + origin(event.origin) +
          " · event " + event.eventId + " · revision " + event.boardRevision +
          " · actor " + event.actorKind + (event.actorId ? " " + event.actorId : "")));
      }
      this.results.append(events);
    } else {
      this.results.append(element("p", page.coverage.status === "complete"
        ? "No activity in this interval." : "No retained events in this incomplete interval."));
    }
    this.next.hidden = page.nextCursor === null;
  }
}
