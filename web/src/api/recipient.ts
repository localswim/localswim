import type { Identity } from "../../generated/archive/contracts.js";
import type { RecipientInput, RecipientView } from "../../generated/transport/contracts.js";
import { sameIdentity } from "./hierarchy.js";

export type RecipientCommand = Readonly<{ action: "status" }> | Readonly<{
  action: "configure"; expectedRuntimeGeneration: string; expectedConfigurationRevision: 0;
  requestId: string; input: RecipientInput;
}>;
export class RecipientRefusal extends Error {
  constructor(readonly code: string) { super("Recipient request refused"); }
}
export class RecipientUnknown extends Error {
  constructor() { super("Recipient request outcome UNKNOWN. Inspect status; do not submit again."); }
}
export function sameRecipientInput(a: RecipientInput, b: RecipientInput): boolean {
  return a.version === b.version && a.policy === b.policy && a.recipient.kind === b.recipient.kind && a.recipient.threadId === b.recipient.threadId;
}
// Generated shape/scalar validation precedes these semantic/correlation checks.
export function validateRecipient(view: RecipientView, identity: Identity, command: RecipientCommand): void {
  if (!sameIdentity(view.identity, identity)) throw Error("Recipient identity mismatch");
  if (view.state === "configured") {
    if (view.reason !== null || view.configurationRevision !== 1 || view.configurationGeneration === null || view.requestId === null || view.input === null) throw Error("Recipient configured mismatch");
  } else if (view.configurationRevision !== 0 || view.configurationGeneration !== null || view.requestId !== null || view.input !== null ||
      (view.state === "unconfigured" ? view.reason !== null : view.reason === null)) throw Error("Recipient redaction mismatch");
  if (command.action === "configure" && (view.state !== "configured" || view.runtimeGeneration !== command.expectedRuntimeGeneration ||
      view.requestId !== command.requestId || view.input === null || !sameRecipientInput(view.input, command.input))) throw new RecipientUnknown();
}
