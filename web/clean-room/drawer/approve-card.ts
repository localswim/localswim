import type { CardDto, SessionDto, Uuid, UuidV7 } from "../api-types.js";
import type { ConfirmedApproval } from "../board/approve-lane.js";
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
import type { DrawerData } from "./contracts.js";

interface BoardContext {
  readonly board: BoardData;
  readonly session: SessionDto;
}

interface DrawerContext extends BoardContext {
  readonly drawer: DrawerData;
}

interface ApprovalIntent {
  readonly requestId: Uuid;
  readonly actorId: Uuid;
  readonly source: CardDto;
  readonly toLaneId: Uuid;
  readonly baseRevision: number;
  readonly body: {
    readonly id: UuidV7;
    readonly board_id: Uuid;
    readonly expected_board_etag: DrawerData["boardEtag"];
    readonly cards: readonly [{ readonly card_id: Uuid; readonly etag: DrawerData["cardEtag"] }];
  };
}

export interface CardApprovalState {
  readonly status: "pending" | "checking" | "unknown" | "failed" | "committed";
  readonly cardId: Uuid;
  readonly actorId: Uuid;
  readonly message: string;
  readonly receipt?: ConfirmedApproval;
}

interface ApprovalCallbacks {
  readonly current: () => DrawerContext | null;
  readonly currentBoard: () => BoardContext | null;
  readonly currentActor: () => Uuid | null;
  readonly onStart: () => void;
  readonly onStateChange: (boardId: Uuid, state: CardApprovalState) => void;
}

interface ApprovalWork {
  readonly intent: ApprovalIntent;
  status: CardApprovalState["status"];
}

const workByCard = new Map<Uuid, ApprovalWork>();
const listeners = new Set<ApprovalCallbacks["onStateChange"]>();

function eligibleDestination(board: BoardData, session: SessionDto | null, card: CardDto) {
  const { board: summary, lanes } = board.view;
  if (
    card.board_id !== summary.id ||
    card.archived ||
    summary.availability !== "open" ||
    session?.kind !== "human" ||
    !summary.user_ids.includes(session.principal_id)
  ) {
    return undefined;
  }
  const source = lanes.find((lane) => lane.id === card.lane_id);
  const target = lanes.find((lane) => lane.role === "completed" && !lane.retired);
  if (source?.role !== "ready_for_review" || source.retired || target === undefined) {
    return undefined;
  }
  const actor = session.principal_id;
  const canApprove = board.policy.resource_grants.some(
    (grant) => grant.user_id === actor && grant.capabilities.includes("approve"),
  );
  const canMove = board.policy.move_grants.some(
    (grant) =>
      grant.user_id === actor && grant.from_lane_id === source.id && grant.to_lane_id === target.id,
  );
  return canApprove && canMove ? target.id : undefined;
}

function matchesRenderedCard(board: BoardData, card: CardDto): boolean {
  return board.view.cards.some(
    (rendered) =>
      rendered.id === card.id &&
      rendered.board_id === card.board_id &&
      rendered.lane_id === card.lane_id &&
      rendered.revision === card.revision &&
      rendered.archived === card.archived,
  );
}

function matchesDrawer(board: BoardData, drawer: DrawerData): boolean {
  return (
    board.boardEtag === drawer.boardEtag &&
    board.view.board.revision === drawer.boardRevision &&
    matchesRenderedCard(board, drawer.card)
  );
}

export function cardApprovalAvailability(
  board: BoardData,
  session: SessionDto | null,
  drawer: DrawerData,
): { readonly visible: boolean; readonly enabled: boolean } {
  const visible = eligibleDestination(board, session, drawer.card) !== undefined;
  return {
    visible,
    enabled: visible && matchesDrawer(board, drawer) && !workByCard.has(drawer.card.id),
  };
}

function newApprovalId(): UuidV7 {
  const time = Date.now().toString(16).padStart(12, "0");
  const random = crypto.randomUUID();
  return `${time.slice(0, 8)}-${time.slice(8)}-7${random.slice(15)}`;
}

function approvalIntent({ board, session, drawer }: DrawerContext): ApprovalIntent | null {
  const toLaneId = eligibleDestination(board, session, drawer.card);
  if (toLaneId === undefined || !matchesDrawer(board, drawer)) return null;
  return {
    requestId: crypto.randomUUID(),
    actorId: session.principal_id,
    source: drawer.card,
    toLaneId,
    baseRevision: drawer.boardRevision,
    body: {
      id: newApprovalId(),
      board_id: drawer.card.board_id,
      expected_board_etag: drawer.boardEtag,
      cards: [{ card_id: drawer.card.id, etag: drawer.cardEtag }],
    },
  };
}

function receiptChange(change: unknown, intent: ApprovalIntent, at: string) {
  if (
    !isRecord(change) ||
    Object.keys(change).length !== 11 ||
    change.board_id !== intent.body.board_id ||
    change.actor_id !== intent.actorId ||
    change.revision !== intent.baseRevision + 1 ||
    change.at !== at
  ) {
    return invalidResponse();
  }
  return change;
}

function validateReceipt(intent: ApprovalIntent, data: unknown): ConfirmedApproval {
  const { body, source, toLaneId } = intent;
  if (
    !isRecord(data) ||
    Object.keys(data).length !== 7 ||
    data.id !== body.id ||
    data.board_id !== body.board_id ||
    data.actor_id !== intent.actorId ||
    !isTimestamp(data.at) ||
    data.board_revision !== intent.baseRevision + 1 ||
    !Number.isSafeInteger(data.board_revision) ||
    !isUnknownArray(data.card_ids) ||
    data.card_ids.length !== 1 ||
    data.card_ids[0] !== source.id ||
    !isUnknownArray(data.changes) ||
    data.changes.length !== 2
  ) {
    return invalidResponse();
  }
  const movement = receiptChange(data.changes[0], intent, data.at);
  const comment = receiptChange(data.changes[1], intent, data.at);
  if (
    movement.id !== body.id ||
    movement.operation !== "approve" ||
    movement.resource_type !== "card" ||
    movement.resource_id !== source.id ||
    movement.field !== "lane_id" ||
    movement.before !== source.lane_id ||
    movement.after !== toLaneId ||
    typeof comment.id !== "string" ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(comment.id) ||
    comment.id === movement.id ||
    comment.operation !== "comment" ||
    comment.resource_type !== "comment" ||
    !isUuid(comment.resource_id) ||
    comment.field !== null ||
    comment.before !== null ||
    comment.after !== null
  ) {
    return invalidResponse();
  }
  return {
    baseTag: body.expected_board_etag,
    revision: intent.baseRevision + 1,
    at: data.at,
    fromLaneId: source.lane_id,
    toLaneId,
    cardIds: [source.id],
  };
}

function setState(
  work: ApprovalWork,
  status: CardApprovalState["status"],
  message: string,
  receipt?: ConfirmedApproval,
): void {
  const { source } = work.intent;
  work.status = status;
  if (status === "failed" || status === "committed") workByCard.delete(source.id);
  for (const listener of listeners) {
    listener(source.board_id, {
      status,
      cardId: source.id,
      actorId: work.intent.actorId,
      message,
      receipt,
    });
  }
}

async function inspectReceipt(intent: ApprovalIntent): Promise<ConfirmedApproval | null> {
  try {
    const { data, headers } = await readJson(`/approvals/${intent.body.id}`);
    entityTag(headers);
    return validateReceipt(intent, data);
  } catch {
    // An absent receipt may race the writer. Keep the original operation for another check.
    return null;
  }
}

async function recover(work: ApprovalWork): Promise<void> {
  setState(work, "checking", "Checking approval status…");
  const receipt = await inspectReceipt(work.intent);
  if (receipt === null) {
    setState(
      work,
      "unknown",
      "Approval could not be confirmed. Check its status before trying again.",
    );
  } else {
    setState(work, "committed", "Reviewed & approved, moving to Completed.", receipt);
  }
}

function matchesIntent(intent: ApprovalIntent, context: BoardContext | null): boolean {
  return (
    context !== null &&
    context.board.boardEtag === intent.body.expected_board_etag &&
    context.board.view.board.revision === intent.baseRevision &&
    context.session.principal_id === intent.actorId &&
    matchesRenderedCard(context.board, intent.source) &&
    eligibleDestination(context.board, context.session, intent.source) === intent.toLaneId
  );
}

async function submit(
  work: ApprovalWork,
  currentBoard: ApprovalCallbacks["currentBoard"],
): Promise<void> {
  const { intent } = work;
  let dispatched = false;
  const result = await mutateJson("/approvals", {
    method: "POST",
    successStatus: 201,
    requestId: intent.requestId,
    body: intent.body,
    beforeDispatch(session) {
      if (
        session.kind !== "human" ||
        session.principal_id !== intent.actorId ||
        !matchesIntent(intent, currentBoard())
      ) {
        return invalidResponse();
      }
      dispatched = true;
    },
    validate(data, headers) {
      if (
        entityTag(headers, "X-Board-ETag") === intent.body.expected_board_etag ||
        headers.get("Location") !== `${apiPrefix}/approvals/${intent.body.id}`
      ) {
        return invalidResponse();
      }
      return validateReceipt(intent, data);
    },
  });
  if (!dispatched) {
    setState(work, "failed", "The board could not be verified. Reopen the card before approving.");
    return;
  }
  switch (result.outcome) {
    case "committed":
      setState(work, "committed", "Reviewed & approved, moving to Completed.", result.value);
      return;
    case "not_applied": {
      const stale =
        result.problem.code === "conflict" || result.problem.code === "precondition_failed";
      setState(
        work,
        "failed",
        stale
          ? "The board changed. Reopen the card before approving."
          : "The card could not be approved.",
      );
      return;
    }
    case "unknown":
      await recover(work);
  }
}

export function createCardApproval(callbacks: ApprovalCallbacks) {
  let disposed = false;
  function notify(boardId: Uuid, state: CardApprovalState): void {
    callbacks.onStateChange(boardId, state);
  }
  listeners.add(notify);
  return {
    approve(): void {
      const current = callbacks.current();
      if (disposed || current === null || this.awaitsConfirmation(current.drawer.card.board_id))
        return;
      const intent = approvalIntent(current);
      if (intent === null) return;
      const work: ApprovalWork = { intent, status: "pending" };
      workByCard.set(intent.source.id, work);
      callbacks.onStart();
      setState(work, "pending", "Approving card…");
      void submit(work, () => (disposed ? null : callbacks.currentBoard()));
    },
    checkStatus(boardId: Uuid, cardId: Uuid): void {
      const work = workByCard.get(cardId);
      if (
        disposed ||
        work?.status !== "unknown" ||
        work.intent.source.board_id !== boardId ||
        callbacks.currentActor() !== work.intent.actorId
      ) {
        return;
      }
      void recover(work);
    },
    awaitsConfirmation(boardId: Uuid): boolean {
      for (const { intent, status } of workByCard.values()) {
        if (
          intent.source.board_id === boardId &&
          intent.actorId === callbacks.currentActor() &&
          (status === "pending" || status === "checking" || status === "unknown")
        ) {
          return true;
        }
      }
      return false;
    },
    recoverable(boardId: Uuid, cardId: Uuid): boolean {
      const work = workByCard.get(cardId);
      return work?.intent.source.board_id === boardId && work.status === "unknown";
    },
    dispose(): void {
      disposed = true;
      listeners.delete(notify);
    },
  };
}
