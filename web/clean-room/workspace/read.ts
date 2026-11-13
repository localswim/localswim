import type {
  AttentionDto,
  BoardSummaryDto,
  BoardSummaryPageDto,
  PublishingDto,
  ServiceDto,
  WorkspaceDto,
} from "../api-types.js";
import type { WorkspaceData } from "../contracts.js";
import {
  entityTag,
  invalidResponse,
  isBoundedText,
  isRecord,
  isTimestamp,
  isUnknownArray,
  isUuid,
  readDeadline,
  readJson,
} from "../http.js";

export { readFailure, readSession } from "../http.js";

const catalogPageSize = 200;
const catalogPageLimit = 100;
const workspacePinLimit = 1000;

function isInteger(
  value: unknown,
  minimum: number,
  maximum = Number.MAX_SAFE_INTEGER,
): value is number {
  return (
    typeof value === "number" && Number.isSafeInteger(value) && value >= minimum && value <= maximum
  );
}

function isPublishing(value: unknown): value is PublishingDto {
  return (
    isRecord(value) &&
    Object.keys(value).length === 5 &&
    typeof value.enabled === "boolean" &&
    (value.repository === null || isBoundedText(value.repository, 0, 2048)) &&
    (value.last_success_at === null || isTimestamp(value.last_success_at)) &&
    typeof value.pending === "boolean" &&
    (value.error_code === null || isBoundedText(value.error_code, 0, 80))
  );
}

function workspaceDto(value: unknown): WorkspaceDto {
  if (
    !isRecord(value) ||
    Object.keys(value).length !== 3 ||
    !isInteger(value.revision, 1) ||
    !isUnknownArray(value.pinned_board_ids) ||
    value.pinned_board_ids.length > workspacePinLimit ||
    !value.pinned_board_ids.every(isUuid) ||
    new Set(value.pinned_board_ids).size !== value.pinned_board_ids.length ||
    !isPublishing(value.publishing)
  ) {
    return invalidResponse();
  }
  return {
    revision: value.revision,
    pinned_board_ids: value.pinned_board_ids,
    publishing: value.publishing,
  };
}

function isAttention(value: unknown): value is AttentionDto {
  return (
    isRecord(value) &&
    Object.keys(value).length === 3 &&
    isInteger(value.blocked, 0) &&
    isInteger(value.needs_input, 0) &&
    isInteger(value.ready_for_review, 0)
  );
}

function isBoardSummary(value: unknown): value is BoardSummaryDto {
  if (
    !isRecord(value) ||
    Object.keys(value).length !== 7 ||
    !isUuid(value.id) ||
    (value.name !== null && !isBoundedText(value.name, 1, 256)) ||
    (value.revision !== null && !isInteger(value.revision, 1)) ||
    (value.attention !== null && !isAttention(value.attention)) ||
    (value.last_card_edit_at !== null && !isTimestamp(value.last_card_edit_at)) ||
    (value.error_code !== null && !isBoundedText(value.error_code, 0, 80))
  ) {
    return false;
  }
  const namedRevision = value.name !== null && value.revision !== null;
  const noAttention = value.attention === null && value.last_card_edit_at === null;
  switch (value.availability) {
    case "open":
      return namedRevision && value.attention !== null && value.error_code === null;
    case "closed":
      return namedRevision && noAttention && value.error_code === null;
    case "unhealthy":
      return (
        noAttention &&
        value.error_code !== null &&
        (namedRevision ||
          (value.name === null &&
            value.revision === null &&
            value.error_code === "durable_state_unavailable"))
      );
    case "unknown":
      return true;
    default:
      return false;
  }
}

function boardSummaryPage(value: unknown): BoardSummaryPageDto {
  if (
    !isRecord(value) ||
    Object.keys(value).length !== 2 ||
    !isUnknownArray(value.boards) ||
    value.boards.length > catalogPageSize ||
    !value.boards.every(isBoardSummary) ||
    (value.next_cursor !== null && !isBoundedText(value.next_cursor, 1, 2048))
  ) {
    return invalidResponse();
  }
  return { boards: value.boards, next_cursor: value.next_cursor };
}

async function readCatalog(signal?: AbortSignal): Promise<readonly BoardSummaryDto[]> {
  const boards: BoardSummaryDto[] = [];
  const seenIds = new Set<string>();
  const seenCursors = new Set<string>();
  const query = new URLSearchParams({ limit: String(catalogPageSize) });

  for (let page = 0; page < catalogPageLimit; page += 1) {
    const { data, headers } = await readJson(`/boards?${query.toString()}`, signal);
    entityTag(headers);
    const { boards: rows, next_cursor: cursor } = boardSummaryPage(data);
    for (const row of rows) {
      if (seenIds.has(row.id)) {
        return invalidResponse();
      }
      seenIds.add(row.id);
      boards.push(row);
    }
    if (cursor === null) {
      return boards;
    }
    if (rows.length === 0 || seenCursors.has(cursor)) {
      return invalidResponse();
    }
    seenCursors.add(cursor);
    query.set("cursor", cursor);
  }
  return invalidResponse();
}

export async function readWorkspace(signal?: AbortSignal): Promise<WorkspaceData> {
  const deadline = readDeadline(signal);
  const { data, headers } = await readJson("/workspace", deadline);
  const workspace = workspaceDto(data);
  const workspaceEtag = entityTag(headers);
  const boards = await readCatalog(deadline);
  return { workspace, workspaceEtag, boards };
}

export async function readService(signal?: AbortSignal): Promise<ServiceDto> {
  const { data, headers } = await readJson("/service", signal);
  entityTag(headers);
  if (
    !isRecord(data) ||
    Object.keys(data).length !== 6 ||
    !isInteger(data.port, 1, 65535) ||
    typeof data.service_instance_id !== "string" ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(
      data.service_instance_id,
    ) ||
    !isUuid(data.build_id) ||
    (data.state !== "running" && data.state !== "draining") ||
    data.api_version !== "2026-11-10" ||
    (data.restart_error_code !== null && !isBoundedText(data.restart_error_code, 0, 80))
  ) {
    return invalidResponse();
  }
  return {
    port: data.port,
    service_instance_id: data.service_instance_id,
    build_id: data.build_id,
    state: data.state,
    api_version: data.api_version,
    restart_error_code: data.restart_error_code,
  };
}
