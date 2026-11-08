import { sameIdentity } from "./hierarchy.js";
export class RecipientRefusal extends Error {
    code;
    constructor(code) {
        super("Recipient request refused");
        this.code = code;
    }
}
export class RecipientUnknown extends Error {
    constructor() { super("Recipient request outcome UNKNOWN. Inspect status; do not submit again."); }
}
export function sameRecipientInput(a, b) {
    return a.version === b.version && a.policy === b.policy && a.recipient.kind === b.recipient.kind && a.recipient.threadId === b.recipient.threadId;
}
// Generated shape/scalar validation precedes these semantic/correlation checks.
export function validateRecipient(view, identity, command) {
    if (!sameIdentity(view.identity, identity))
        throw Error("Recipient identity mismatch");
    if (view.state === "configured") {
        if (view.reason !== null || view.configurationRevision !== 1 || view.configurationGeneration === null || view.requestId === null || view.input === null)
            throw Error("Recipient configured mismatch");
    }
    else if (view.configurationRevision !== 0 || view.configurationGeneration !== null || view.requestId !== null || view.input !== null ||
        (view.state === "unconfigured" ? view.reason !== null : view.reason === null))
        throw Error("Recipient redaction mismatch");
    if (command.action === "configure" && (view.state !== "configured" || view.runtimeGeneration !== command.expectedRuntimeGeneration ||
        view.requestId !== command.requestId || view.input === null || !sameRecipientInput(view.input, command.input)))
        throw new RecipientUnknown();
}
