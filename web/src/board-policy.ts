import { ApiError } from "../api.mjs";
import type { AppContext } from "./model.js";

const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export type BrowserPrincipal = Readonly<{ principalId: string; workspaceAdmin: boolean }>;
export type BoardPolicyContext = BrowserPrincipal &
  Readonly<{
    defaultPriorityId: string;
    createLaneIds: ReadonlySet<string>;
    moveLaneIds: ReadonlyMap<string, ReadonlySet<string>>;
    capabilities: ReadonlySet<string>;
    canClose: boolean;
  }>;

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ApiError("invalid_response");
  }
  return value as Record<string, unknown>;
}

function id(value: unknown): string {
  if (typeof value !== "string" || !idPattern.test(value)) throw new ApiError("invalid_response");
  return value;
}

function rows(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new ApiError("invalid_response");
  return value as unknown[];
}

export async function readBrowserPrincipal({ api, signal }: AppContext): Promise<BrowserPrincipal> {
  const { data } = await api.getSession(signal);
  const session = record(data);
  if (session.kind !== "human" || typeof session.workspace_admin !== "boolean") {
    throw new ApiError("invalid_response");
  }
  return { principalId: id(session.principal_id), workspaceAdmin: session.workspace_admin };
}

export async function readBoardPolicy(
  { api, signal }: AppContext,
  principal: BrowserPrincipal,
  boardId: string,
  policyId: string,
): Promise<BoardPolicyContext> {
  const { data } = await api.getPolicy(policyId, signal);
  const policy = record(data);
  if (policy.id !== policyId || policy.board_id !== boardId) throw new ApiError("invalid_response");
  const createLaneIds = new Set<string>();
  const moveLaneIds = new Map<string, Set<string>>();
  const capabilities = new Set<string>();
  for (const entry of rows(policy.create_grants)) {
    const grant = record(entry);
    const lane = id(grant.lane_id);
    if (id(grant.user_id) === principal.principalId) createLaneIds.add(lane);
  }
  for (const entry of rows(policy.move_grants)) {
    const grant = record(entry);
    const from = id(grant.from_lane_id);
    const to = id(grant.to_lane_id);
    if (id(grant.user_id) !== principal.principalId) continue;
    const destinations = moveLaneIds.get(from) ?? new Set<string>();
    destinations.add(to);
    moveLaneIds.set(from, destinations);
  }
  for (const entry of rows(policy.resource_grants)) {
    const grant = record(entry);
    if (id(grant.user_id) !== principal.principalId) continue;
    for (const capability of rows(grant.capabilities)) {
      if (typeof capability !== "string") throw new ApiError("invalid_response");
      capabilities.add(capability);
    }
  }
  return {
    ...principal,
    defaultPriorityId: id(policy.default_priority_id),
    createLaneIds,
    moveLaneIds,
    capabilities,
    canClose: capabilities.has("close_board"),
  };
}
