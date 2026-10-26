// Private local recovery storage. These are client records, never authority.
import type { Command, RequestAttachmentAdd } from "../../generated/contracts.js";
import type { MutationRequest, Terminal } from "../../generated/transport/contracts.js";

export type Context = Readonly<{
  installationId: string;
  boardId: string;
  incarnation: string;
  actorId: string;
}>;

export type Draft = Readonly<{
  key: string;
  context: Context;
  draftId: string;
  revision: number;
  editedAt: number;
  // An editor's prose/input serialization, not a session or credential DTO.
  input: string;
  forkedFrom: string | null;
}>;

export type PreparedEvidence = Readonly<{
  context: Context;
  draftId: string;
  command: Command;
  requestBytes: string;
  canonicalBytes: string;
  fingerprintVersion: 1;
  fingerprint: string;
  attachment: RequestAttachmentAdd["input"] | null;
}>;

// Future UI composition must supply this seam from the authoritative contracts.
// Both methods must validate exact schemas (including unknown/duplicate keys),
// Unicode and semantic request/canonical correspondence. A type assertion, HTTP
// status or a hash alone is not a verifier. No production provider ships here.
export interface BoundaryVerifier {
  prepared(evidence: PreparedEvidence, request: MutationRequest): Promise<boolean>;
  terminal(evidence: PreparedEvidence, terminal: Terminal): Promise<boolean>;
}

export type Submission = Readonly<{
  key: string;
  evidence: PreparedEvidence;
  opId: string;
  insertedAt: number;
}>;

// Append-only terminal classification. No result prose, token or arbitrary body.
export type TerminalStatus = Readonly<{
  key: string;
  outcome: "committed" | "not_committed";
  terminalAt: string;
  recordedAt: number;
}>;

export type Snapshot = Readonly<{
  drafts: readonly Draft[];
  submissions: readonly Submission[];
  statuses: readonly TerminalStatus[];
}>;

export type Changes<T> = {
  value: T;
  draftPuts?: readonly Draft[];
  submissionAdds?: readonly Submission[];
  statusAdds?: readonly TerminalStatus[];
  draftDeletes?: readonly string[];
  submissionDeletes?: readonly string[];
  statusDeletes?: readonly string[];
};

// A successful commit MUST mean transaction COMPLETE, never request success.
// Change runs synchronously inside one serial readwrite transaction over all
// stores. No awaits or external work may occur inside it. Reads are fresh txs.
export interface RecoveryDriver {
  read(): Promise<Snapshot>;
  commit<T>(change: (snapshot: Snapshot) => Changes<T>): Promise<T>;
}

export type ProfileQualification = Readonly<{
  installationId: string;
  profileId: string;
  strictDurabilityQualified: true;
  evidenceReference: string;
}>;

export type ClearAcknowledgement = Readonly<{
  discardPrivateProse: true;
  unresolvedMayBecomeUnknowable?: true;
}>;

export class RecoveryError extends Error {
  constructor(readonly code: "invalid" | "unavailable" | "unqualified" | "boundary-unavailable" |
    "capacity" | "collision" | "mismatch" | "readback" | "warning-required") {
    super(`Recovery ${code}; retain the caller's input and original identity.`);
    this.name = "RecoveryError";
  }
}
