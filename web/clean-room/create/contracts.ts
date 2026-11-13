import type { CardDto, EntityTag, PriorityDto, UserDto, Uuid } from "../api-types.js";
import type { MountComponent } from "../contracts.js";

export type CreateRelationshipKind = "blocks" | "duplicates" | "references" | "relates_to";
export interface CreateRelationshipDraft {
  readonly kind: CreateRelationshipKind;
  readonly direction: "outgoing" | "incoming";
  readonly targetCardId: Uuid;
}
export interface CreateDraft {
  readonly title: string;
  readonly description: string;
  readonly ownerId: Uuid;
  readonly priorityId: Uuid;
  readonly relationships: readonly CreateRelationshipDraft[];
}
export interface CreateInput extends CreateDraft {
  readonly requestId: Uuid;
  readonly cardId: Uuid;
  readonly boardId: Uuid;
  readonly boardEtag: EntityTag;
  readonly boardRevision: number;
  readonly laneId: Uuid;
  readonly priorities: readonly Pick<PriorityDto, "id" | "rank">[];
  readonly completed: boolean;
}
export interface CreateReceipt {
  readonly card: CardDto;
  readonly cardEtag: EntityTag;
}
export interface CreateAttachmentProps {
  readonly form: HTMLFormElement;
  readonly disabled: boolean;
}
export interface CreateProps {
  readonly laneName: string;
  readonly actorName: string;
  readonly draft: CreateDraft;
  readonly priorities: readonly PriorityDto[];
  readonly owners: readonly UserDto[];
  readonly targets: readonly Pick<CardDto, "id" | "ticket" | "title">[];
  readonly phase: "editing" | "submitting" | "unknown";
  readonly writable?: boolean;
  readonly error: string | null;
  readonly attachment?: MountComponent<CreateAttachmentProps>;
  readonly onDraftChange: (draft: CreateDraft) => void;
  readonly onSubmit: () => void;
  readonly onCancel: () => void;
  readonly onInspect: () => void;
}
