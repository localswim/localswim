import {
  ApiError,
  DEFAULT_APPROVAL_COMMENT,
  MAX_BULK_APPROVAL_COMMENT_LENGTH,
  MAX_LANE_NOTES_LENGTH,
} from "../api.mjs";
import type { BoardViewData } from "./board-view.js";
import type { AppContext } from "./model.js";
import { submitOperation, type OperationStorage } from "./operations.js";
import {
  alignTextarea,
  applyInput,
  rememberExact,
  textareaText,
  type InputChange,
} from "./text-input.js";

type Lane = BoardViewData["lanes"][number];
type LanePatch = Parameters<AppContext["api"]["updateLaneSettings"]>[4];
type LaneAddition = Required<
  Pick<
    Parameters<AppContext["api"]["createLane"]>[2],
    "id" | "label" | "after_id" | "create_for" | "connect_for"
  >
>;
type Change =
  | { kind: "add"; value: LaneAddition }
  | { kind: "patch"; laneId: string; revision: number; value: LanePatch };
type Intent = Change & { boardEtag: string };
type Context = {
  data: BoardViewData | null;
  actorId?: string;
  allowed: boolean;
  writable: boolean;
};
type Options = AppContext & {
  boardId: string;
  readWithSession: <T>(read: () => Promise<T>, signal: AbortSignal) => Promise<T>;
  getContext: () => Context;
  canInspectSaved?: () => boolean;
  beginWrite: () => (() => void) | null;
  beginInspection: () => (() => void) | null;
  onChange: () => void;
  storage?: OperationStorage;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const limits = {
  label: 256,
  key: 128,
  notes: MAX_LANE_NOTES_LENGTH,
  bulk_approval_comment: MAX_BULK_APPROVAL_COMMENT_LENGTH,
} as const;

function isId(value: unknown): value is string {
  return typeof value === "string" && value.length === 36 && uuid.test(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function validText(value: unknown, max: number, required = false): value is string {
  return (
    typeof value === "string" &&
    value.length <= max * 2 &&
    value.isWellFormed() &&
    [...value].length <= max &&
    (!required || value !== "")
  );
}

function readIds(value: unknown): string[] | undefined {
  if (!Array.isArray(value) || value.length > 1000) return undefined;
  const ids: unknown[] = value;
  if (!ids.every(isId) || new Set(ids).size !== ids.length) return undefined;
  return ids;
}

function revisionOf(boardId: string, tag: string) {
  const revision = Number(tag.slice(boardId.length + 2, -1));
  return Number.isSafeInteger(revision) && revision > 0 && tag === `"${boardId}-${revision}"`
    ? revision
    : 0;
}

function parseIntent(raw: string, boardId: string): Intent {
  const entry: unknown = JSON.parse(raw);
  if (isRecord(entry) && typeof entry.boardEtag === "string" && isRecord(entry.value)) {
    const { boardEtag, value, revision, laneId } = entry;
    const boardRevision = revisionOf(boardId, boardEtag);
    if (boardRevision) {
      if (entry.kind === "add") {
        const { id, label, after_id } = value;
        const create_for = readIds(value.create_for);
        const connect_for = readIds(value.connect_for);
        if (
          isId(id) &&
          isId(after_id) &&
          id !== after_id &&
          validText(label, limits.label, true) &&
          create_for &&
          connect_for
        ) {
          return {
            kind: "add",
            boardEtag,
            value: { id, label, after_id, create_for, connect_for },
          };
        }
      }
      if (
        entry.kind === "patch" &&
        isId(laneId) &&
        typeof revision === "number" &&
        Number.isSafeInteger(revision) &&
        revision > 0 &&
        revision <= boardRevision
      ) {
        const patch: LanePatch = {};
        for (const [field, input] of Object.entries(value)) {
          if (field === "retired" && input === true && Object.keys(value).length === 1)
            patch.retired = true;
          else if (field === "approval_enabled" && typeof input === "boolean")
            patch.approval_enabled = input;
          else if (field === "label" && validText(input, limits.label, true)) patch.label = input;
          else if (field === "key" && validText(input, limits.key, true) && input !== "completed")
            patch.key = input;
          else if (field === "notes" && validText(input, limits.notes)) patch.notes = input;
          else if (
            field === "bulk_approval_comment" &&
            validText(input, limits.bulk_approval_comment)
          )
            patch.bulk_approval_comment = input;
          else throw new TypeError("Invalid lane change.");
        }
        if (Object.keys(patch).length)
          return { kind: "patch", boardEtag, laneId, revision, value: patch };
      }
    }
  }
  throw new TypeError("Invalid lane change.");
}

function intentMatches(
  intent: Intent,
  lane: Awaited<ReturnType<AppContext["api"]["getLaneSettings"]>>,
) {
  return intent.kind === "add"
    ? lane.role === "ordinary" && lane.label === intent.value.label && !lane.retired
    : lane.revision > intent.revision &&
        Object.entries(intent.value).every(
          ([field, value]) =>
            (field === "bulk_approval_comment" || field === "notes"
              ? (lane[field] ?? "")
              : lane[field]) === value,
        );
}

function matchesAddedGrants(
  policy: object,
  boardId: string,
  policyId: string,
  addition: LaneAddition,
  peers: readonly { id: string }[],
) {
  if (
    !isRecord(policy) ||
    policy.board_id !== boardId ||
    policy.id !== policyId ||
    !Array.isArray(policy.create_grants) ||
    !Array.isArray(policy.move_grants)
  )
    return false;
  const { id, create_for, connect_for } = addition;
  const creates = new Set(create_for);
  for (const grant of policy.create_grants as unknown[]) {
    if (!isRecord(grant)) return false;
    if (grant.lane_id === id && (!isId(grant.user_id) || !creates.delete(grant.user_id)))
      return false;
  }
  const moves = new Set(
    connect_for.flatMap((user) =>
      peers.flatMap((peer) => [`${user}:${id}:${peer.id}`, `${user}:${peer.id}:${id}`]),
    ),
  );
  for (const grant of policy.move_grants as unknown[]) {
    if (!isRecord(grant)) return false;
    const { user_id, from_lane_id, to_lane_id } = grant;
    if (from_lane_id !== id && to_lane_id !== id) continue;
    if (
      !isId(user_id) ||
      !isId(from_lane_id) ||
      !isId(to_lane_id) ||
      !moves.delete(`${user_id}:${from_lane_id}:${to_lane_id}`)
    )
      return false;
  }
  return creates.size === 0 && moves.size === 0;
}

function exactField(doc: Document, signal: AbortSignal, changed: () => void, rows: number) {
  const node = doc.createElement("textarea");
  node.rows = rows;
  let value = "";
  let change: InputChange | undefined;
  let pasted: string | undefined;
  let composition: InputChange | undefined;
  const recent = new Map<string, string | null>();

  function edited(input: InputChange | undefined, history = false) {
    value = history ? (recent.get(node.value) ?? node.value) : applyInput(value, node.value, input);
    alignTextarea(node, textareaText(value));
    if (!history) rememberExact(recent, value);
    change = undefined;
    changed();
  }

  node.addEventListener(
    "paste",
    (event) => {
      pasted = event.clipboardData?.getData("text/plain");
    },
    { signal },
  );
  node.addEventListener(
    "beforeinput",
    (event) => {
      change = {
        start: node.selectionStart,
        end: node.selectionEnd,
        inserted: event.inputType === "insertFromPaste" ? (pasted ?? event.data) : event.data,
      };
      pasted = undefined;
    },
    { signal },
  );
  node.addEventListener(
    "compositionstart",
    () => {
      composition = { start: node.selectionStart, end: node.selectionEnd, inserted: null };
    },
    { signal },
  );
  node.addEventListener(
    "compositionend",
    () => {
      if (!composition) return;
      edited(composition);
      composition = undefined;
    },
    { signal },
  );
  node.addEventListener(
    "input",
    (event) => {
      const input = event instanceof InputEvent ? event : undefined;
      if (composition || input?.isComposing) return;
      if ((!change || change.start === change.end) && input?.inputType.startsWith("delete")) {
        const start = node.selectionStart;
        change = {
          start,
          end: start + textareaText(value).length - node.value.length,
          inserted: null,
        };
      }
      edited(change, input?.inputType === "historyUndo" || input?.inputType === "historyRedo");
    },
    { signal },
  );
  return {
    node,
    get value() {
      return value;
    },
    set(text: string) {
      value = text;
      node.value = textareaText(text);
      change = composition = undefined;
      pasted = undefined;
      recent.clear();
      rememberExact(recent, text);
    },
  };
}

export function mountLaneSettings(host: HTMLElement, options: Options) {
  const {
    api,
    signal,
    boardId,
    readWithSession,
    getContext,
    canInspectSaved = () => true,
    beginWrite,
    beginInspection,
    onChange,
  } = options;
  const doc = host.ownerDocument;
  const panel = doc.createElement("details");
  panel.className = "lane-settings";
  const summary = doc.createElement("summary");
  summary.textContent = "Manage lanes";
  const retiredLanes = doc.createElement("ul");
  retiredLanes.setAttribute("aria-label", "Retired lanes");
  retiredLanes.hidden = true;
  const feedback = doc.createElement("p");
  feedback.setAttribute("role", "status");
  const retirementStatus = doc.createElement("p");
  retirementStatus.setAttribute("role", "status");
  const addForm = doc.createElement("form");
  const editForm = doc.createElement("form");
  addForm.className = editForm.className = "lane-settings-form";
  const addFields = doc.createElement("fieldset");
  const editFields = doc.createElement("fieldset");
  const newLabel = exactField(doc, signal, edited, 1);
  const after = doc.createElement("select");
  const target = doc.createElement("select");
  const label = exactField(doc, signal, edited, 1);
  const key = exactField(doc, signal, edited, 1);
  const notes = exactField(doc, signal, edited, 3);
  const comment = exactField(doc, signal, edited, 3);
  comment.node.placeholder = DEFAULT_APPROVAL_COMMENT;
  const approval = doc.createElement("input");
  approval.type = "checkbox";
  const approvalFields = doc.createElement("fieldset");
  const createGrants = grantControls("Create rights");
  const connectGrants = grantControls("Connect rights");
  const add = button("Add lane", "submit");
  const save = button("Save lane settings", "submit");
  const reload = button("Reload lane settings");
  const retire = button("Retire lane");
  const confirmRetirement = button("Confirm retirement");
  const cancelRetirement = button("Cancel retirement");
  const inspect = button("Inspect lane change");
  const retry = button("Retry lane change");
  const dismiss = button("Dismiss pending change");
  const actions = doc.createElement("div");
  actions.className = "lane-settings-actions";
  const recovery = doc.createElement("div");
  recovery.className = "lane-settings-actions";

  approvalFields.append(
    legend("Lane approval"),
    field("Enable lane approval", approval),
    field("Bulk approval comment", comment.node),
  );
  actions.append(save, reload, retire, confirmRetirement, cancelRetirement);
  recovery.append(inspect, retry, dismiss);
  addFields.append(
    legend("Add lane"),
    field("New lane label", newLabel.node),
    field("Insert after", after),
    createGrants.node,
    connectGrants.node,
    add,
  );
  editFields.append(
    legend("Edit lane"),
    field("Lane to edit", target),
    field("Lane label", label.node),
    field("Lane key", key.node),
    field("Lane notes", notes.node),
    approvalFields,
    actions,
    retirementStatus,
  );
  addForm.append(addFields);
  editForm.append(editFields);
  panel.append(summary, addForm, editForm, retiredLanes, recovery, feedback);
  host.append(panel);

  let displayed: BoardViewData | null = null;
  let baseline: Lane | undefined;
  let recoveredLaneId: string | undefined;
  let retirementTag: string | undefined;
  let awaitingTag: string | undefined;
  let notice = "";
  let busy = false;
  let journalKey: string | undefined;
  let raw: string | null = null;
  let pending: Intent | undefined;
  let journalError = false;
  let inspected: { raw: string | null; kind: "retry" | "dismiss" } | undefined;
  let retiredTag: string | undefined;

  function button(text: string, type: "button" | "submit" = "button") {
    const node = doc.createElement("button");
    node.textContent = text;
    node.type = type;
    return node;
  }

  function field(text: string, control: HTMLElement) {
    const node = doc.createElement("label");
    node.append(text, control);
    return node;
  }

  function legend(text: string) {
    const node = doc.createElement("legend");
    node.textContent = text;
    return node;
  }

  function grantControls(text: string) {
    const node = doc.createElement("fieldset");
    node.className = "lane-settings-grants";
    let users: BoardViewData["users"] = [];
    let choices: HTMLInputElement[] = [];
    const selected = () => choices.filter((choice) => choice.checked).map(({ value }) => value);
    return {
      node,
      selected,
      update(next: BoardViewData["users"]) {
        if (
          next.length === users.length &&
          next.every(
            (user, index) => user.id === users[index]?.id && user.label === users[index]?.label,
          )
        )
          return;
        const previous = new Set(selected());
        users = next;
        choices = [];
        node.replaceChildren(legend(text));
        for (const user of users) {
          const input = doc.createElement("input");
          input.type = "checkbox";
          input.value = user.id;
          input.checked = previous.has(user.id);
          choices.push(input);
          node.append(field(user.label, input));
        }
      },
      clear() {
        for (const choice of choices) choice.checked = false;
      },
    };
  }

  function tab() {
    return options.storage ?? globalThis.sessionStorage;
  }

  function readJournal(actor: string | undefined) {
    const nextKey = actor ? `localswim:v2:lane-settings:${actor}:${boardId}` : undefined;
    try {
      const value = nextKey ? tab().getItem(nextKey) : null;
      if (nextKey === journalKey && value === raw && !journalError) return;
      journalKey = nextKey;
      raw = value;
      pending = value === null ? undefined : parseIntent(value, boardId);
      journalError = false;
      inspected = undefined;
      if (pending) notice = "Inspect the saved lane before retrying this change.";
    } catch {
      journalError = true;
      inspected = undefined;
    }
  }

  function remember(next: Intent | undefined, expected = raw) {
    try {
      if (!journalKey || tab().getItem(journalKey) !== expected) throw new Error();
      const value = next ? JSON.stringify(next) : null;
      if (value === null) tab().removeItem(journalKey);
      else tab().setItem(journalKey, value);
      raw = value;
      pending = next;
      inspected = undefined;
      return true;
    } catch {
      journalError = true;
      return false;
    }
  }

  function patch(): LanePatch {
    if (!baseline) return {};
    const value: LanePatch = {};
    if (label.value !== baseline.label) value.label = label.value;
    if (baseline.role !== "completed" && key.value !== baseline.key) value.key = key.value;
    if (notes.value !== (baseline.notes ?? "")) value.notes = notes.value;
    if (baseline.role === "ready_for_review") {
      if (approval.checked !== baseline.approval_enabled) value.approval_enabled = approval.checked;
      if (comment.value !== (baseline.bulk_approval_comment ?? ""))
        value.bulk_approval_comment = comment.value;
    }
    return value;
  }

  function load(lane: Lane | undefined) {
    baseline = lane;
    label.set(lane?.label ?? "");
    key.set(lane?.key ?? "");
    notes.set(lane?.notes ?? "");
    approval.checked = lane?.approval_enabled ?? false;
    comment.set(lane?.bulk_approval_comment ?? "");
    retirementTag = undefined;
  }

  function setLanes(select: HTMLSelectElement, lanes: readonly Lane[], selected: string) {
    const opts = lanes.map((lane) => {
      const option = doc.createElement("option");
      option.value = lane.id;
      option.textContent = lane.label;
      return option;
    });
    select.replaceChildren(...opts);
    if (lanes.some(({ id }) => id === selected)) select.value = selected;
    else if (select === after && displayed) {
      const prompt = doc.createElement("option");
      prompt.value = "";
      prompt.textContent = "Choose a lane";
      select.replaceChildren(prompt, ...opts);
      select.value = "";
    }
  }

  function editProblem() {
    if (!validText(label.value, limits.label, true))
      return "Enter a lane label of up to 256 characters.";
    if (
      baseline?.role !== "completed" &&
      (!validText(key.value, limits.key, true) || key.value === "completed")
    )
      return "Enter a lane key of up to 128 characters. The completed key is reserved.";
    if (!validText(notes.value, limits.notes))
      return `Use up to ${limits.notes} characters for lane notes.`;
    if (!validText(comment.value, limits.bulk_approval_comment))
      return `Use up to ${limits.bulk_approval_comment} characters for the bulk approval comment.`;
    return "";
  }

  function edited() {
    notice = "";
    update();
  }

  async function showRetired(tag: string) {
    try {
      const rows: { id: string; label: string; key: string }[] = [];
      const cursors = new Set<string>();
      let cursor: string | undefined;
      do {
        const { data } = await readWithSession(
          () => api.listLanes(boardId, cursor, signal),
          signal,
        );
        if (signal.aborted || tag !== retiredTag) return;
        const page = data as {
          lanes: { id: string; label: string; key: string; retired: boolean }[];
          next_cursor: unknown;
        };
        const next = page.next_cursor;
        if (next !== null && (typeof next !== "string" || !next || cursors.has(next))) return;
        rows.push(...page.lanes.filter(({ retired }) => retired));
        cursor = next ?? undefined;
        if (cursor) cursors.add(cursor);
      } while (cursor);
      const check = await readWithSession(
        () => api.getBoardIfChanged(boardId, tag, signal),
        signal,
      );
      const context = getContext();
      if (
        signal.aborted ||
        !context.allowed ||
        context.data?.etag !== tag ||
        tag !== retiredTag ||
        check.etag !== tag
      )
        return;
      retiredLanes.replaceChildren(
        ...rows.map(({ label, key }) => {
          const item = doc.createElement("li");
          item.textContent = `${label} [${key}] (retired)`;
          return item;
        }),
      );
      retiredLanes.hidden = rows.length === 0;
    } catch {
      if (!signal.aborted && tag === retiredTag) retiredLanes.hidden = true;
    }
  }

  function update() {
    if (signal.aborted) return;
    const { data, actorId, allowed, writable } = getContext();
    if (!busy && actorId) readJournal(actorId);
    panel.hidden = !allowed || !actorId;
    if (panel.hidden) {
      retiredTag = undefined;
      retiredLanes.hidden = true;
      return;
    }
    const listTag = panel.open ? data?.etag : undefined;
    if (listTag !== retiredTag) {
      retiredTag = listTag;
      retiredLanes.hidden = true;
      if (listTag) void showRetired(listTag);
    }
    if (data && data !== displayed) {
      const lanesChanged =
        data.lanes.length !== displayed?.lanes.length ||
        data.lanes.some((lane, index) => {
          const old = displayed?.lanes[index];
          return lane.id !== old?.id || lane.label !== old.label;
        });
      if (lanesChanged) {
        setLanes(after, data.lanes, after.value);
        setLanes(target, data.lanes, target.value);
      }
      createGrants.update(data.users);
      connectGrants.update(data.users);
      displayed = data;
    }
    if (data && recoveredLaneId && data.lanes.some(({ id }) => id === recoveredLaneId)) {
      target.value = recoveredLaneId;
      recoveredLaneId = undefined;
    }
    const current = data?.lanes.find(({ id }) => id === target.value);
    const dirty = Object.keys(patch()).length > 0;
    if (
      current &&
      (!baseline || !dirty || current.id !== baseline.id) &&
      (current.id !== baseline?.id || current.revision !== baseline.revision)
    )
      load(current);
    const stale =
      !!data &&
      !!baseline &&
      (!current || current.id !== baseline.id || current.revision !== baseline.revision);
    const occupied =
      !!current && data?.cards.some(({ lane_id }) => lane_id === current.id) === true;
    const canRetire = !!current && current.role !== "completed";
    const confirm =
      retirementTag !== undefined &&
      retirementTag === data?.etag &&
      canRetire &&
      !stale &&
      !occupied &&
      !dirty;
    if (!confirm) retirementTag = undefined;
    if (awaitingTag && data && awaitingTag !== data.etag) awaitingTag = undefined;
    const unavailable =
      !data || !writable || busy || !!pending || journalError || awaitingTag !== undefined;
    const problem = dirty && !stale ? editProblem() : "";
    addFields.disabled = unavailable;
    editFields.disabled = unavailable;
    label.node.disabled = notes.node.disabled = !current || stale;
    key.node.disabled = !current || stale || current.role === "completed";
    approvalFields.hidden = current?.role !== "ready_for_review";
    approvalFields.disabled = approvalFields.hidden || stale;
    add.disabled = unavailable || !after.value || !validText(newLabel.value, limits.label, true);
    save.disabled = unavailable || !current || stale || !dirty || !!problem;
    reload.hidden = !stale;
    retire.hidden = !canRetire || confirm;
    retire.disabled = unavailable || stale || dirty || occupied;
    confirmRetirement.hidden = cancelRetirement.hidden = !confirm;
    retirementStatus.textContent =
      canRetire && occupied ? "Move or archive the active cards before retiring this lane." : "";
    inspect.hidden = !pending;
    inspect.disabled = busy || journalError || !canInspectSaved();
    retry.hidden = !pending || inspected?.kind !== "retry";
    retry.disabled = busy || !writable || data?.etag !== pending?.boardEtag || journalError;
    dismiss.hidden = !pending || inspected?.kind !== "dismiss";
    dismiss.disabled = busy || journalError || !canInspectSaved();
    const text = journalError
      ? "The pending lane change could not be read or saved in this tab."
      : notice ||
        (stale ? "The lane changed. Reload its settings before editing." : problem) ||
        (!data || !writable ? "Confirm the current board before changing its lanes." : "");
    if (feedback.textContent !== text) feedback.textContent = text;
  }

  function run(operation: () => Promise<void>) {
    if (busy || signal.aborted) return;
    busy = true;
    update();
    void operation()
      .catch(() => {
        notice = "The lane change could not be checked. Try inspecting again.";
      })
      .finally(() => {
        busy = false;
        update();
      });
  }

  function submit(intent: Intent, repeating = false) {
    const { data, actorId, allowed, writable } = getContext();
    if (!actorId || !allowed || !writable || data?.etag !== intent.boardEtag || awaitingTag || busy)
      return;
    readJournal(actorId);
    if (journalError || (repeating ? inspected?.kind !== "retry" || pending !== intent : !!pending))
      return;
    const release = beginWrite();
    if (!release) return;
    run(async () => {
      try {
        const result = await submitOperation(
          () => remember(intent),
          async () => {
            const reply =
              intent.kind === "add"
                ? await api.createLane(boardId, intent.boardEtag, intent.value)
                : await api.updateLaneSettings(
                    boardId,
                    intent.laneId,
                    intent.revision,
                    intent.boardEtag,
                    intent.value,
                  );
            if (
              reply.etag !== `"${reply.data.id}-${reply.data.revision}"` ||
              reply.data.revision !== data.board.revision + 1 ||
              reply.boardEtag !== `"${boardId}-${reply.data.revision}"`
            ) {
              throw new ApiError("invalid_response", { outcomeUnknown: true });
            }
          },
        );
        switch (result.kind) {
          case "unknown":
            notice = "The lane change may have been saved. Inspect the saved lane before retrying.";
            break;
          case "journal-failed":
            break;
          case "confirmed":
            if (remember(undefined) && !signal.aborted) {
              notice =
                intent.kind === "add"
                  ? "Lane added."
                  : intent.value.retired
                    ? "Lane retired."
                    : "Lane settings saved.";
              if (intent.kind === "add") {
                newLabel.set("");
                createGrants.clear();
                connectGrants.clear();
              } else if (target.value === intent.laneId) load(undefined);
              awaitingTag = intent.boardEtag;
              onChange();
            }
            break;
          case "stale":
          case "refused":
            // An intervening original write can explain a refusal of an inspected retry.
            if (repeating) {
              notice = "The retry was refused. Inspect the original lane change again.";
              if (!signal.aborted) onChange();
              break;
            }
            if (remember(undefined) && !signal.aborted) {
              notice =
                result.kind === "stale"
                  ? "The board or lane changed. Review the current settings before saving again."
                  : intent.kind === "patch" && intent.value.retired
                    ? "The server refused retirement. Check for active cards and review the workflow."
                    : "The server refused the lane change. Check the lane label, key, grants, and workflow.";
              if (result.kind === "stale") awaitingTag = intent.boardEtag;
              onChange();
            }
            break;
        }
      } finally {
        release();
      }
    });
  }

  function inspectOriginal() {
    const { actorId, allowed } = getContext();
    if (!actorId || !allowed || busy || !canInspectSaved()) return;
    readJournal(actorId);
    const intent = pending;
    if (!intent || journalError) return;
    const release = beginInspection();
    if (!release) return;
    run(async () => {
      try {
        const expected = raw;
        inspected = undefined;
        const laneId = intent.kind === "add" ? intent.value.id : intent.laneId;
        const readLane = () =>
          readWithSession(() => api.getLaneSettings(boardId, laneId, signal), signal).catch(
            (error: unknown) => {
              if (error instanceof ApiError && error.status === 404) return undefined;
              throw error;
            },
          );
        const readBoard = () =>
          readWithSession(() => api.getBoardAvailability(boardId, signal), signal);
        const current = () =>
          !signal.aborted &&
          expected === raw &&
          canInspectSaved() &&
          getContext().allowed &&
          getContext().actorId === actorId &&
          tab().getItem(journalKey ?? "") === expected;
        const first = await readBoard();
        if (!current()) return;
        if (first.data.availability !== "open") {
          notice = "Reopen and confirm the board before inspecting the lane.";
          return;
        }
        // Read the exact lane only after observing the board fence, then confirm
        // the board remained stable through the inspection.
        const originalRevision = revisionOf(boardId, intent.boardEtag);
        const lane = await readLane();
        if (!current()) return;
        const validVersion =
          !!lane && lane.revision > originalRevision && lane.revision <= first.data.revision;
        let matched = validVersion && intentMatches(intent, lane);
        const foundOriginal = validVersion && intent.kind === "add";
        if (matched && intent.kind === "add") {
          const view = await readWithSession(() => api.getBoardLanes(boardId, signal), signal);
          const lanes = (view.data as Pick<BoardViewData, "lanes">).lanes;
          const index = lanes.findIndex(({ id }) => id === laneId);
          const policy = await readWithSession(
            () => api.getPolicy(first.data.policy_id, signal),
            signal,
          );
          matched =
            view.etag === first.etag &&
            index > 0 &&
            lanes[index - 1]?.id === intent.value.after_id &&
            matchesAddedGrants(
              policy.data,
              boardId,
              first.data.policy_id,
              intent.value,
              lanes.filter(({ id }) => id !== laneId),
            );
        }
        const last = await readBoard();
        if (!current()) return;
        if (first.etag !== last.etag || last.data.availability !== "open") {
          notice = "The board changed during inspection. Inspect the lane again.";
          return;
        }
        if (lane && (matched || foundOriginal)) {
          if (remember(undefined, expected)) {
            notice =
              intent.kind === "add"
                ? matched
                  ? "The saved board contains the new lane."
                  : `The original lane was found as “${lane.label}”${lane.retired ? " (retired)" : ""}. Its current settings, position, or grants differ from the pending request.`
                : "The saved lane matches the pending change.";
            if (intent.kind === "add") {
              newLabel.set("");
              createGrants.clear();
              connectGrants.clear();
              if (!lane.retired) recoveredLaneId = lane.id;
            } else if (target.value === laneId) load(undefined);
            if (getContext().data?.etag === intent.boardEtag) awaitingTag = intent.boardEtag;
            onChange();
          }
          return;
        }
        const unchanged =
          first.etag === intent.boardEtag &&
          (intent.kind === "add" ? !lane : lane?.revision === intent.revision);
        const advanced =
          first.data.revision > originalRevision && (intent.kind === "patch" || !lane);
        if (unchanged || advanced) {
          inspected = { raw: expected, kind: unchanged ? "retry" : "dismiss" };
          notice = unchanged
            ? "The original lane change is absent. You can retry against the same board version."
            : "The saved board differs from the pending change. Dismiss it and review the current settings.";
        } else notice = "The board and lane versions could not be reconciled. Inspect again.";
        onChange();
      } finally {
        release();
      }
    });
  }

  function selected() {
    const { data } = getContext();
    const lane = data?.lanes.find(({ id }) => id === target.value);
    return data && lane && lane.id === baseline?.id && lane.revision === baseline.revision
      ? { data, lane }
      : undefined;
  }

  addForm.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      const { data } = getContext();
      if (
        !data?.lanes.some(({ id }) => id === after.value) ||
        !validText(newLabel.value, limits.label, true)
      )
        return;
      submit({
        kind: "add",
        boardEtag: data.etag,
        value: {
          id: crypto.randomUUID(),
          label: newLabel.value,
          after_id: after.value,
          create_for: createGrants.selected(),
          connect_for: connectGrants.selected(),
        },
      });
    },
    { signal },
  );
  editForm.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      const current = selected();
      const value = patch();
      if (!current || editProblem() || !Object.keys(value).length) return;
      submit({
        kind: "patch",
        boardEtag: current.data.etag,
        laneId: current.lane.id,
        revision: current.lane.revision,
        value,
      });
    },
    { signal },
  );
  target.addEventListener(
    "change",
    () => {
      recoveredLaneId = undefined;
      load(getContext().data?.lanes.find(({ id }) => id === target.value));
      edited();
    },
    { signal },
  );
  after.addEventListener("change", update, { signal });
  approval.addEventListener("change", edited, { signal });
  reload.addEventListener(
    "click",
    () => {
      load(getContext().data?.lanes.find(({ id }) => id === target.value));
      edited();
    },
    { signal },
  );
  retire.addEventListener(
    "click",
    () => {
      retirementTag = selected()?.data.etag;
      update();
    },
    { signal },
  );
  cancelRetirement.addEventListener(
    "click",
    () => {
      retirementTag = undefined;
      update();
      retire.focus();
    },
    { signal },
  );
  confirmRetirement.addEventListener(
    "click",
    () => {
      const current = selected();
      if (
        !current ||
        current.lane.role === "completed" ||
        retirementTag !== current.data.etag ||
        Object.keys(patch()).length ||
        current.data.cards.some(({ lane_id }) => lane_id === current.lane.id)
      )
        return;
      retirementTag = undefined;
      submit({
        kind: "patch",
        boardEtag: current.data.etag,
        laneId: current.lane.id,
        revision: current.lane.revision,
        value: { retired: true },
      });
    },
    { signal },
  );
  inspect.addEventListener("click", inspectOriginal, { signal });
  panel.addEventListener("toggle", update, { signal });
  retry.addEventListener(
    "click",
    () => {
      if (pending) submit(pending, true);
    },
    { signal },
  );
  dismiss.addEventListener(
    "click",
    () => {
      const { actorId, allowed } = getContext();
      if (!actorId || !allowed || busy || !canInspectSaved()) return;
      readJournal(actorId);
      if (inspected?.kind !== "dismiss" || !remember(undefined, inspected.raw)) return;
      notice = "Pending lane change dismissed. Review the current settings before saving.";
      onChange();
      update();
    },
    { signal },
  );
  update();
  return { update };
}
