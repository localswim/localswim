import type {
  ActorKind,
  BoardSummaryDto,
  BoardViewDto,
  EntityTag,
  PolicyDto,
  PriorityDto,
  ProblemDto,
  ServiceDto,
  SessionDto,
  Timestamp,
  UserDto,
  Uuid,
  WorkspaceDto,
} from "./api-types.js";
import type { BoardCardProjection } from "./board/filter.js";
import type { MoveIntent } from "./board/move.js";
import type { LiveConnection } from "./live/controller.js";
import type { RetainedBoardCache } from "./workspace/retained-cache.js";

// Confirmed data survives a failed refresh without implying it is still writable.
export interface Confirmed<T> {
  readonly value: T;
  readonly confirmedAt: Timestamp;
}

export type ReadFailure =
  | { readonly kind: "http"; readonly problem: ProblemDto }
  | { readonly kind: "transport" | "invalid_response" };

// A failed refresh retains the last confirmed value. Only ready data permits writes.
export type ReadState<T> =
  | { readonly status: "loading"; readonly previous: Confirmed<T> | null }
  | { readonly status: "ready"; readonly current: Confirmed<T> }
  | {
      readonly status: "unavailable";
      readonly previous: Confirmed<T> | null;
      readonly failure: ReadFailure;
    };

export interface WorkspaceData {
  readonly workspace: WorkspaceDto;
  readonly workspaceEtag: EntityTag;
  readonly boards: readonly BoardSummaryDto[];
}

// An adapter assembles these independently addressed resources before presentation.
export interface BoardData {
  readonly view: BoardViewDto;
  readonly boardEtag: EntityTag;
  readonly users: readonly UserDto[];
  readonly priorities: readonly PriorityDto[];
  readonly policy: PolicyDto;
}

// Semantic route only. The observed browser URL syntax belongs to the root adapter.
export type RouteState =
  | { readonly kind: "workspace" }
  | {
      readonly kind: "board";
      readonly boardId: Uuid;
      readonly cardId: Uuid | null;
      readonly search: string;
      readonly showCompleted: boolean;
    };

export interface ScrollPosition {
  readonly left: number;
  readonly top: number;
}

export interface BoardScroll {
  readonly board: ScrollPosition;
  readonly lanes: readonly {
    readonly laneId: Uuid;
    readonly position: ScrollPosition;
  }[];
}

export interface NavigationRequest {
  readonly route: RouteState;
  readonly history: "push" | "replace";
}

// Feature owners provide precise value types. The root retains drafts across reads/navigation.
export interface Draft<T> {
  readonly value: T;
  readonly baseRevision: number | null;
  readonly dirty: boolean;
}

export interface FocusTarget {
  readonly control: string;
  readonly resourceId: Uuid | null;
}

export interface BoardViewState {
  readonly boardId: Uuid;
  readonly search: string;
  readonly showCompleted: boolean;
  readonly cardId: Uuid | null;
  readonly scroll: BoardScroll;
  readonly focus: FocusTarget | null;
}

export type DraftKey =
  | { readonly kind: "create_card"; readonly boardId: Uuid }
  | {
      readonly kind: "card" | "comment" | "relationship" | "attachment";
      readonly boardId: Uuid;
      readonly cardId: Uuid;
    };

// A resolved success requires a validated response or authoritative inspection.
// Unknown results remain pending inspection, even after abort or disconnection.
export type MutationResult<T> =
  | { readonly outcome: "committed"; readonly value: T }
  | {
      readonly outcome: "not_applied";
      readonly problem: ProblemDto & { readonly outcome: "not_applied" };
    }
  | {
      readonly outcome: "unknown";
      readonly requestId: Uuid;
      readonly problem: ProblemDto | null;
    };

export type InspectionResult<T> =
  | { readonly outcome: "committed"; readonly value: T }
  | { readonly outcome: "not_applied" }
  | { readonly outcome: "unknown" };

export interface MountedComponent<Props> {
  update(props: Props): void;
  dispose(): void;
}

export type MountComponent<Props> = (host: HTMLElement, props: Props) => MountedComponent<Props>;

export interface BoardProps {
  readonly liveMoveRevision?: number;
  readonly boardName?: string;
  readonly data: ReadState<BoardData>;
  readonly session: SessionDto | null;
  readonly route: Extract<RouteState, { readonly kind: "board" }>;
  readonly scroll: BoardScroll;
  readonly focus: FocusTarget | null;
  readonly onNavigate: (request: NavigationRequest) => void;
  readonly onScrollChange: (scroll: BoardScroll) => void;
  readonly onFocusChange: (focus: FocusTarget | null) => void;
  readonly onRefresh: () => void;
  readonly projection?: BoardCardProjection;
  readonly writable?: boolean;
  readonly onLaneAction?: (action: "create" | "order" | "approve", laneId: Uuid) => void;
  readonly approvalState?: (laneId: Uuid) => "busy" | "unknown" | null;
  readonly onMove?: (intent: MoveIntent) => void;
  readonly moveEnabled?: boolean;
}

export interface WorkspaceSlots {
  readonly mountBoard: MountComponent<BoardProps>;
}

export interface WorkspaceProps {
  readonly cache?: RetainedBoardCache;
  readonly refreshing?: boolean;
  readonly boardPath?: (id: Uuid) => string;
  readonly unavailableBoardKey?: string;
  readonly data: ReadState<WorkspaceData>;
  readonly service: ReadState<ServiceDto>;
  readonly session: SessionDto | null;
  readonly route: RouteState;
  readonly selectedBoard: BoardProps | null;
  readonly scroll: ScrollPosition;
  readonly slots: WorkspaceSlots;
  readonly onNavigate: (request: NavigationRequest) => void;
  readonly onScrollChange: (scroll: ScrollPosition) => void;
  readonly onRefresh: () => void;
  readonly connection?: LiveConnection;
  readonly lastActor?: ActorKind | null;
  readonly lastActorAt?: Timestamp | null;
  readonly searchFound?: number;
  readonly onSearch?: (value: string) => void;
  readonly overlayOpen?: boolean;
}
