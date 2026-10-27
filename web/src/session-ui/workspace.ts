import type { SessionRead } from "../../generated/transport/contracts.js";
import type { OriginalOutcome } from "../api/client.js";
import { BoardView } from "../board/view.js";
import { RecoveryView, element } from "../recovery-ui/view.js";
import { RecoveryAdminView } from "../recovery-ui/admin.js";
import type { RecoveryAdminClient } from "../recovery-ui/admin.js";
import { ArchiveView } from "../archive-ui/view.js";
import type { ArchiveClient } from "../archive-ui/view.js";

// Composition stays separate from entry/assets. No automatic original lookup,
// recovery confirmation, attachment replay, export, restore, or native bootstrap.
export class SessionWorkspace {
  private readonly board: BoardView;
  private readonly recovery: RecoveryView;
  private readonly admin: RecoveryAdminView | undefined;
  private readonly archive: ArchiveView;
  constructor(private readonly root: HTMLElement, client: RecoveryAdminClient & ArchiveClient, session: SessionRead, options: Readonly<{ local?: boolean }> = {}) {
    const board = element("section"), recovery = element("section"), admin = element("section"), archive = element("section");
    root.replaceChildren(board, recovery, admin, archive);
    this.board = new BoardView(board, client, session.grant.boardId);
    const observe = (outcome: OriginalOutcome): void => this.board.observeOutcome(outcome);
    this.recovery = new RecoveryView(recovery, client, observe);
    if (!options.local) this.admin = new RecoveryAdminView(admin, client, session.grant.boardId, observe);
    else admin.remove();
    this.archive = new ArchiveView(archive, client, session.grant.boardId, observe);
    void this.board.open(); void this.recovery.open();
  }
  destroy(): void { this.board.destroy(); this.recovery.destroy(); this.admin?.destroy(); this.archive.destroy(); this.root.replaceChildren(); }
}
