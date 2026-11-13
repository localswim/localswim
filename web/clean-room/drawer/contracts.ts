import type { CardDto, EntityTag, SessionDto, Timestamp, Uuid, UuidV7 } from "../api-types.js";
import type { BoardData, ReadState } from "../contracts.js";
import type { Attachment, AttachmentWrite } from "./attachment-data.js";
import type { CardPatch } from "./card-actions.js";
import type { RelationshipIntent } from "./edit-relationships.js";
import type { CommentComposerState } from "./post-comment.js";

export type DrawerCardLink = Pick<CardDto, "id" | "ticket" | "title" | "archived">;
export interface DrawerRelationship {
  readonly id: Uuid;
  readonly kind: "blocks" | "duplicates" | "references" | "relates_to";
  readonly direction: "outgoing" | "incoming";
  readonly card: DrawerCardLink;
}
export interface DrawerComment {
  readonly id: Uuid;
  readonly board_id: Uuid;
  readonly card_id: Uuid;
  readonly created_at: Timestamp;
  readonly author_id: Uuid;
  readonly text: string;
  readonly ticket: number;
}
export interface DrawerActivity {
  readonly id: UuidV7;
  readonly board_id: Uuid;
  readonly revision: number;
  readonly at: Timestamp;
  readonly actor_id: Uuid;
  readonly operation: string;
  readonly resource_type: string;
  readonly resource_id: Uuid;
  readonly field: string | null;
  readonly before: string | null;
  readonly after: string | null;
  readonly bulk_approval_comment?: string;
}
export interface DrawerData {
  readonly card: CardDto;
  readonly cardEtag: EntityTag;
  readonly boardRevision: number;
  readonly boardEtag: EntityTag;
  readonly parent: DrawerCardLink | null;
  readonly children: readonly DrawerCardLink[];
  readonly relationships: readonly DrawerRelationship[];
  readonly comments: readonly DrawerComment[];
  readonly activity: readonly DrawerActivity[];
  readonly attachments?: readonly Attachment[];
}
export interface DrawerProps {
  readonly data: ReadState<DrawerData>;
  readonly board: BoardData | null;
  readonly session: SessionDto | null;
  readonly writable?: boolean;
  readonly operationMessage?: string;
  readonly onInspectOperation?: () => void;
  readonly onPatch?: (patch: CardPatch) => void;
  readonly onRelationship?: (intent: RelationshipIntent) => void;
  readonly committedRelationship?: RelationshipIntent | null;
  readonly committedText?: { readonly field: "title" | "description"; readonly value: string };
  readonly onAttachment?: (action: AttachmentWrite["action"]) => void;
  readonly onApprove?: () => void;
  readonly comment?: CommentComposerState;
  readonly onCommentDraft?: (text: string) => void;
  readonly onCommentSubmit?: () => void;
  readonly onClose: () => void;
  readonly onRefresh: () => void;
  readonly onOpenCard: (id: Uuid) => void;
  readonly onOpenTicket: (ticket: number) => void;
}
