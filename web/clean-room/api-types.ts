// Fresh declarations of the V2 wire contract in docs/openapi.yaml.
// Response validation belongs to the HTTP adapter before these types are exposed.
export type Uuid = string;
export type UuidV7 = string;
export type Timestamp = string;
export type EntityTag = string;
export type ActorKind = "human" | "bot";
export type Availability = "open" | "closed" | "unhealthy";

export interface AttentionDto {
  readonly blocked: number;
  readonly needs_input: number;
  readonly ready_for_review: number;
}

export interface PublishingDto {
  readonly enabled: boolean;
  readonly repository: string | null;
  readonly last_success_at: Timestamp | null;
  readonly pending: boolean;
  readonly error_code: string | null;
}

export interface SessionDto {
  readonly principal_id: Uuid;
  readonly kind: ActorKind;
  readonly api_version: "2026-11-10";
  readonly workspace_admin: boolean;
}

export interface ServiceDto {
  readonly port: number;
  readonly service_instance_id: UuidV7;
  readonly build_id: Uuid;
  readonly state: "running" | "draining";
  readonly api_version: "2026-11-10";
  readonly restart_error_code: string | null;
}

export interface WorkspaceDto {
  readonly revision: number;
  readonly pinned_board_ids: readonly Uuid[];
  readonly publishing: PublishingDto;
}

export interface BoardSummaryDto {
  readonly id: Uuid;
  readonly name: string | null;
  readonly revision: number | null;
  readonly availability: Availability | "unknown";
  readonly attention: AttentionDto | null;
  readonly last_card_edit_at: Timestamp | null;
  readonly error_code: string | null;
}

export interface BoardSummaryPageDto {
  readonly boards: readonly BoardSummaryDto[];
  readonly next_cursor: string | null;
}

export interface MutableEntityDto {
  readonly id: Uuid;
  readonly revision: number;
  readonly created_at: Timestamp;
  readonly updated_at: Timestamp;
}

export interface ActorDecorationDto {
  readonly label?: string;
  readonly accent?: string;
}

export interface BoardDto extends MutableEntityDto {
  readonly name: string;
  readonly policy_id: Uuid;
  readonly user_ids: readonly Uuid[];
  readonly default_owner_id: Uuid;
  readonly lane_ids: readonly Uuid[];
  readonly priority_ids: readonly Uuid[];
  readonly availability: Availability;
  readonly attention: AttentionDto;
  readonly last_card_edit_at: Timestamp | null;
  readonly publishing: PublishingDto;
  readonly user_css: string;
  readonly actor_presentation?: {
    readonly human?: ActorDecorationDto;
    readonly bot?: ActorDecorationDto;
  };
}

export interface UserDto {
  readonly id: Uuid;
  readonly revision: number;
  readonly created_at: Timestamp;
  readonly label: string;
  readonly kind: ActorKind;
  readonly color: string;
}

export interface PriorityDto extends MutableEntityDto {
  readonly board_id: Uuid;
  readonly key: string;
  readonly label: string;
  readonly rank: number;
  readonly color: string;
}

export interface LaneDto extends MutableEntityDto {
  readonly board_id: Uuid;
  readonly key: string;
  readonly label: string;
  readonly position: number;
  readonly role: "ordinary" | "completed" | "blocked" | "needs_input" | "ready_for_review";
  readonly retired: boolean;
  readonly approval_enabled: boolean;
  readonly reorder_enabled: boolean;
  readonly notes?: string;
  readonly bulk_approval_comment?: string;
}

export type Capability =
  | "read"
  | "edit_card"
  | "comment"
  | "archive_card"
  | "manage_relationships"
  | "manage_attachments"
  | "set_priority"
  | "approve"
  | "manage_board"
  | "manage_workflow"
  | "manage_permissions"
  | "manage_notifications"
  | "backup"
  | "restore"
  | "publish"
  | "close_board"
  | "create_card"
  | "reorder_tickets";

export interface PolicyDto extends MutableEntityDto {
  readonly board_id: Uuid;
  readonly default_priority_id: Uuid;
  readonly create_grants: readonly {
    readonly user_id: Uuid;
    readonly lane_id: Uuid;
  }[];
  readonly move_grants: readonly {
    readonly user_id: Uuid;
    readonly from_lane_id: Uuid;
    readonly to_lane_id: Uuid;
  }[];
  readonly resource_grants: readonly {
    readonly user_id: Uuid;
    readonly capabilities: readonly Capability[];
  }[];
}

export interface CardDto extends MutableEntityDto {
  readonly board_id: Uuid;
  readonly ticket: number;
  readonly title: string;
  readonly description: string;
  readonly lane_id: Uuid;
  readonly owner_id: Uuid;
  readonly priority_id: Uuid;
  readonly parent_id: Uuid | null;
  readonly child_ids: readonly Uuid[];
  readonly archived: boolean;
  readonly cover_attachment_id: Uuid | null;
  readonly comment_count: number;
  readonly attachment_count: number;
  readonly blocking_depth: number;
  readonly blocked_card_count: number;
  readonly triage_score: number;
  readonly completed_at: Timestamp | null;
}

export interface BoardViewDto {
  readonly board: BoardDto;
  readonly lanes: readonly LaneDto[];
  readonly cards: readonly CardDto[];
}

export interface ProblemDto {
  readonly type: "about:blank";
  readonly title: string;
  readonly status: number;
  readonly code:
    | "invalid_request"
    | "unauthorized"
    | "forbidden"
    | "not_found"
    | "conflict"
    | "precondition_failed"
    | "precondition_required"
    | "payload_too_large"
    | "unsupported_media_type"
    | "method_not_allowed"
    | "not_acceptable"
    | "internal_error"
    | "unavailable"
    | "outcome_unknown";
  readonly detail: string;
  readonly request_id: Uuid;
  readonly outcome: "not_applied" | "unknown" | "not_applicable";
  readonly errors: readonly {
    readonly field: string;
    readonly code:
      | "required"
      | "invalid_type"
      | "invalid_value"
      | "duplicate"
      | "unknown_reference"
      | "forbidden_field"
      | "incompatible_history"
      | "integrity_failure";
  }[];
}
