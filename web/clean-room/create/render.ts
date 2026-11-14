import type { MountComponent, MountedComponent } from "../contracts.js";
import type {
  CreateAttachmentProps,
  CreateDraft,
  CreateProps,
  CreateRelationshipDraft,
} from "./contracts.js";

const relationshipChoices = [
  { kind: "blocks", direction: "outgoing", label: "Blocks" },
  { kind: "blocks", direction: "incoming", label: "Blocked by" },
  { kind: "duplicates", direction: "outgoing", label: "Duplicates" },
  { kind: "duplicates", direction: "incoming", label: "Duplicated by" },
  { kind: "references", direction: "outgoing", label: "References" },
  { kind: "references", direction: "incoming", label: "Referenced by" },
  { kind: "relates_to", direction: "outgoing", label: "Relates to" },
] as const;

interface Choice {
  readonly value: string;
  readonly label: string;
}

interface RelationshipRow {
  readonly node: HTMLElement;
  readonly kind: HTMLSelectElement;
  readonly target: HTMLSelectElement;
  readonly remove: HTMLButtonElement;
}

function element<Tag extends keyof HTMLElementTagNameMap>(
  tag: Tag,
  className: string,
  text?: string,
): HTMLElementTagNameMap[Tag] {
  const node = document.createElement(tag);
  node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function action(label: string, type: "button" | "submit" = "button"): HTMLButtonElement {
  const button = element("button", "lsw-create-action", label);
  button.type = type;
  return button;
}

function field(label: string, control: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement) {
  const group = element("div", "lsw-create-field");
  const caption = element("label", "lsw-create-label", label);
  caption.htmlFor = control.id;
  group.append(caption, control);
  return group;
}

function options(select: HTMLSelectElement, choices: readonly Choice[]): void {
  select.replaceChildren(...choices.map(({ label, value }) => new Option(label, value)));
}

function describe(control: HTMLElement, id: string, text: string): HTMLParagraphElement {
  const hint = element("p", "lsw-create-hint", text);
  hint.id = id;
  control.setAttribute("aria-describedby", id);
  return hint;
}

export const mountCreate: MountComponent<CreateProps> = (host, initial) => {
  let props = initial;
  const lifetime = new AbortController();
  const listeners = { signal: lifetime.signal };
  const rows: RelationshipRow[] = [];
  let priorities: CreateProps["priorities"] | undefined;
  let owners: CreateProps["owners"] | undefined;
  let targets: CreateProps["targets"] | undefined;
  let targetOptions: readonly Choice[] = [];
  let attachmentMount: CreateProps["attachment"];
  let attachments: MountedComponent<CreateAttachmentProps> | undefined;

  const dialog = element("dialog", "lsw-create");
  dialog.setAttribute("aria-labelledby", "lsw-create-heading");
  dialog.setAttribute("aria-describedby", "lsw-create-subtitle");
  const form = element("form", "lsw-create-form");
  const header = element("header", "lsw-create-header");
  const heading = element("h2", "", "New card");
  heading.id = "lsw-create-heading";
  const subtitle = element("p", "lsw-create-subtitle");
  subtitle.id = "lsw-create-subtitle";
  header.append(heading, subtitle);
  const fields = element("div", "lsw-create-fields");

  const title = element("input", "lsw-create-control lsw-create-title");
  title.id = "lsw-create-title";
  title.type = "text";
  title.placeholder = "What needs doing, in one line";
  title.required = true;
  title.autofocus = true;
  const priority = element("select", "lsw-create-control");
  priority.id = "lsw-create-priority";
  const owner = element("select", "lsw-create-control");
  owner.id = "lsw-create-owner";
  const description = element("textarea", "lsw-create-control lsw-create-description");
  description.id = "lsw-create-description";
  description.placeholder = "The reasoning, the constraints, the traps...";
  const descriptionField = field("Description", description);
  descriptionField.append(
    describe(
      description,
      "lsw-create-description-hint",
      "Enter adds a new line. Use Add card to submit.",
    ),
  );

  const file = element("input", "");
  file.id = "lsw-create-files";
  file.type = "file";
  file.multiple = true;
  file.disabled = true;
  const attachmentField = field("Attachments", file);
  attachmentField.classList.add("lsw-create-attachments");
  attachmentField.append(
    describe(
      file,
      "lsw-create-attachment-hint",
      "Choose files or paste a screen clip (Ctrl+V) anywhere in this form. Files upload after the card is created, up to 10 MiB each. Keep this page open until uploads finish.",
    ),
  );
  const attachmentHost = element("div", "");

  const relationships = element("div", "lsw-create-relationships");
  const relationshipsLabel = element("span", "lsw-create-label", "Relationships");
  relationshipsLabel.id = "lsw-create-relationships-label";
  relationships.setAttribute("role", "group");
  relationships.setAttribute("aria-labelledby", relationshipsLabel.id);
  const rowHost = element("div", "lsw-create-relations");
  const addRelationship = action("Add relationship");
  const relationshipHint = describe(
    addRelationship,
    "lsw-create-relationships-hint",
    "Each relationship is saved atomically with the new card.",
  );
  relationships.append(relationshipsLabel, rowHost, addRelationship, relationshipHint);

  const feedback = element("p", "lsw-create-feedback");
  feedback.setAttribute("role", "alert");
  fields.append(
    field("Title", title),
    field("Priority", priority),
    field("Owner", owner),
    descriptionField,
    attachmentHost,
    relationships,
    feedback,
  );

  const footer = element("footer", "lsw-create-footer");
  const cancel = action("Cancel");
  const submit = action("Add card", "submit");
  submit.classList.add("lsw-create-submit");
  const inspect = action("Check result");
  footer.append(cancel, submit, inspect);
  form.append(header, fields, footer);
  dialog.append(form);

  function change(patch: Partial<CreateDraft>): void {
    props.onDraftChange({ ...props.draft, ...patch });
  }

  function replaceRow(row: RelationshipRow, patch: Partial<CreateRelationshipDraft>): void {
    const index = rows.indexOf(row);
    change({
      relationships: props.draft.relationships.map((value, position) =>
        index === position ? { ...value, ...patch } : value,
      ),
    });
  }

  function appendRow(): RelationshipRow {
    const node = element("div", "lsw-create-relation");
    const kind = element("select", "lsw-create-control");
    kind.setAttribute("aria-label", "Relationship type");
    options(
      kind,
      relationshipChoices.map(({ label }, index) => ({ label, value: String(index) })),
    );
    const target = element("select", "lsw-create-control");
    target.setAttribute("aria-label", "Related card");
    options(target, targetOptions);
    const remove = action("Remove");
    const row = { node, kind, target, remove };
    kind.addEventListener(
      "change",
      () => {
        const selected = relationshipChoices[kind.selectedIndex];
        if (selected !== undefined)
          replaceRow(row, { kind: selected.kind, direction: selected.direction });
      },
      listeners,
    );
    target.addEventListener(
      "change",
      () => replaceRow(row, { targetCardId: target.value }),
      listeners,
    );
    remove.addEventListener(
      "click",
      () => {
        const index = rows.indexOf(row);
        rows.splice(index, 1);
        row.node.remove();
        change({
          relationships: props.draft.relationships.filter((_, position) => index !== position),
        });
      },
      listeners,
    );
    node.append(kind, target, remove);
    rowHost.append(node);
    return row;
  }

  function dismiss(): void {
    if (props.phase !== "submitting") props.onCancel();
  }

  title.addEventListener(
    "input",
    () => {
      title.setCustomValidity("");
      change({ title: title.value });
    },
    listeners,
  );
  priority.addEventListener("change", () => change({ priorityId: priority.value }), listeners);
  owner.addEventListener("change", () => change({ ownerId: owner.value }), listeners);
  description.addEventListener(
    "input",
    () => change({ description: description.value }),
    listeners,
  );
  addRelationship.addEventListener(
    "click",
    () => {
      const first = props.targets[0];
      if (first !== undefined) {
        change({
          relationships: [
            ...props.draft.relationships,
            { kind: "blocks", direction: "outgoing", targetCardId: first.id },
          ],
        });
      }
    },
    listeners,
  );
  cancel.addEventListener("click", dismiss, listeners);
  dialog.addEventListener(
    "cancel",
    (event) => {
      if (event.target !== dialog) return;
      event.preventDefault();
      dismiss();
    },
    listeners,
  );
  inspect.addEventListener("click", () => props.onInspect(), listeners);
  form.addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      if (props.phase !== "editing") return;
      if (!/[^\p{White_Space}]/u.test(title.value)) {
        title.setCustomValidity("Title is required.");
        title.reportValidity();
        return;
      }
      props.onSubmit();
    },
    listeners,
  );

  function update(next: CreateProps): void {
    const feedbackChanged = next.error !== props.error;
    props = next;
    const editing = props.phase === "editing";
    subtitle.textContent = `It will be created in ${props.laneName}, by ${props.actorName}.`;
    if (priorities !== props.priorities) {
      priorities = props.priorities;
      options(
        priority,
        priorities.map(({ id, key, label }) => ({
          value: id,
          label: `${key.toUpperCase()} ${label}`,
        })),
      );
    }
    if (owners !== props.owners) {
      owners = props.owners;
      options(
        owner,
        owners.map(({ id, label }) => ({ value: id, label })),
      );
    }
    if (targets !== props.targets) {
      targets = props.targets;
      targetOptions = targets.map(({ id, ticket, title: cardTitle }) => ({
        value: id,
        label: `#${String(ticket).padStart(4, "0")} ${cardTitle}`,
      }));
      for (const row of rows) options(row.target, targetOptions);
    }
    const draft = props.draft;
    if (title.value !== draft.title) {
      title.value = draft.title;
      title.setCustomValidity("");
    }
    if (description.value !== draft.description) description.value = draft.description;
    priority.value = draft.priorityId;
    owner.value = draft.ownerId;
    for (const control of [title, description, priority, owner]) control.disabled = !editing;
    while (rows.length > draft.relationships.length) rows.pop()?.node.remove();
    for (const [index, relation] of draft.relationships.entries()) {
      let row = rows[index];
      if (row === undefined) {
        row = appendRow();
        rows.push(row);
      }
      row.kind.selectedIndex = relationshipChoices.findIndex(
        ({ kind, direction }) => relation.kind === kind && relation.direction === direction,
      );
      row.target.value = relation.targetCardId;
      for (const control of [row.kind, row.target, row.remove]) control.disabled = !editing;
    }
    addRelationship.disabled = !editing || targetOptions.length === 0;
    cancel.disabled = props.phase === "submitting";
    submit.disabled = !editing || props.writable === false;
    inspect.hidden = props.phase !== "unknown";
    feedback.hidden = props.error === null;
    feedback.textContent = props.error;
    if (feedbackChanged && !feedback.hidden && dialog.open)
      feedback.scrollIntoView({ block: "nearest" });

    const attachmentProps = { form, disabled: !editing };
    if (attachmentMount !== props.attachment) {
      attachments?.dispose();
      attachmentHost.replaceChildren();
      attachmentMount = props.attachment;
      attachments = attachmentMount?.(attachmentHost, attachmentProps);
    }
    if (attachments === undefined && attachmentField.parentElement !== attachmentHost) {
      attachmentHost.append(attachmentField);
    }
    attachments?.update(attachmentProps);
  }

  host.append(dialog);
  update(initial);
  dialog.showModal();
  return {
    update,
    dispose() {
      lifetime.abort();
      attachments?.dispose();
      dialog.remove();
    },
  };
};
