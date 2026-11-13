import type {
  CardDto,
  EntityTag,
  LaneDto,
  SessionDto,
  Timestamp,
  Uuid,
  UuidV7,
} from "../api-types.js";
import type { BoardData } from "../contracts.js";
import {
  apiPrefix,
  entityTag,
  invalidResponse,
  isRecord,
  isTimestamp,
  isUnknownArray,
  isUuid,
  mutateJson,
  readJson,
} from "../http.js";

const defaultComment = "Reviewed & approved, moving to Completed.";
const maxApprovalCards = 5000;
const cardReadBatchSize = 8;

interface ApprovalContext {
  readonly data: BoardData;
  readonly session: SessionDto;
}

type ApprovalStatus = "pending" | "unknown" | "checking" | "failed" | "committed";

interface ApprovalWork {
  readonly scope: ApprovalScope;
  status: ApprovalStatus;
  intent: ApprovalIntent | null;
  message: string;
  receipt?: ConfirmedApproval;
}

export interface ConfirmedApproval {
  readonly baseTag: EntityTag;
  readonly revision: number;
  readonly at: Timestamp;
  readonly fromLaneId: Uuid;
  readonly toLaneId: Uuid;
  readonly cardIds: readonly Uuid[];
}

interface ApprovalCallbacks {
  readonly current: () => ApprovalContext | null;
  readonly selectedBoard: () => Uuid | null;
  readonly refresh: (boardId: Uuid, receipt?: ConfirmedApproval) => void;
  readonly onStateChange: () => void;
}

// Keep uncertain submissions available when the user leaves and reopens a board.
const workByLane = new Map<Uuid, ApprovalWork>();
const workUpdates = new Set<(work: ApprovalWork) => void>();

interface ApprovalScope {
  readonly lane: LaneDto;
  readonly completed: LaneDto;
  readonly cards: readonly CardVersion[];
  readonly boardTag: EntityTag;
  readonly boardRevision: number;
  readonly actorId: Uuid;
  readonly comment: string;
}

type CardVersion = Pick<CardDto, "id" | "board_id" | "lane_id" | "revision">;

interface ApprovalIntent {
  readonly scope: ApprovalScope;
  readonly requestId: Uuid;
  readonly body: {
    readonly id: UuidV7;
    readonly board_id: Uuid;
    readonly expected_board_etag: EntityTag;
    readonly lane_id: Uuid;
    readonly cards: readonly { readonly card_id: Uuid; readonly etag: EntityTag }[];
  };
}

function sourceLane(data: BoardData, laneId: Uuid): LaneDto | undefined {
  return data.view.lanes.find(
    (lane) =>
      lane.id === laneId &&
      lane.role === "ready_for_review" &&
      !lane.retired &&
      lane.approval_enabled,
  );
}

function completedLane(data: BoardData): LaneDto | undefined {
  return data.view.lanes.find((lane) => lane.role === "completed" && !lane.retired);
}

function canApprove(data: BoardData, session: SessionDto | null): boolean {
  return (
    session?.kind === "human" &&
    data.view.board.user_ids.includes(session.principal_id) &&
    data.policy.resource_grants.some(
      (grant) => grant.user_id === session.principal_id && grant.capabilities.includes("approve"),
    )
  );
}

export function laneApprovalAvailability(
  data: BoardData,
  session: SessionDto | null,
  laneId: Uuid,
) {
  const count = data.view.cards.filter((card) => card.lane_id === laneId && !card.archived).length;
  if (sourceLane(data, laneId) === undefined || !canApprove(data, session)) {
    return { visible: false, enabled: false, count };
  }
  const completed = completedLane(data);
  const enabled =
    completed !== undefined &&
    data.view.board.availability === "open" &&
    count > 0 &&
    count <= maxApprovalCards &&
    data.policy.move_grants.some(
      (grant) =>
        grant.user_id === session?.principal_id &&
        grant.from_lane_id === laneId &&
        grant.to_lane_id === completed.id,
    );
  return { visible: true, enabled, count };
}

function approvalScope({ data, session }: ApprovalContext, laneId: Uuid): ApprovalScope | null {
  const lane = sourceLane(data, laneId);
  const completed = completedLane(data);
  if (
    lane === undefined ||
    completed === undefined ||
    !laneApprovalAvailability(data, session, laneId).enabled
  ) {
    return null;
  }
  return {
    lane,
    completed,
    cards: data.view.cards
      .filter((card) => card.lane_id === laneId && !card.archived)
      .map(({ id, board_id, lane_id, revision }) => ({ id, board_id, lane_id, revision })),
    boardTag: data.boardEtag,
    boardRevision: data.view.board.revision,
    actorId: session.principal_id,
    comment: lane.bulk_approval_comment || defaultComment,
  };
}

function newApprovalId(): UuidV7 {
  const time = Date.now().toString(16).padStart(12, "0");
  const random = crypto.randomUUID();
  return `${time.slice(0, 8)}-${time.slice(8)}-7${random.slice(15)}`;
}

function isUuidV7(value: unknown): value is UuidV7 {
  return (
    typeof value === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(value)
  );
}

async function cardVersion(card: CardVersion, signal: AbortSignal) {
  const { data, headers } = await readJson(`/cards/${card.id}`, signal);
  if (
    !isRecord(data) ||
    data.id !== card.id ||
    data.board_id !== card.board_id ||
    data.lane_id !== card.lane_id ||
    data.revision !== card.revision ||
    data.archived !== false
  ) {
    return invalidResponse();
  }
  return { card_id: card.id, etag: entityTag(headers) };
}

async function prepareApproval(scope: ApprovalScope, signal: AbortSignal): Promise<ApprovalIntent> {
  const cards: ApprovalIntent["body"]["cards"][number][] = [];
  for (let index = 0; index < scope.cards.length; index += cardReadBatchSize) {
    const batch = scope.cards.slice(index, index + cardReadBatchSize);
    cards.push(...(await Promise.all(batch.map((card) => cardVersion(card, signal)))));
  }
  return {
    scope,
    requestId: crypto.randomUUID(),
    body: {
      id: newApprovalId(),
      board_id: scope.lane.board_id,
      expected_board_etag: scope.boardTag,
      lane_id: scope.lane.id,
      cards,
    },
  };
}

function validateReceipt(intent: ApprovalIntent, value: unknown): ConfirmedApproval {
  const { scope, body } = intent;
  if (
    !isRecord(value) ||
    Object.keys(value).length !== 7 ||
    value.id !== body.id ||
    value.board_id !== body.board_id ||
    value.actor_id !== scope.actorId ||
    value.board_revision !== scope.boardRevision + 1 ||
    !Number.isSafeInteger(value.board_revision) ||
    !isTimestamp(value.at) ||
    !isUnknownArray(value.card_ids) ||
    value.card_ids.length !== body.cards.length ||
    !isUnknownArray(value.changes) ||
    value.changes.length !== body.cards.length * 2
  ) {
    return invalidResponse();
  }
  const changeIds = new Set<UuidV7>();
  const commentIds = new Set<Uuid>();
  for (const [index, card] of body.cards.entries()) {
    const move = value.changes[index * 2];
    const comment = value.changes[index * 2 + 1];
    if (
      value.card_ids[index] !== card.card_id ||
      !isRecord(move) ||
      !isRecord(comment) ||
      Object.keys(move).length !== (index === 0 ? 12 : 11) ||
      Object.keys(comment).length !== 11 ||
      move.operation !== "approve" ||
      move.resource_type !== "card" ||
      move.resource_id !== card.card_id ||
      move.field !== "lane_id" ||
      move.before !== body.lane_id ||
      move.after !== scope.completed.id ||
      (index === 0 && move.id !== body.id) ||
      move.bulk_approval_comment !== (index === 0 ? scope.comment : undefined) ||
      comment.operation !== "comment" ||
      comment.resource_type !== "comment" ||
      !isUuid(comment.resource_id) ||
      commentIds.has(comment.resource_id) ||
      comment.field !== null ||
      comment.before !== null ||
      comment.after !== null
    ) {
      return invalidResponse();
    }
    commentIds.add(comment.resource_id);
    for (const change of [move, comment]) {
      if (
        !isUuidV7(change.id) ||
        changeIds.has(change.id) ||
        change.board_id !== value.board_id ||
        change.actor_id !== value.actor_id ||
        change.revision !== value.board_revision ||
        change.at !== value.at
      ) {
        return invalidResponse();
      }
      changeIds.add(change.id);
    }
  }
  return {
    baseTag: scope.boardTag,
    revision: scope.boardRevision + 1,
    at: value.at,
    fromLaneId: body.lane_id,
    toLaneId: scope.completed.id,
    cardIds: body.cards.map(({ card_id }) => card_id),
  };
}

function submitApproval(intent: ApprovalIntent, beforeDispatch: (session: SessionDto) => void) {
  return mutateJson("/approvals", {
    method: "POST",
    successStatus: 201,
    requestId: intent.requestId,
    body: intent.body,
    beforeDispatch,
    validate(data, headers) {
      const receipt = validateReceipt(intent, data);
      if (
        entityTag(headers, "X-Board-ETag") === intent.scope.boardTag ||
        headers.get("Location") !== `${apiPrefix}/approvals/${intent.body.id}`
      ) {
        return invalidResponse();
      }
      return receipt;
    },
  });
}

async function inspectApproval(intent: ApprovalIntent): Promise<ConfirmedApproval | null> {
  try {
    const { data, headers } = await readJson(`/approvals/${intent.body.id}`);
    entityTag(headers);
    return validateReceipt(intent, data);
  } catch {
    // A missing receipt can race an in-flight writer, so it also leaves the outcome unknown.
    return null;
  }
}

function setStatus(work: ApprovalWork, status: ApprovalStatus, message = ""): void {
  work.status = status;
  work.message = message;
  if (status === "committed" || status === "failed") workByLane.delete(work.scope.lane.id);
  for (const listener of workUpdates) listener(work);
}

async function checkWork(work: ApprovalWork, intent: ApprovalIntent): Promise<void> {
  setStatus(work, "checking", "Checking approval status…");
  const receipt = await inspectApproval(intent);
  if (receipt !== null) {
    work.receipt = receipt;
    setStatus(work, "committed");
  } else {
    setStatus(
      work,
      "unknown",
      "Approval could not be confirmed. Check its status before trying again.",
    );
  }
}

async function runApproval(
  work: ApprovalWork,
  current: ApprovalCallbacks["current"],
): Promise<void> {
  let intent: ApprovalIntent;
  try {
    intent = await prepareApproval(work.scope, AbortSignal.timeout(60_000));
  } catch {
    setStatus(work, "failed", "The cards could not be verified. Refresh the board and try again.");
    return;
  }
  work.intent = intent;
  let dispatching = false;
  const result = await submitApproval(intent, (session) => {
    const context = current();
    if (
      context === null ||
      context.data.view.board.id !== intent.body.board_id ||
      context.data.boardEtag !== intent.body.expected_board_etag ||
      context.data.view.board.revision !== intent.scope.boardRevision ||
      context.session.principal_id !== intent.scope.actorId ||
      session.principal_id !== intent.scope.actorId ||
      !laneApprovalAvailability(context.data, context.session, intent.body.lane_id).enabled
    ) {
      return invalidResponse();
    }
    dispatching = true;
  });
  if (!dispatching) {
    setStatus(
      work,
      "failed",
      "The board could not be verified. Review the updated lane before approving.",
    );
    return;
  }
  switch (result.outcome) {
    case "committed":
      work.receipt = result.value;
      setStatus(work, "committed");
      return;
    case "not_applied": {
      const stale =
        result.problem.code === "conflict" || result.problem.code === "precondition_failed";
      setStatus(
        work,
        "failed",
        stale
          ? "The board changed. Review the updated lane before approving."
          : "Lane approval failed. Refresh the board and try again.",
      );
      return;
    }
    case "unknown":
      await checkWork(work, intent);
  }
}

export function createLaneApproval(host: HTMLElement | ShadowRoot, callbacks: ApprovalCallbacks) {
  const status = document.createElement("p");
  status.className = "lsw-approve-lane-status";
  status.setAttribute("role", "status");
  host.append(status);
  let noticeBoard: Uuid | null = null;
  let hideStatus = 0;
  let disposed = false;

  function onChange(work: ApprovalWork): void {
    const id = work.scope.lane.board_id;
    if (callbacks.selectedBoard() !== id) return;
    clearTimeout(hideStatus);
    noticeBoard = id;
    status.dataset.outcome = work.status;
    status.textContent =
      work.status === "committed"
        ? `Approved ${work.scope.cards.length} review card(s), moving to Completed.`
        : work.message;
    if (work.status === "committed" || work.status === "failed" || work.status === "unknown") {
      callbacks.refresh(id, work.receipt);
    }
    if (work.status === "committed" || work.status === "failed") {
      hideStatus = window.setTimeout(() => {
        status.textContent = "";
      }, 5000);
    }
    callbacks.onStateChange();
  }

  workUpdates.add(onChange);
  return {
    open(laneId: Uuid): void {
      const current = callbacks.current();
      if (current === null) return;
      const outstanding = workByLane.get(laneId);
      if (outstanding !== undefined) {
        if (
          outstanding.status === "unknown" &&
          outstanding.intent !== null &&
          outstanding.scope.lane.board_id === current.data.view.board.id &&
          outstanding.scope.actorId === current.session.principal_id
        ) {
          void checkWork(outstanding, outstanding.intent);
        }
        return;
      }
      const scope = approvalScope(current, laneId);
      if (scope === null) return;
      const work: ApprovalWork = { scope, status: "pending", intent: null, message: "" };
      workByLane.set(laneId, work);
      setStatus(work, "pending", "Approving review cards…");
      void runApproval(work, () => (disposed ? null : callbacks.current()));
    },
    busy(laneId: Uuid): boolean {
      const state = workByLane.get(laneId)?.status;
      return state === "pending" || state === "checking";
    },
    updateSelection(): void {
      if (noticeBoard === callbacks.selectedBoard()) return;
      clearTimeout(hideStatus);
      noticeBoard = null;
      status.textContent = "";
    },
    awaitsConfirmation(boardId: Uuid): boolean {
      return Array.from(workByLane.values()).some(
        ({ scope, status }) =>
          scope.lane.board_id === boardId && (status === "pending" || status === "checking"),
      );
    },
    recoverable(laneId: Uuid): boolean {
      return workByLane.get(laneId)?.status === "unknown";
    },
    dispose(): void {
      disposed = true;
      workUpdates.delete(onChange);
      clearTimeout(hideStatus);
      status.remove();
    },
  };
}
