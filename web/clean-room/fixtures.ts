import type { CardDto, LaneDto, SessionDto, ServiceDto } from "./api-types.js";
import type { BoardData, ReadState, WorkspaceData } from "./contracts.js";

export interface EntryFixture {
  readonly session: SessionDto;
  readonly workspace: ReadState<WorkspaceData>;
  readonly board: ReadState<BoardData>;
  readonly service: ReadState<ServiceDto>;
}

export type EntryFixtureScenario =
  | "populated"
  | "empty_workspace"
  | "empty_board"
  | "loading"
  | "board_loading"
  | "unavailable"
  | "board_unavailable"
  | "unavailable_retained";
export type FixtureBoard = "alpha" | "beta";

export const entryFixtureBoards = {
  alpha: "58200000-0000-4000-8000-000000000001",
  beta: "58300000-0000-4000-8000-000000000001",
} as const;

// Fresh identities map the Lead's observed disposable V1 recipe into native V2 DTOs.
// Each call returns independent objects.
export function createEntryFixture(
  scenario: EntryFixtureScenario = "populated",
  selectedBoard: FixtureBoard = "alpha",
): EntryFixture {
  const at = "2026-11-13 12:00:00+00:00";
  const prefix = selectedBoard === "alpha" ? "58200000" : "58300000";
  const boardId = entryFixtureBoards[selectedBoard];
  const boardName = selectedBoard === "alpha" ? "Parity Alpha" : "Parity Beta";
  const humanId = "58200000-0000-4000-8000-000000000002";
  const botId = "58200000-0000-4000-8000-000000000003";
  const policyId = `${prefix}-0000-4000-8000-000000000004`;
  const urgentId = `${prefix}-0000-4000-8000-000000000028`;
  const highId = `${prefix}-0000-4000-8000-000000000029`;
  const priorityId = `${prefix}-0000-4000-8000-000000000030`;
  const lowId = `${prefix}-0000-4000-8000-000000000031`;
  const backlogId = `${prefix}-0000-4000-8000-000000000006`;
  const readyId = `${prefix}-0000-4000-8000-000000000007`;
  const progressId = `${prefix}-0000-4000-8000-000000000008`;
  const reviewId = `${prefix}-0000-4000-8000-000000000009`;
  const inputId = `${prefix}-0000-4000-8000-000000000010`;
  const blockedId = `${prefix}-0000-4000-8000-000000000011`;
  const completedId = `${prefix}-0000-4000-8000-000000000012`;
  const publishing = {
    enabled: false,
    repository: null,
    last_success_at: null,
    pending: false,
    error_code: null,
  };
  const alphaPopulated = scenario !== "empty_board";
  const selectedPopulated = selectedBoard === "alpha" && alphaPopulated;
  const attention = {
    blocked: selectedPopulated ? 1 : 0,
    needs_input: selectedPopulated ? 1 : 0,
    ready_for_review: selectedPopulated ? 1 : 0,
  };
  const entityTimes = { revision: 1, created_at: at, updated_at: at };
  const laneSeeds: readonly Pick<LaneDto, "id" | "key" | "label" | "role">[] = [
    { id: backlogId, key: "backlog", label: "Backlog", role: "ordinary" },
    {
      id: readyId,
      key: "ready_for_work",
      label: "Ready For Work",
      role: "ordinary",
    },
    {
      id: progressId,
      key: "in_progress",
      label: "In Progress",
      role: "ordinary",
    },
    {
      id: reviewId,
      key: "ready_for_review",
      label: "Ready For Review",
      role: "ready_for_review",
    },
    {
      id: inputId,
      key: "needs_terry",
      label: "Needs Terry",
      role: "needs_input",
    },
    { id: blockedId, key: "blocked", label: "Blocked", role: "blocked" },
    {
      id: completedId,
      key: "completed",
      label: "Completed",
      role: "completed",
    },
  ];
  const lanes: readonly LaneDto[] = laneSeeds.map((seed, position) => ({
    ...entityTimes,
    ...seed,
    board_id: boardId,
    position,
    retired: false,
    approval_enabled: selectedBoard === "alpha" && seed.id === reviewId,
    reorder_enabled: selectedBoard === "alpha" && seed.id === backlogId,
    notes: `Fixture lane ${seed.label}`,
    ...(selectedBoard === "alpha" && seed.id === reviewId
      ? { bulk_approval_comment: "Fixture reviewed and approved" }
      : {}),
  }));
  const priorities = [
    {
      ...entityTimes,
      id: urgentId,
      board_id: boardId,
      key: "P1",
      label: "Urgent",
      rank: 1,
      color: "",
    },
    {
      ...entityTimes,
      id: highId,
      board_id: boardId,
      key: "P2",
      label: "High",
      rank: 2,
      color: "",
    },
    {
      ...entityTimes,
      id: priorityId,
      board_id: boardId,
      key: "P3",
      label: "Normal",
      rank: 3,
      color: "",
    },
    {
      ...entityTimes,
      id: lowId,
      board_id: boardId,
      key: "P4",
      label: "Low",
      rank: 4,
      color: "",
    },
  ];
  const initialCards: readonly Pick<CardDto, "title" | "lane_id" | "priority_id">[] = [
    {
      title: "Review the release checklist",
      lane_id: backlogId,
      priority_id: urgentId,
    },
    {
      title: "Prepare the next demonstration",
      lane_id: readyId,
      priority_id: highId,
    },
    {
      title: "Match the selected board",
      lane_id: progressId,
      priority_id: priorityId,
    },
    {
      title: "Verify the keyboard workflow",
      lane_id: reviewId,
      priority_id: lowId,
    },
    {
      title: "Confirm the fixture requirements",
      lane_id: inputId,
      priority_id: priorityId,
    },
    {
      title: "Wait for sample material",
      lane_id: blockedId,
      priority_id: highId,
    },
  ];
  const cardSeeds = [
    ...initialCards,
    ...Array.from({ length: 17 }, (_, index) => ({
      title: `Backlog fixture ${index + 7} with a longer wrapping title`,
      lane_id: backlogId,
      priority_id: priorityId,
    })),
  ];
  const cards: readonly CardDto[] = selectedPopulated
    ? cardSeeds.map((seed, index) => ({
        ...entityTimes,
        ...seed,
        id: `${prefix}-0000-4000-8000-${String(index + 101).padStart(12, "0")}`,
        board_id: boardId,
        ticket: index + 1,
        description: index < 6 ? "Disposable parity fixture. Preserve this exact text." : "",
        owner_id: botId,
        parent_id: null,
        child_ids: [],
        archived: false,
        cover_attachment_id: null,
        comment_count: index === 0 ? 2 : 0,
        attachment_count: 0,
        blocking_depth: 0,
        blocked_card_count: 0,
        triage_score: 0,
        completed_at: null,
      }))
    : [];
  const actors = [humanId, botId];
  const creationLanes = selectedBoard === "alpha" ? lanes : lanes.slice(0, 2);
  const createGrants = actors.flatMap((userId) =>
    creationLanes.map((lane) => ({ user_id: userId, lane_id: lane.id })),
  );
  const moveGrants = actors.flatMap((userId) =>
    lanes.flatMap((fromLane) =>
      lanes
        .filter(
          (toLane) =>
            toLane.id !== fromLane.id && (userId === humanId || toLane.id !== completedId),
        )
        .map((toLane) => ({
          user_id: userId,
          from_lane_id: fromLane.id,
          to_lane_id: toLane.id,
        })),
    ),
  );
  const board: BoardData = {
    boardEtag: `"fixture-board-${selectedBoard}-1"`,
    view: {
      board: {
        ...entityTimes,
        id: boardId,
        name: boardName,
        policy_id: policyId,
        user_ids: [humanId, botId],
        default_owner_id: botId,
        lane_ids: lanes.map((lane) => lane.id),
        priority_ids: priorities.map((priority) => priority.id),
        availability: "open",
        attention,
        last_card_edit_at: cards.length === 0 ? null : at,
        publishing,
        user_css: "",
      },
      lanes,
      cards,
    },
    users: [
      {
        id: humanId,
        revision: 1,
        created_at: at,
        label: "Terry",
        kind: "human",
        color: "#2266AA",
      },
      {
        id: botId,
        revision: 1,
        created_at: at,
        label: "Bot",
        kind: "bot",
        color: "#AA6622",
      },
    ],
    priorities,
    policy: {
      ...entityTimes,
      id: policyId,
      board_id: boardId,
      default_priority_id: priorityId,
      create_grants: createGrants,
      move_grants: moveGrants,
      resource_grants: [
        {
          user_id: humanId,
          capabilities: [
            "read",
            "create_card",
            "edit_card",
            "approve",
            ...(selectedBoard === "alpha" ? ["reorder_tickets" as const] : []),
          ],
        },
        { user_id: botId, capabilities: ["read", "create_card", "edit_card"] },
      ],
    },
  };
  const workspace: WorkspaceData = {
    workspace: {
      revision: 1,
      pinned_board_ids: [],
      publishing,
    },
    workspaceEtag: '"fixture-workspace-1"',
    boards:
      scenario === "empty_workspace"
        ? []
        : (["alpha", "beta"] as const).map((name) => ({
            id: entryFixtureBoards[name],
            name: name === "alpha" ? "Parity Alpha" : "Parity Beta",
            revision: 1,
            availability: "open" as const,
            attention: {
              blocked: name === "alpha" && alphaPopulated ? 1 : 0,
              needs_input: name === "alpha" && alphaPopulated ? 1 : 0,
              ready_for_review: name === "alpha" && alphaPopulated ? 1 : 0,
            },
            last_card_edit_at: name === "alpha" && alphaPopulated ? at : null,
            error_code: null,
          })),
  };
  const service: ServiceDto = {
    port: 53158,
    service_instance_id: "019a7dbc-9200-7000-8000-000000000001",
    build_id: "58200000-0000-4000-8000-000000000099",
    state: "running",
    api_version: "2026-11-10",
    restart_error_code: null,
  };
  function readState<T>(value: T, isBoard = false): ReadState<T> {
    const current = { value, confirmedAt: at };
    if (scenario === "loading" || (isBoard && scenario === "board_loading")) {
      return { status: "loading", previous: null };
    }
    if (
      scenario === "unavailable" ||
      scenario === "unavailable_retained" ||
      (isBoard && scenario === "board_unavailable")
    ) {
      return {
        status: "unavailable",
        previous: scenario === "unavailable_retained" ? current : null,
        failure: { kind: "transport" },
      };
    }
    return { status: "ready", current };
  }
  return {
    session: {
      principal_id: humanId,
      kind: "human",
      api_version: "2026-11-10",
      workspace_admin: true,
    },
    workspace: readState(workspace),
    board: readState(board, true),
    service: readState(service),
  };
}
