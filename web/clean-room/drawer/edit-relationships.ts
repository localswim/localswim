import type { EntityTag, SessionDto, Timestamp, Uuid } from "../api-types.js";
import type { InspectionResult, MutationResult } from "../contracts.js";
import {
  apiPrefix,
  entityTag,
  invalidResponse,
  isRecord,
  isTimestamp,
  isUuid,
  mutateJson,
  readJson,
} from "../http.js";
import type { DrawerCardLink, DrawerData, DrawerRelationship } from "./contracts.js";

type Kind = DrawerRelationship["kind"];
type Direction = DrawerRelationship["direction"];

interface RelationshipLink {
  readonly kind: Kind;
  readonly source_card_id: Uuid;
  readonly target_card_id: Uuid;
}

export type RelationshipIntent =
  | { readonly action: "create"; readonly id: Uuid; readonly link: RelationshipLink }
  | {
      readonly action: "patch";
      readonly original: DrawerRelationship;
      readonly kind: Kind;
      readonly direction: Direction;
    }
  | { readonly action: "remove"; readonly original: DrawerRelationship };

interface OperationBase {
  readonly boardId: Uuid;
  readonly cardId: Uuid;
  readonly boardEtag: EntityTag;
  readonly boardRevision: number;
  readonly actorId: Uuid;
  readonly id: Uuid;
}

export type RelationshipOperation = OperationBase &
  (
    | { readonly action: "create"; readonly after: RelationshipLink }
    | {
        readonly action: "patch";
        readonly before: RelationshipLink;
        readonly after: RelationshipLink;
        readonly etag: EntityTag;
        readonly createdAt: Timestamp;
      }
    | { readonly action: "remove"; readonly before: RelationshipLink; readonly etag: EntityTag }
  );

export interface RelationshipReceipt {
  readonly boardEtag: EntityTag;
  readonly revision: number;
}

export function applyRelationshipReceipt(
  data: DrawerData,
  input: RelationshipOperation,
  receipt: RelationshipReceipt,
  targets: readonly DrawerCardLink[],
): DrawerData {
  if (input.action === "remove") {
    return {
      ...data,
      boardEtag: receipt.boardEtag,
      boardRevision: receipt.revision,
      relationships: data.relationships.filter(({ id }) => id !== input.id),
    };
  }
  const { kind, source_card_id: source, target_card_id: target } = input.after;
  const direction = source === data.card.id ? "outgoing" : "incoming";
  const prior = data.relationships.find(({ id }) => id === input.id);
  const otherId = direction === "outgoing" ? target : source;
  const other = prior?.card ?? targets.find(({ id }) => id === otherId);
  if (other === undefined) return data;
  const row: DrawerRelationship = { id: input.id, kind, direction, card: other };
  return {
    ...data,
    boardEtag: receipt.boardEtag,
    boardRevision: receipt.revision,
    relationships: prior
      ? data.relationships.map((relationship) => (relationship.id === row.id ? row : relationship))
      : [...data.relationships, row],
  };
}

interface RelationshipRow extends RelationshipLink {
  readonly id: Uuid;
  readonly board_id: Uuid;
  readonly revision: number;
  readonly created_at: Timestamp;
  readonly updated_at: Timestamp;
}

const choices = [
  { kind: "blocks", direction: "outgoing", label: "Blocks" },
  { kind: "blocks", direction: "incoming", label: "Blocked by" },
  { kind: "duplicates", direction: "outgoing", label: "Duplicates" },
  { kind: "duplicates", direction: "incoming", label: "Duplicated by" },
  { kind: "references", direction: "outgoing", label: "References" },
  { kind: "references", direction: "incoming", label: "Referenced by" },
  { kind: "relates_to", direction: "outgoing", label: "Relates to" },
] as const;

function orderedLink(
  kind: Kind,
  direction: Direction,
  cardId: Uuid,
  otherId: Uuid,
): RelationshipLink {
  return direction === "incoming"
    ? { kind, source_card_id: otherId, target_card_id: cardId }
    : { kind, source_card_id: cardId, target_card_id: otherId };
}

function sameLink(value: unknown, expected: RelationshipLink): boolean {
  return (
    isRecord(value) &&
    value.kind === expected.kind &&
    value.source_card_id === expected.source_card_id &&
    value.target_card_id === expected.target_card_id
  );
}

function isRelationshipRow(value: unknown): value is RelationshipRow {
  return (
    isRecord(value) &&
    Object.keys(value).length === 8 &&
    isUuid(value.id) &&
    isUuid(value.board_id) &&
    isUuid(value.source_card_id) &&
    isUuid(value.target_card_id) &&
    value.source_card_id !== value.target_card_id &&
    choices.some(({ kind }) => value.kind === kind) &&
    typeof value.revision === "number" &&
    Number.isSafeInteger(value.revision) &&
    value.revision > 0 &&
    isTimestamp(value.created_at) &&
    isTimestamp(value.updated_at) &&
    value.updated_at >= value.created_at
  );
}

export async function prepareRelationshipOperation(
  data: DrawerData,
  intent: RelationshipIntent,
  actorId: Uuid,
  signal?: AbortSignal,
): Promise<RelationshipOperation> {
  const { card, boardEtag, boardRevision } = data;
  const base: OperationBase = {
    boardId: card.board_id,
    cardId: card.id,
    boardEtag,
    boardRevision,
    actorId,
    id: intent.action === "create" ? intent.id : intent.original.id,
  };
  if (intent.action === "create") {
    const { source_card_id: source, target_card_id: target } = intent.link;
    if (
      !isUuid(intent.id) ||
      !isUuid(source) ||
      !isUuid(target) ||
      (source === card.id) === (target === card.id)
    ) {
      return invalidResponse();
    }
    return { ...base, action: "create", after: intent.link };
  }

  const original = data.relationships.find(({ id }) => id === base.id);
  if (
    original === undefined ||
    original.kind !== intent.original.kind ||
    original.direction !== intent.original.direction ||
    original.card.id !== intent.original.card.id ||
    (intent.action === "patch" &&
      intent.kind === original.kind &&
      intent.direction === original.direction)
  ) {
    return invalidResponse();
  }
  const before = orderedLink(original.kind, original.direction, card.id, original.card.id);
  const { data: row, headers } = await readJson(`/relationships/${base.id}`, signal);
  if (
    !isRelationshipRow(row) ||
    row.id !== base.id ||
    row.board_id !== base.boardId ||
    row.revision > base.boardRevision ||
    !sameLink(row, before)
  ) {
    return invalidResponse();
  }
  const etag = entityTag(headers);
  return intent.action === "remove"
    ? { ...base, action: "remove", before, etag }
    : {
        ...base,
        action: "patch",
        before,
        after: orderedLink(intent.kind, intent.direction, card.id, original.card.id),
        etag,
        createdAt: row.created_at,
      };
}

export function writeRelationshipOperation(
  input: RelationshipOperation,
  requestId: Uuid,
  signal?: AbortSignal,
  beforeDispatch?: (session: SessionDto) => void,
): Promise<MutationResult<RelationshipReceipt>> {
  const path = `/relationships/${input.id}`;
  const revision = input.boardRevision + 1;
  function validate(data: unknown, headers: Headers): RelationshipReceipt {
    const boardEtag = entityTag(headers, "X-Board-ETag");
    if (boardEtag === input.boardEtag) return invalidResponse();
    if (input.action !== "remove") {
      const etag = entityTag(headers);
      if (
        !isRelationshipRow(data) ||
        data.id !== input.id ||
        data.board_id !== input.boardId ||
        data.revision !== revision ||
        !sameLink(data, input.after) ||
        (input.action === "create" &&
          (data.created_at !== data.updated_at ||
            headers.get("Location") !== `${apiPrefix}${path}`)) ||
        (input.action === "patch" && (data.created_at !== input.createdAt || etag === input.etag))
      ) {
        return invalidResponse();
      }
    }
    return { boardEtag, revision };
  }

  const shared = { requestId, signal, beforeDispatch, validate };
  switch (input.action) {
    case "create":
      return mutateJson("/relationships", {
        ...shared,
        method: "POST",
        successStatus: 201,
        body: {
          ...input.after,
          id: input.id,
          board_id: input.boardId,
          expected_board_etag: input.boardEtag,
        },
      });
    case "patch":
      return mutateJson(path, {
        ...shared,
        method: "PATCH",
        etag: input.etag,
        body: {
          kind: input.after.kind,
          expected_board_etag: input.boardEtag,
          ...(input.before.source_card_id === input.after.source_card_id
            ? {}
            : {
                source_card_id: input.after.source_card_id,
                target_card_id: input.after.target_card_id,
              }),
        },
      });
    case "remove":
      return mutateJson(path, {
        ...shared,
        method: "DELETE",
        successStatus: 204,
        etag: input.etag,
        boardEtag: input.boardEtag,
        body: undefined,
      });
  }
}

function auditMatches(value: string | null, expected: RelationshipLink | null): boolean {
  if (value === null || expected === null) return value === expected;
  try {
    const data: unknown = JSON.parse(value);
    return isRecord(data) && Object.keys(data).length === 3 && sameLink(data, expected);
  } catch {
    return false;
  }
}

export function inspectRelationshipOperation(
  input: RelationshipOperation,
  fresh: DrawerData,
): InspectionResult<DrawerData> {
  const revision = input.boardRevision + 1;
  if (
    fresh.card.board_id !== input.boardId ||
    fresh.card.id !== input.cardId ||
    fresh.boardRevision < revision ||
    fresh.boardEtag === input.boardEtag
  ) {
    return { outcome: "unknown" };
  }
  const before = input.action === "create" ? null : input.before;
  const after = input.action === "remove" ? null : input.after;
  const event = fresh.activity.find(
    (item) =>
      item.resource_type === "relationship" &&
      item.resource_id === input.id &&
      item.revision === revision &&
      item.board_id === input.boardId &&
      item.actor_id === input.actorId &&
      item.operation === input.action &&
      auditMatches(item.before, before) &&
      auditMatches(item.after, after),
  );
  // Absence alone cannot distinguish a fenced or still-running write from a failure.
  return event !== undefined ? { outcome: "committed", value: fresh } : { outcome: "unknown" };
}

export interface RelationshipEditorProps {
  readonly data: DrawerData;
  readonly targets: readonly DrawerCardLink[];
  readonly writable: boolean;
  readonly onSubmit: (intent: RelationshipIntent) => void;
  readonly committed?: RelationshipIntent | null;
}

function select(label: string, className: string): HTMLSelectElement {
  const control = document.createElement("select");
  control.className = className;
  control.setAttribute("aria-label", label);
  return control;
}

function button(label: string, type: "submit" | "button" = "button"): HTMLButtonElement {
  const control = document.createElement("button");
  control.type = type;
  control.textContent = label;
  control.className = "lsw-drawer-relation-action";
  return control;
}

function linkAlreadyExists(
  data: DrawerData,
  kind: Kind,
  direction: Direction,
  other: Uuid,
  except?: Uuid,
): boolean {
  return data.relationships.some(
    (row) =>
      row.id !== except &&
      row.kind === kind &&
      row.card.id === other &&
      (kind === "relates_to" || row.direction === direction),
  );
}

export function mountRelationshipEditor(
  add: HTMLButtonElement,
  formHost: HTMLElement,
  empty?: HTMLElement,
) {
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };
  let props: RelationshipEditorProps | null = null;
  let submitted: RelationshipIntent | null = null;
  let targets: RelationshipEditorProps["targets"] | undefined;
  let targetCardId: Uuid | undefined;

  const form = document.createElement("form");
  form.className = "lsw-drawer-relation-form";
  form.hidden = true;
  const kinds = select("Relationship type", "lsw-drawer-relation-kind");
  for (const [index, choice] of choices.entries())
    kinds.add(new Option(choice.label, String(index)));
  const related = select("Related card", "lsw-drawer-relation-target");
  const submit = button("Add", "submit");
  const cancel = button("Cancel");
  form.append(kinds, related, submit, cancel);
  formHost.append(form);

  function updateEmpty(): void {
    if (empty === undefined) return;
    const data = props?.data;
    empty.hidden =
      !form.hidden ||
      data === undefined ||
      data.parent !== null ||
      data.children.length > 0 ||
      data.relationships.length > 0;
  }

  function close(restoreFocus: boolean): void {
    form.hidden = true;
    submitted = null;
    add.setAttribute("aria-expanded", "false");
    updateEmpty();
    if (restoreFocus) add.focus({ preventScroll: true });
  }

  function sync(): void {
    const writable = props?.writable === true;
    add.disabled = !writable;
    kinds.disabled = related.disabled = !writable;
    const choice = choices[kinds.selectedIndex];
    submit.disabled =
      !writable ||
      props === null ||
      choice === undefined ||
      related.value === "" ||
      linkAlreadyExists(props.data, choice.kind, choice.direction, related.value);
  }

  add.setAttribute("aria-expanded", "false");
  add.addEventListener(
    "click",
    () => {
      if (props?.writable !== true) return;
      if (!form.hidden) {
        close(true);
        return;
      }
      kinds.selectedIndex = 0;
      related.selectedIndex = 0;
      form.hidden = false;
      add.setAttribute("aria-expanded", "true");
      updateEmpty();
      sync();
      kinds.focus({ preventScroll: true });
    },
    listeners,
  );
  cancel.addEventListener("click", () => close(true), listeners);
  form.addEventListener(
    "keydown",
    (event) => {
      if (event.key !== "Escape" || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      close(true);
    },
    listeners,
  );
  kinds.addEventListener("change", sync, listeners);
  related.addEventListener("change", sync, listeners);
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      const choice = choices[kinds.selectedIndex];
      if (
        props?.writable !== true ||
        submit.disabled ||
        choice === undefined ||
        !isUuid(related.value)
      )
        return;
      submitted = {
        action: "create",
        id: crypto.randomUUID(),
        link: orderedLink(choice.kind, choice.direction, props.data.card.id, related.value),
      };
      props.onSubmit(submitted);
    },
    listeners,
  );

  return {
    update(next: RelationshipEditorProps | null): void {
      const nextCardId = next?.data.card.id;
      const sameCard = nextCardId === targetCardId;
      props = next;
      sync();
      if (!sameCard || (submitted !== null && submitted === next?.committed)) close(sameCard);
      if (!sameCard || targets !== next?.targets) {
        const previousTarget = sameCard ? related.value : "";
        targets = next?.targets;
        targetCardId = nextCardId;
        const available = (targets ?? []).filter(
          ({ id, archived }) => id !== nextCardId && !archived,
        );
        available.sort((a, b) => a.ticket - b.ticket);
        related.replaceChildren(
          ...available.map(
            ({ id, ticket, title }) =>
              new Option(`#${String(ticket).padStart(4, "0")} ${title}`, id),
          ),
        );
        if (sameCard && !form.hidden) related.value = previousTarget;
      }
      sync();
      updateEmpty();
    },

    renderRow(relationship: DrawerRelationship, link: HTMLButtonElement): HTMLLIElement {
      const row = document.createElement("li");
      row.className = "lsw-drawer-related-card lsw-drawer-relation-row";
      row.dataset.relationshipId = relationship.id;
      const type = select("Relationship type", "lsw-drawer-relation-kind");
      const remove = button("Remove");
      const selected = choices.findIndex(
        ({ kind, direction }) =>
          kind === relationship.kind &&
          (kind === "relates_to" || direction === relationship.direction),
      );
      for (const [index, choice] of choices.entries()) {
        const option = new Option(choice.label, String(index));
        option.disabled =
          index !== selected &&
          props !== null &&
          linkAlreadyExists(
            props.data,
            choice.kind,
            choice.direction,
            relationship.card.id,
            relationship.id,
          );
        type.add(option);
      }
      type.selectedIndex = selected;
      type.disabled = remove.disabled = props?.writable !== true;
      type.addEventListener("change", () => {
        const index = type.selectedIndex;
        const choice = choices[index];
        type.selectedIndex = selected;
        if (
          choice !== undefined &&
          index !== selected &&
          props?.writable === true &&
          !linkAlreadyExists(
            props.data,
            choice.kind,
            choice.direction,
            relationship.card.id,
            relationship.id,
          )
        ) {
          props.onSubmit({
            action: "patch",
            original: relationship,
            kind: choice.kind,
            direction: choice.kind === "relates_to" ? relationship.direction : choice.direction,
          });
        }
      });
      remove.addEventListener("click", () => {
        if (props?.writable === true) props.onSubmit({ action: "remove", original: relationship });
      });
      const target = document.createElement("span");
      target.className = "lsw-drawer-relation-link";
      target.append(link);
      if (relationship.card.archived) target.append(" (archived)");
      row.append(type, target, remove);
      return row;
    },

    dispose(): void {
      lifetime.abort();
      form.remove();
    },
  };
}
