import type { SessionRead } from "../../generated/transport/contracts.js";
import type { OriginalOutcome } from "../api/client.js";
import { BoardView } from "../board/view.js";
import type { BoardViewOptions } from "../board/view.js";
import { RecoveryView, element } from "../recovery-ui/view.js";
import { RecoveryAdminView } from "../recovery-ui/admin.js";
import type { RecoveryAdminClient } from "../recovery-ui/admin.js";
import { ArchiveView } from "../archive-ui/view.js";
import type { ArchiveClient } from "../archive-ui/view.js";
import type { EditorReference } from "./window-state.js";

// Composition stays separate from entry/assets. No automatic original lookup,
// recovery confirmation, attachment replay, export, restore, or native bootstrap.
export class SessionWorkspace {
  readonly ready: Promise<void>;
  private readonly board: BoardView;
  private readonly recovery: RecoveryView;
  private readonly admin: RecoveryAdminView | undefined;
  private readonly archive: ArchiveView;
  constructor(private readonly root: HTMLElement, client: RecoveryAdminClient & ArchiveClient, session: SessionRead, options: BoardViewOptions & Readonly<{ local?: boolean; editor?: EditorReference; checkpoint?: (reference?: EditorReference) => void; recovery?: EditorReference; recoveryCheckpoint?: (reference?: EditorReference) => void }> = {}) {
    const board = element("section"), recovery = element("section"), admin = element("section"), archive = element("section");
    root.replaceChildren(board, recovery, admin, archive);
    this.board = new BoardView(board, client, session.grant.boardId, options.checkpoint, options);
    const observe = (outcome: OriginalOutcome): void => this.board.observeOutcome(outcome);
    this.recovery = new RecoveryView(recovery, client, observe, options.recoveryCheckpoint);
    if (!options.local) this.admin = new RecoveryAdminView(admin, client, session.grant.boardId, observe);
    else admin.remove();
    this.archive = new ArchiveView(archive, client, session.grant.boardId, observe);
    this.ready = Promise.all([
      this.board.open().then(() => options.editor ? this.board.restore(options.editor) : undefined),
      this.recovery.open().then(() => options.recovery ? this.recovery.restore(options.recovery) : undefined),
    ]).then(() => {});
  }
  suspend(): void { this.board.suspend(); this.recovery.suspend(); this.archive.suspend(); }
  resume(): void { this.board.resume(); this.archive.resume(); }
  activate(): void { this.root.querySelector<HTMLElement>('form input, form textarea, form select, textarea[aria-label="Retained private draft text"]')?.focus({ preventScroll: true }); }
  get canLeave(): boolean { return this.board.canLeave && this.recovery.canLeave && this.archive.canLeave; }
  async flushPrivateInput(): Promise<boolean> { await Promise.all([this.board.flushPrivateInput(), this.recovery.flushPrivateInput(), this.archive.flushPrivateInput()]); return this.canLeave; }
  discardInput(): void { this.board.discardInput(); this.recovery.discardInput(); this.archive.discardInput(); }
  destroy(): void { this.board.destroy(); this.recovery.destroy(); this.admin?.destroy(); this.archive.destroy(); this.root.replaceChildren(); }
}
