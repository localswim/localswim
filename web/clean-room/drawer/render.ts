import type { Timestamp, Uuid } from "../api-types.js";
import type { BoardData, MountComponent } from "../contracts.js";
import type { DrawerActivity, DrawerCardLink, DrawerData, DrawerProps } from "./contracts.js";
import { mountCardActions } from "./card-actions.js";
import { mountTextEditor } from "./edit-text.js";
import { mountRelationshipEditor } from "./edit-relationships.js";
import { mountAttachmentView } from "./attachment-view.js";
import { updateCommentComposer, wireCommentComposer } from "./post-comment.js";

function element<K extends keyof HTMLElementTagNameMap>(tag: K, name: string, text?: string) {
  const node = document.createElement(tag);
  node.className = `lsw-drawer-${name}`;
  if (text !== undefined) node.textContent = text;
  return node;
}

function control(name: string, text: string, disabled = false): HTMLButtonElement {
  const button = element("button", name, text);
  button.type = "button";
  button.disabled = disabled;
  return button;
}

function slot<K extends "span" | "div">(tag: K, name: string, ...children: HTMLElement[]) {
  const node = element(tag, "slot");
  node.dataset.drawerSlot = name;
  node.append(...children);
  return node;
}

function section(title: string, action?: HTMLElement) {
  const region = element("section", "section");
  const heading = element("div", "section-heading");
  heading.append(element("h3", "label", title));
  if (action !== undefined) heading.append(action);
  region.append(heading);
  return region;
}

function ticketLabel(ticket: number): string {
  return String(ticket).padStart(4, "0");
}

function cardLink(card: DrawerCardLink): HTMLButtonElement {
  const node = control("card-link", `#${ticketLabel(card.ticket)} ${card.title}`);
  node.dataset.drawerCardId = card.id;
  return node;
}

function ticketLink(parent: HTMLElement, token: string): void {
  const ticket = Number(token.slice(1));
  if (!Number.isSafeInteger(ticket) || ticket < 1) {
    parent.append(token);
    return;
  }
  const link = control("card-link", token);
  link.dataset.drawerTicket = String(ticket);
  parent.append(link);
}

function appendInline(parent: HTMLElement, text: string): void {
  const tokens =
    /`([^`\r\n]+)`|\*\*([^*\r\n]+)\*\*|\*([^*\r\n]+)\*|\[([^\[\]\r\n]+)\]\((https?:\/\/[^\s()]+)\)|(?<![\p{L}\p{N}_])#[0-9]+(?![\p{L}\p{N}_])/gu;
  let index = 0;
  for (const match of text.matchAll(tokens)) {
    const [token, code, strong, emphasis, label, href] = match;
    if (token === undefined) continue;
    parent.append(text.slice(index, match.index));
    index = match.index + token.length;
    if (code !== undefined) {
      parent.append(element("code", "inline-code", code));
    } else if (strong !== undefined || emphasis !== undefined) {
      parent.append(
        element(strong === undefined ? "em" : "strong", "emphasis", strong ?? emphasis),
      );
    } else if (label !== undefined && href !== undefined) {
      const link = element("a", "external-link", label);
      link.href = href;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      parent.append(link);
    } else {
      ticketLink(parent, token);
    }
  }
  parent.append(text.slice(index));
}

function renderProse(container: HTMLElement, text: string): void {
  const lines = text.split(/\r\n|\r|\n/);
  let list: HTMLOListElement | HTMLUListElement | null = null;
  let paragraph: HTMLParagraphElement | null = null;
  for (const line of lines) {
    const item = /^(?:([-*])|([0-9]+)\.)[ \t]+(.+)$/.exec(line);
    if (item?.[3] !== undefined) {
      paragraph = null;
      const tag = item[1] === undefined ? "ol" : "ul";
      if (list === null || list.localName !== tag) {
        list = element(tag, "prose-list");
        if (list instanceof HTMLOListElement) list.start = Number(item[2]);
        container.append(list);
      }
      const row = element("li", "prose-list-item");
      appendInline(row, item[3]);
      list.append(row);
    } else {
      list = null;
      if (line === "") {
        paragraph = null;
        continue;
      }
      if (paragraph === null) {
        paragraph = element("p", "prose-paragraph");
        container.append(paragraph);
      } else {
        paragraph.append("\n");
      }
      appendInline(paragraph, line);
    }
  }
}

function actor(board: BoardData | null, id: Uuid) {
  const user = board?.users.find((candidate) => candidate.id === id);
  if (user === undefined) return { label: "Unknown user", color: "" };
  const decoration = board?.view.board.actor_presentation?.[user.kind];
  return { label: decoration?.label ?? user.label, color: decoration?.accent ?? user.color };
}

function actorLabel(board: BoardData | null, id: Uuid): HTMLSpanElement {
  const display = actor(board, id);
  const label = element("span", "actor", display.label);
  label.style.color = display.color;
  return label;
}

const dateParts = new Intl.DateTimeFormat("en-US", {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "numeric",
  minute: "2-digit",
  hour12: true,
});

function timeLabel(at: Timestamp): HTMLTimeElement {
  const iso = at.replace(" ", "T");
  const parts = new Map(
    dateParts.formatToParts(new Date(iso)).map(({ type, value }) => [type, value]),
  );
  const day = `${parts.get("year")}-${parts.get("month")}-${parts.get("day")}`;
  const time = `${parts.get("hour")}:${parts.get("minute")}${parts.get("dayPeriod")?.toLowerCase()}`;
  const label = element("time", "time", `${day} ${time}`);
  label.dateTime = iso;
  return label;
}

function laneLabel(board: BoardData | null, id: Uuid | null): string | undefined {
  return board?.view.lanes.find((lane) => lane.id === id)?.label;
}

function approvalLabel(props: DrawerProps, data: DrawerData): string {
  const { board, session } = props;
  const { card } = data;
  if (
    board === null ||
    session?.kind !== "human" ||
    card.archived ||
    board.view.board.availability !== "open"
  ) {
    return "";
  }
  const lanes = board.view.lanes;
  const source = lanes.find((lane) => lane.id === card.lane_id);
  const target = lanes.find((lane) => lane.role === "completed" && !lane.retired);
  if (source?.role !== "ready_for_review" || source.retired || target === undefined) return "";
  const userId = session.principal_id;
  const canApprove = board.policy.resource_grants.some(
    (grant) => grant.user_id === userId && grant.capabilities.includes("approve"),
  );
  const canMove = board.policy.move_grants.some(
    (grant) =>
      grant.user_id === userId &&
      grant.from_lane_id === source.id &&
      grant.to_lane_id === target.id,
  );
  return canApprove && canMove ? `${actor(board, userId).label} Reviewed & Approved` : "";
}

function auditText(event: DrawerActivity, board: BoardData | null, creationLane: string): string {
  if (event.operation === "create" && event.resource_type === "card") {
    return creationLane === "" ? "created" : `created in ${creationLane}`;
  }
  if (event.field === "lane_id") {
    const from = laneLabel(board, event.before);
    const to = laneLabel(board, event.after);
    const action = event.operation === "approve" ? "reviewed and approved" : "moved";
    return from === undefined || to === undefined ? action : `${action} from ${from} to ${to}`;
  }
  if (event.field === "owner_id" && event.after !== null) {
    return `assigned to ${actor(board, event.after).label}`;
  }
  if (event.field === "priority_id") {
    const priority = board?.priorities.find((item) => item.id === event.after);
    return priority === undefined
      ? "changed priority"
      : `changed priority to ${priority.key.toUpperCase()}`;
  }
  if (event.field === "title") return "edited title";
  if (event.field === "description") return "edited description";
  if (event.field === "parent_id") return "changed parent";
  if (event.field === "archived") return event.after === "true" ? "archived" : "restored";
  if (event.field === "cover_attachment_id") return "changed cover attachment";
  if (event.operation === "attach") return "added attachment";
  if (event.operation === "detach") return "removed attachment";
  if (event.resource_type === "relationship") return "changed relationships";
  return `${event.operation.replaceAll("_", " ")} ${event.field?.replaceAll("_", " ") ?? event.resource_type}`;
}

function relatedRow(label: string, card: DrawerCardLink): HTMLLIElement {
  const row = element("li", "related-card");
  row.append(`${label} `, cardLink(card));
  if (card.archived) row.append(" (archived)");
  return row;
}

function activeControl(): HTMLElement | null {
  let active = document.activeElement;
  while (active?.shadowRoot?.activeElement != null) active = active.shadowRoot.activeElement;
  return active instanceof HTMLElement ? active : null;
}

export const mountDrawer: (
  host: HTMLElement,
  props: DrawerProps,
) => ReturnType<MountComponent<DrawerProps>> & { closeAnimated(): void } = (host, initialProps) => {
  let props = initialProps;
  let renderedData: DrawerData | undefined;
  let renderedBoard: BoardData | null = null;
  let renderedCard: Uuid | null = null;
  const opener = activeControl();
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };

  const shade = element("div", "shade");
  shade.setAttribute("aria-hidden", "true");
  const drawer = document.createElement("section");
  drawer.className = "lsw-drawer";
  drawer.setAttribute("role", "dialog");
  drawer.setAttribute("aria-modal", "true");
  drawer.setAttribute("aria-labelledby", "lsw-drawer-title");
  const header = element("header", "header");
  const heading = element("div", "heading");
  const prioritySlot = slot("span", "priority");
  const approval = control("approval", "", true);
  const approvalSlot = slot("span", "approval", approval);
  const ticket = element("span", "ticket");
  const close = control("close", "×");
  close.setAttribute("aria-label", "Close card");
  close.classList.toggle(
    "lsw-drawer-close-pointer",
    opener?.dataset.cardId !== undefined && !opener.matches(":focus-visible"),
  );
  heading.append(prioritySlot, approvalSlot, ticket, close);
  const title = element("h2", "title");
  title.id = "lsw-drawer-title";
  const location = element("p", "location");
  const ownerSlot = slot("span", "owner");
  const ownerRow = element("div", "owner");
  ownerRow.append(element("span", "label", "Owner"), ownerSlot);
  const titleButton = control("title-edit", "");
  title.append(titleButton);
  const titleEditor = slot("div", "title-editor");
  header.append(
    heading,
    slot("div", "title", title, titleEditor),
    location,
    slot("div", "move"),
    ownerRow,
  );

  const body = element("div", "body");
  const message = element("p", "message");
  message.setAttribute("role", "status");
  const retry = control("retry", "Retry");
  const content = element("div", "content");
  const editDescription = control("text-action", "edit", true);
  const description = section("Description", slot("span", "description", editDescription));
  const descriptionText = element("div", "prose");
  const descriptionEditor = slot("div", "description-editor");
  description.append(descriptionText, descriptionEditor);

  const attachments = section("Attachments");
  attachments.classList.add("lsw-drawer-attachments");
  const attachmentHost = slot("div", "attachment-view");
  attachments.append(
    slot(
      "div",
      "attachments",
      control("upload", "Upload attachment", true),
      element(
        "p",
        "hint",
        "Upload a file or paste a screen clip (Ctrl+V) anywhere in this card. Maximum 10 MiB per file.",
      ),
      attachmentHost,
    ),
  );

  const addRelationship = control("text-action lsw-drawer-add-relationship", "+", true);
  addRelationship.setAttribute("aria-label", "Add relationship");
  const relationships = section("Relationships", slot("span", "relationships", addRelationship));
  const relationshipList = element("ul", "links");
  const relationshipEmpty = element("p", "empty", "No relationships.");
  const relationshipForm = slot("div", "relationship-form");
  relationships.append(relationshipForm, relationshipEmpty, relationshipList);

  const comments = section("Comments");
  const commentInput = element("textarea", "comment-input");
  commentInput.disabled = true;
  commentInput.placeholder = "Leave a note on this card...";
  commentInput.setAttribute("aria-label", "Comment");
  const commentSubmit = control("comment-submit", "Comment", true);
  const commentStatus = element("p", "comment-status");
  commentStatus.setAttribute("role", "status");
  const inspectComment = control("comment-inspect", "Check comment status");
  const commentEmpty = element("p", "empty", "No comments yet.");
  const commentList = element("ul", "comments");
  comments.append(
    slot(
      "div",
      "comments",
      commentInput,
      element("p", "hint", "Enter posts. Shift+Enter adds a line break."),
      commentSubmit,
      commentStatus,
      inspectComment,
    ),
    commentEmpty,
    commentList,
  );

  const audit = section("Audit trail");
  const auditList = element("ul", "audit");
  audit.append(auditList);
  content.append(description, attachments, relationships, comments, audit);
  const operationStatus = element("p", "operation-status");
  operationStatus.setAttribute("role", "status");
  const operationInspect = control("retry", "Check result");
  body.append(message, retry, operationStatus, operationInspect, content);
  drawer.append(header, body);

  const emptyComment = {
    draft: "",
    canEdit: false,
    canSubmit: false,
    canInspect: false,
    status: "",
  };
  const commentElements = {
    input: commentInput,
    submit: commentSubmit,
    status: commentStatus,
    inspect: inspectComment,
  };
  wireCommentComposer(
    commentElements,
    () => props.comment ?? emptyComment,
    {
      onDraft: (value) => props.onCommentDraft?.(value),
      onSubmit: () => props.onCommentSubmit?.(),
      onInspect: () => props.onInspectOperation?.(),
    },
    lifetime.signal,
  );
  const cardActions = mountCardActions(
    { priority: prioritySlot, owner: ownerSlot },
    {
      board: null,
      drawer: null,
      session: null,
      writable: false,
      onPatch: (patch) => props.onPatch?.(patch),
    },
  );
  const relations = mountRelationshipEditor(addRelationship, relationshipForm, relationshipEmpty);
  const attachmentView = mountAttachmentView(attachmentHost, {
    attachments: null,
    coverId: null,
    canManage: false,
    pending: false,
    onCover() {},
    onDetach() {},
  });
  let editor: ReturnType<typeof mountTextEditor> | null = null;
  let editorField: "title" | "description" | null = null;
  let editorBaseValue = "";
  let editorConflict = false;
  let submittedText: { readonly prior: string; readonly requested: string } | null = null;

  function closeEditor(focus = true): void {
    const opener = editorField === "title" ? titleButton : editDescription;
    editor?.dispose();
    editor = null;
    editorField = null;
    editorConflict = false;
    submittedText = null;
    title.hidden = false;
    descriptionText.hidden = false;
    if (focus) opener.focus({ preventScroll: true });
  }

  function editorProps(field: "title" | "description", value: string) {
    return {
      field,
      value,
      pending: props.writable !== true && submittedText !== null,
      error: editorConflict
        ? "This field changed. Copy your draft or cancel and reopen the editor."
        : props.operationMessage || null,
      onSave(requested: string) {
        if (!props.writable) return;
        if (value !== editorBaseValue) {
          editorConflict = true;
          editor?.update(editorProps(field, value));
          return;
        }
        if (requested === value) {
          closeEditor();
          return;
        }
        submittedText = { prior: value, requested };
        props.onPatch?.(field === "title" ? { title: requested } : { description: requested });
      },
      onCancel: closeEditor,
    };
  }

  function beginEdit(field: "title" | "description"): void {
    const data = renderedData;
    if (!data || editor || !props.writable || !can("edit_card")) return;
    editorField = field;
    editorBaseValue = data.card[field];
    title.hidden = field === "title";
    descriptionText.hidden = field === "description";
    editor = mountTextEditor(
      field === "title" ? titleEditor : descriptionEditor,
      editorProps(field, data.card[field]),
    );
  }

  function can(
    capability: NonNullable<
      DrawerProps["board"]
    >["policy"]["resource_grants"][number]["capabilities"][number],
  ): boolean {
    return (
      props.board?.policy.resource_grants.some(
        (grant) =>
          grant.user_id === props.session?.principal_id && grant.capabilities.includes(capability),
      ) ?? false
    );
  }

  titleButton.addEventListener("click", () => beginEdit("title"), listeners);
  editDescription.addEventListener("click", () => beginEdit("description"), listeners);
  descriptionText.addEventListener("click", () => beginEdit("description"), listeners);
  operationInspect.addEventListener("click", () => props.onInspectOperation?.(), listeners);
  approval.addEventListener("click", () => props.onApprove?.(), listeners);

  close.addEventListener("click", () => props.onClose(), listeners);
  shade.addEventListener("click", () => props.onClose(), listeners);
  retry.addEventListener("click", () => props.onRefresh(), listeners);
  drawer.addEventListener(
    "click",
    (event) => {
      if (!(event.target instanceof Element)) return;
      const link = event.target.closest<HTMLButtonElement>(
        "button[data-drawer-card-id], button[data-drawer-ticket]",
      );
      const cardId = link?.dataset.drawerCardId;
      const linkedTicket = link?.dataset.drawerTicket;
      if (cardId !== undefined) props.onOpenCard(cardId);
      else if (linkedTicket !== undefined) props.onOpenTicket(Number(linkedTicket));
    },
    listeners,
  );
  document.addEventListener(
    "keydown",
    (event) => {
      close.classList.remove("lsw-drawer-close-pointer");
      if (event.defaultPrevented || event.isComposing) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        props.onClose();
      }
      if (event.key !== "Tab") return;
      const controls = Array.from(
        drawer.querySelectorAll<HTMLElement>(
          "button:not(:disabled), a[href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled)",
        ),
      ).filter((item) => item.tabIndex >= 0 && item.getClientRects().length > 0);
      const first = controls[0];
      const last = controls.at(-1);
      const active = activeControl();
      if (event.shiftKey ? active === first : active === last) {
        event.preventDefault();
        (event.shiftKey ? last : first)?.focus();
      }
    },
    listeners,
  );

  function update(next: DrawerProps): void {
    props = next;
    const data = next.data.status === "ready" ? next.data.current.value : next.data.previous?.value;
    const active = activeControl();
    const sessionActor =
      next.session === null ? null : actor(next.board, next.session.principal_id);
    commentSubmit.textContent =
      sessionActor === null ? "Comment" : `Comment as ${sessionActor.label}`;
    const approved = data === undefined ? "" : approvalLabel(next, data);
    approvalSlot.hidden = approved === "";
    approval.textContent = approved;
    approval.disabled = !next.writable || next.onApprove === undefined;
    operationStatus.hidden = !next.operationMessage;
    operationStatus.textContent = next.operationMessage ?? "";
    operationInspect.hidden =
      next.onInspectOperation === undefined || next.comment?.canInspect === true;
    updateCommentComposer(commentElements, next.comment ?? emptyComment);
    const editable =
      next.writable === true && data !== undefined && data.card.completed_at === null;
    cardActions.update({
      board: next.board,
      drawer: data ?? null,
      session: next.session,
      writable: editable,
      onPatch: (patch) => props.onPatch?.(patch),
    });
    titleButton.disabled = editDescription.disabled = !editable || !can("edit_card");
    attachmentView.update({
      attachments: data?.attachments ?? null,
      coverId: data?.card.cover_attachment_id ?? null,
      canManage: data?.card.completed_at === null && can("manage_attachments"),
      pending: !editable,
      onCover: (id) => {
        const attachment = data?.attachments?.find((item) => item.id === id) ?? null;
        props.onAttachment?.({ kind: "cover", attachment });
      },
      onDetach: (attachment) => props.onAttachment?.({ kind: "detach", attachment }),
    });
    relations.update(
      data === undefined
        ? null
        : {
            data,
            targets: next.board?.view.cards ?? [],
            writable: editable && can("manage_relationships"),
            committed: next.committedRelationship,
            onSubmit: (intent) => props.onRelationship?.(intent),
          },
    );
    for (const control of relationshipList.querySelectorAll<HTMLButtonElement | HTMLSelectElement>(
      "li[data-relationship-id] :is(select, button:not([data-drawer-card-id]))",
    )) {
      control.disabled = !editable || !can("manage_relationships");
    }
    message.hidden =
      next.data.status === "ready" || (next.data.status === "loading" && data !== undefined);
    retry.hidden = next.data.status !== "unavailable";
    message.textContent =
      next.data.status === "loading"
        ? "Loading card…"
        : data === undefined
          ? "Card unavailable"
          : "Showing the last confirmed card.";
    content.hidden = data === undefined;
    ticket.hidden = location.hidden = ownerRow.hidden = data === undefined;
    if (active === retry && retry.hidden) close.focus({ preventScroll: true });
    if (data === undefined) {
      titleButton.textContent = "Card";
      prioritySlot.hidden = true;
      renderedData = undefined;
      if (content.contains(active)) close.focus({ preventScroll: true });
      return;
    }
    if (
      editorField &&
      editor &&
      next.committedText?.field === editorField &&
      submittedText?.requested === next.committedText.value &&
      submittedText.prior !== submittedText.requested
    )
      closeEditor();
    else if (editorField && editor) editor.update(editorProps(editorField, data.card[editorField]));
    if (renderedData === data && renderedBoard === next.board) return;
    renderedData = data;
    renderedBoard = next.board;

    const { card } = data;
    const newCard = renderedCard !== card.id;
    if (newCard && editor) closeEditor(false);
    renderedCard = card.id;
    const currentLane = laneLabel(next.board, card.lane_id) ?? "";
    prioritySlot.hidden = !next.board?.priorities.some((item) => item.id === card.priority_id);
    ticket.textContent = ticketLabel(card.ticket);
    titleButton.textContent = card.title;
    titleButton.setAttribute("aria-label", `Rename card title: ${card.title}`);
    location.textContent = [currentLane, card.archived ? "Archived" : ""]
      .filter(Boolean)
      .join(" · ");
    descriptionText.replaceChildren();
    if (card.description === "") descriptionText.append(element("p", "empty", "No description."));
    else descriptionText.textContent = card.description;

    const rows: HTMLLIElement[] = [];
    if (data.parent !== null) rows.push(relatedRow("Parent", data.parent));
    for (const child of data.children) rows.push(relatedRow("Child", child));
    for (const relation of data.relationships) {
      rows.push(relations.renderRow(relation, cardLink(relation.card)));
    }
    relationshipEmpty.hidden = rows.length > 0;
    relationshipList.replaceChildren(...rows);
    commentEmpty.hidden = data.comments.length !== 0;
    commentList.replaceChildren();
    for (const comment of data.comments) {
      const row = element("li", "comment");
      const meta = element("p", "comment-meta");
      meta.append(actorLabel(next.board, comment.author_id), " · ", timeLabel(comment.created_at));
      const prose = element("div", "prose");
      renderProse(prose, comment.text);
      row.append(meta, prose);
      commentList.append(row);
    }

    const firstMove = data.activity.find((event) => event.field === "lane_id");
    const creationLane =
      firstMove === undefined ? currentLane : (laneLabel(next.board, firstMove.before) ?? "");
    const auditRows = data.activity.filter((event) => event.resource_type !== "comment");
    audit.hidden = auditRows.length === 0;
    auditList.replaceChildren();
    for (const event of auditRows) {
      const row = element("li", "audit-entry");
      row.append(
        timeLabel(event.at),
        actorLabel(next.board, event.actor_id),
        auditText(event, next.board, creationLane),
      );
      auditList.append(row);
    }
    if (newCard) body.scrollTop = 0;
    if (newCard || active?.isConnected === false) close.focus({ preventScroll: true });
  }

  host.append(shade, drawer);
  update(initialProps);
  close.focus({ preventScroll: true });
  function dispose(animate = false): void {
    lifetime.abort();
    cardActions.dispose();
    relations.dispose();
    attachmentView.dispose();
    editor?.dispose();
    const active = activeControl();
    const restore = active === document.body || drawer.contains(active);
    if (animate && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
      drawer.inert = true;
      shade.inert = true;
      shade.style.pointerEvents = "none";
      const slide = drawer.animate(
        [{ transform: "translateX(0)" }, { transform: "translateX(100%)" }],
        { duration: 180, easing: "ease-in", fill: "forwards" },
      );
      shade.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 180, fill: "forwards" });
      const remove = () => {
        drawer.remove();
        shade.remove();
      };
      void slide.finished.then(remove, remove);
    } else {
      drawer.remove();
      shade.remove();
    }
    if (restore && opener?.isConnected === true) opener.focus({ preventScroll: true });
  }
  return {
    update,
    dispose,
    closeAnimated: () => dispose(true),
  };
};
