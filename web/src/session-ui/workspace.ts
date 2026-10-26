import type { SessionRead } from "../../generated/transport/contracts";
import type { OriginalOutcome } from "../api/client";
import { BoardView } from "../board/view";
import { RecoveryView, element } from "../recovery-ui/view";
import { RecoveryAdminView } from "../recovery-ui/admin";
import type { RecoveryAdminClient } from "../recovery-ui/admin";
import { ArchiveView } from "../archive-ui/view";
import type { ArchiveClient } from "../archive-ui/view";

// Composition stays separate from entry/assets. No automatic original lookup,
// recovery confirmation, attachment replay, export, restore, or native bootstrap.
export class SessionWorkspace {
  private readonly board: BoardView;
  private readonly recovery: RecoveryView;
  private readonly admin: RecoveryAdminView;
  private readonly archive: ArchiveView;
  constructor(private readonly root: HTMLElement, client: RecoveryAdminClient & ArchiveClient, session: SessionRead) {
    const board = element("section"), recovery = element("section"), admin = element("section"), archive = element("section");
    root.replaceChildren(board, recovery, admin, archive);
    this.board = new BoardView(board, client, session.grant.boardId);
    const observe = (outcome: OriginalOutcome): void => this.board.observeOutcome(outcome);
    this.recovery = new RecoveryView(recovery, client, observe);
    this.admin = new RecoveryAdminView(admin, client, session.grant.boardId, observe);
    this.archive = new ArchiveView(archive, client, session.grant.boardId, observe);
    void this.board.open(); void this.recovery.open();
  }
  destroy(): void { this.board.destroy(); this.recovery.destroy(); this.admin.destroy(); this.archive.destroy(); this.root.replaceChildren(); }
}
