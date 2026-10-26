package schema

// Registry rows preserve typed entity existence after authorized compaction.
// A tombstone is never made merely to satisfy an attempted refused reference.
const ddl = `CREATE TABLE board (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 board_id TEXT NOT NULL CHECK({{u:board_id}}),
 incarnation TEXT NOT NULL UNIQUE CHECK({{u:incarnation}}),
 schema_version INTEGER NOT NULL DEFAULT 3 CHECK(schema_version=3),
 revision INTEGER NOT NULL DEFAULT 0 CHECK({{n:revision}}),
 next_ticket INTEGER NOT NULL DEFAULT 1 CHECK({{p:next_ticket}}),
 next_event INTEGER NOT NULL DEFAULT 1 CHECK({{p:next_event}}),
 retention_version INTEGER NOT NULL DEFAULT 1 CHECK({{p:retention_version}}),
 history_days INTEGER NOT NULL DEFAULT 90 CHECK(history_days BETWEEN 1 AND 3650),
 activity_days INTEGER NOT NULL DEFAULT 365 CHECK(activity_days BETWEEN 1 AND 3650),
 outcome_days INTEGER NOT NULL DEFAULT 180 CHECK(outcome_days BETWEEN 1 AND 3650),
 history_floor TEXT CHECK(history_floor IS NULL OR {{t:history_floor}}),
 activity_floor TEXT CHECK(activity_floor IS NULL OR {{t:activity_floor}}),
 outcome_floor TEXT CHECK(outcome_floor IS NULL OR {{t:outcome_floor}})
) STRICT;
CREATE TABLE policy (
 incarnation TEXT PRIMARY KEY REFERENCES board(incarnation),
 policy_version INTEGER NOT NULL DEFAULT 1 CHECK(policy_version=1)
) STRICT;
CREATE TABLE actor (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), actor_id TEXT NOT NULL CHECK({{u:actor_id}}),
 kind TEXT NOT NULL CHECK(kind IN ('human','automation')), label TEXT NOT NULL,
 origin_board TEXT NOT NULL CHECK({{u:origin_board}}), origin_incarnation TEXT NOT NULL CHECK({{u:origin_incarnation}}),
 PRIMARY KEY(incarnation,actor_id)
) STRICT;
CREATE TABLE grant_fact (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), grant_id TEXT NOT NULL CHECK({{u:grant_id}}),
 PRIMARY KEY(incarnation,grant_id)
) STRICT;
CREATE TABLE grant_record (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), grant_id TEXT NOT NULL CHECK({{u:grant_id}}),
 actor_id TEXT NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 PRIMARY KEY(incarnation,grant_id), UNIQUE(incarnation,grant_id,actor_id), FOREIGN KEY(incarnation,grant_id) REFERENCES grant_fact(incarnation,grant_id), FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id)
) STRICT;
CREATE TABLE cli_credential (
 incarnation TEXT NOT NULL, board_id TEXT NOT NULL CHECK({{u:board_id}}),
 grant_id TEXT NOT NULL, actor_id TEXT NOT NULL,
 installation_id TEXT NOT NULL CHECK({{v4:installation_id}}),
 origin TEXT NOT NULL CHECK(length(CAST(origin AS BLOB))=length(origin) AND length(origin) BETWEEN 55 AND 56 AND substr(origin,1,8)='https://' AND substr(origin,9,32) NOT GLOB '*[^0-9a-f]*' AND substr(origin,41,11)='.localhost:' AND substr(origin,52) NOT GLOB '*[^0-9]*' AND CAST(substr(origin,52) AS INTEGER) BETWEEN 1024 AND 65535 AND CAST(CAST(substr(origin,52) AS INTEGER) AS TEXT)=substr(origin,52)),
 credential_id TEXT NOT NULL UNIQUE CHECK({{v4:credential_id}}),
 credential_hash TEXT NOT NULL UNIQUE CHECK(length(CAST(credential_hash AS BLOB))=64 AND {{h:credential_hash}}),
 issued_at TEXT NOT NULL CHECK(length(CAST(issued_at AS BLOB))=20 AND {{t:issued_at}}), expires_at TEXT NOT NULL CHECK(length(CAST(expires_at AS BLOB))=20 AND {{t:expires_at}}),
 PRIMARY KEY(incarnation,grant_id),
 FOREIGN KEY(incarnation,grant_id,actor_id) REFERENCES grant_record(incarnation,grant_id,actor_id),
 CHECK(unixepoch(expires_at)-unixepoch(issued_at)=2592000)
) STRICT;
CREATE TABLE grant_right (
 incarnation TEXT NOT NULL, grant_id TEXT NOT NULL,
 right_name TEXT NOT NULL CHECK(right_name IN ('read','read-own-outcome','create','edit','comment','attach','export','restore','review-handoff','approve','grant-admin','history-admin')),
 PRIMARY KEY(incarnation,grant_id,right_name), FOREIGN KEY(incarnation,grant_id) REFERENCES grant_record(incarnation,grant_id)
) STRICT;
CREATE TABLE entity (
 incarnation TEXT NOT NULL REFERENCES board(incarnation),
 kind TEXT NOT NULL CHECK(kind IN ('card','comment','link','attachment','approval')),
 entity_id TEXT NOT NULL CHECK({{u:entity_id}}), card_id TEXT NOT NULL CHECK({{u:card_id}}),
 origin_board TEXT NOT NULL CHECK({{u:origin_board}}), origin_incarnation TEXT NOT NULL CHECK({{u:origin_incarnation}}),
 removed_at TEXT CHECK(removed_at IS NULL OR {{t:removed_at}}),
 removal_reason TEXT CHECK(removal_reason IN ('cleared','expired')),
 creator_id TEXT, created_at TEXT CHECK(created_at IS NULL OR {{t:created_at}}),
 PRIMARY KEY(incarnation,kind,entity_id), UNIQUE(incarnation,kind,entity_id,card_id),
 CHECK((removed_at IS NULL)=(removal_reason IS NULL)), CHECK(kind<>'approval' OR removed_at IS NULL),
 CHECK(kind<>'card' OR entity_id=card_id),
 FOREIGN KEY(incarnation,creator_id) REFERENCES actor(incarnation,actor_id)
) STRICT;
CREATE TABLE ticket (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), ticket INTEGER NOT NULL CHECK({{p:ticket}}),
 card_id TEXT NOT NULL CHECK({{u:card_id}}), kind TEXT NOT NULL DEFAULT 'card' CHECK(kind='card'),
 PRIMARY KEY(incarnation,ticket), UNIQUE(incarnation,card_id),
 FOREIGN KEY(incarnation,kind,card_id) REFERENCES entity(incarnation,kind,entity_id)
) STRICT;
CREATE TABLE card (
 incarnation TEXT NOT NULL, card_id TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'card' CHECK(kind='card'),
 revision INTEGER NOT NULL CHECK({{p:revision}}), title TEXT NOT NULL CHECK({{l:title}} BETWEEN 1 AND 200),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=65536),
 priority TEXT NOT NULL DEFAULT 'P3' CHECK(priority IN ('P0','P1','P2','P3','P4','P5')),
 lane TEXT NOT NULL DEFAULT 'Ready for Work' CHECK(lane IN ('Ready for Work','In Progress','Ready for Review','Completed')),
 archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1)), current_approval_id TEXT,
 approval_kind TEXT NOT NULL DEFAULT 'approval' CHECK(approval_kind='approval'),
 PRIMARY KEY(incarnation,card_id), FOREIGN KEY(incarnation,kind,card_id) REFERENCES entity(incarnation,kind,entity_id),
 FOREIGN KEY(incarnation,card_id) REFERENCES ticket(incarnation,card_id),
 FOREIGN KEY(incarnation,card_id,current_approval_id) REFERENCES approval(incarnation,card_id,approval_id) DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE TABLE comment (
 incarnation TEXT NOT NULL, comment_id TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'comment' CHECK(kind='comment'),
 card_id TEXT NOT NULL, actor_id TEXT NOT NULL, text TEXT NOT NULL CHECK(length(CAST(text AS BLOB)) BETWEEN 1 AND 65536),
 created_at TEXT NOT NULL CHECK({{t:created_at}}),
 PRIMARY KEY(incarnation,comment_id), FOREIGN KEY(incarnation,kind,comment_id,card_id) REFERENCES entity(incarnation,kind,entity_id,card_id),
 FOREIGN KEY(incarnation,card_id) REFERENCES card(incarnation,card_id), FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id)
) STRICT;
CREATE TABLE link (
 incarnation TEXT NOT NULL, link_id TEXT NOT NULL, entity_kind TEXT NOT NULL DEFAULT 'link' CHECK(entity_kind='link'),
 card_id TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('blocking','informational')),
 target_board TEXT NOT NULL CHECK({{u:target_board}}), target_incarnation TEXT NOT NULL CHECK({{u:target_incarnation}}),
 target_card_id TEXT NOT NULL CHECK({{u:target_card_id}}), internal_target_id TEXT,
 active INTEGER NOT NULL CHECK(active IN (0,1)), created_at TEXT NOT NULL CHECK({{t:created_at}}),
 PRIMARY KEY(incarnation,link_id), FOREIGN KEY(incarnation,entity_kind,link_id,card_id) REFERENCES entity(incarnation,kind,entity_id,card_id),
 FOREIGN KEY(incarnation,card_id) REFERENCES card(incarnation,card_id),
 FOREIGN KEY(incarnation,internal_target_id) REFERENCES card(incarnation,card_id),
 CHECK(internal_target_id IS NULL OR (internal_target_id=target_card_id AND target_incarnation=incarnation AND card_id<>target_card_id))
) STRICT;
CREATE UNIQUE INDEX active_typed_pair ON link(incarnation,card_id,kind,target_board,target_incarnation,target_card_id) WHERE active=1;
CREATE TABLE approval (
 incarnation TEXT NOT NULL, approval_id TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'approval' CHECK(kind='approval'),
 card_id TEXT NOT NULL, actor_id TEXT NOT NULL, reviewed_card_revision INTEGER NOT NULL CHECK({{p:reviewed_card_revision}}),
 reviewed_board_revision INTEGER NOT NULL CHECK({{n:reviewed_board_revision}}), policy_version INTEGER NOT NULL CHECK(policy_version=1),
 created_at TEXT NOT NULL CHECK({{t:created_at}}), PRIMARY KEY(incarnation,approval_id), UNIQUE(incarnation,card_id,approval_id),
 FOREIGN KEY(incarnation,kind,approval_id,card_id) REFERENCES entity(incarnation,kind,entity_id,card_id),
 FOREIGN KEY(incarnation,card_id) REFERENCES card(incarnation,card_id), FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id)
) STRICT;
CREATE TABLE blob (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), sha256 TEXT NOT NULL CHECK({{h:sha256}}),
 byte_length INTEGER NOT NULL CHECK(byte_length BETWEEN 0 AND 10485760), bytes BLOB NOT NULL,
 PRIMARY KEY(incarnation,sha256), UNIQUE(incarnation,sha256,byte_length), CHECK(length(bytes)=byte_length)
) STRICT;
CREATE TABLE attachment (
 incarnation TEXT NOT NULL, attachment_id TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'attachment' CHECK(kind='attachment'),
 card_id TEXT NOT NULL, name TEXT NOT NULL CHECK(length(CAST(name AS BLOB))<=255),
 media_label TEXT NOT NULL CHECK(length(CAST(media_label AS BLOB))<=127 AND media_label NOT GLOB '*[^ -~]*'),
 sha256 TEXT NOT NULL CHECK({{h:sha256}}), byte_length INTEGER NOT NULL CHECK(byte_length BETWEEN 0 AND 10485760),
 created_at TEXT NOT NULL CHECK({{t:created_at}}), PRIMARY KEY(incarnation,attachment_id),
 FOREIGN KEY(incarnation,kind,attachment_id,card_id) REFERENCES entity(incarnation,kind,entity_id,card_id),
 FOREIGN KEY(incarnation,card_id) REFERENCES card(incarnation,card_id)
) STRICT;
CREATE TABLE current_attachment_pin (
 incarnation TEXT NOT NULL, attachment_id TEXT NOT NULL, sha256 TEXT NOT NULL, byte_length INTEGER NOT NULL,
 PRIMARY KEY(incarnation,attachment_id), FOREIGN KEY(incarnation,attachment_id) REFERENCES attachment(incarnation,attachment_id),
 FOREIGN KEY(incarnation,sha256,byte_length) REFERENCES blob(incarnation,sha256,byte_length)
) STRICT;
CREATE TABLE current_reference (
 incarnation TEXT NOT NULL, card_id TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('comment','link','attachment')),
 entity_id TEXT NOT NULL, PRIMARY KEY(incarnation,card_id,kind,entity_id),
 FOREIGN KEY(incarnation,card_id) REFERENCES card(incarnation,card_id),
 FOREIGN KEY(incarnation,kind,entity_id,card_id) REFERENCES entity(incarnation,kind,entity_id,card_id)
) STRICT;
CREATE TABLE card_revision (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), origin_board TEXT NOT NULL CHECK({{u:origin_board}}),
 origin_incarnation TEXT NOT NULL CHECK({{u:origin_incarnation}}), card_id TEXT NOT NULL,
 card_revision INTEGER NOT NULL CHECK({{p:card_revision}}), actor_id TEXT NOT NULL, committed_at TEXT NOT NULL CHECK({{t:committed_at}}),
 state TEXT CHECK(state IS NULL OR json_valid(state)),
 removed_at TEXT CHECK(removed_at IS NULL OR {{t:removed_at}}), removal_reason TEXT CHECK(removal_reason IN ('cleared','expired')),
 removal_revision INTEGER CHECK(removal_revision IS NULL OR {{p:removal_revision}}),
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,card_id,card_revision),
 FOREIGN KEY(incarnation,card_id) REFERENCES card(incarnation,card_id), FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id),
 CHECK((state IS NULL)=(removed_at IS NOT NULL)), CHECK((removed_at IS NULL)=(removal_reason IS NULL)), CHECK((removed_at IS NULL)=(removal_revision IS NULL))
) STRICT;
CREATE TABLE revision_reference (
 incarnation TEXT NOT NULL, origin_board TEXT NOT NULL, origin_incarnation TEXT NOT NULL, card_id TEXT NOT NULL, card_revision INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('comment','link','attachment','approval')), entity_id TEXT NOT NULL,
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,card_id,card_revision,kind,entity_id),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,card_id,card_revision) REFERENCES card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision),
 FOREIGN KEY(incarnation,kind,entity_id,card_id) REFERENCES entity(incarnation,kind,entity_id,card_id)
) STRICT;
CREATE TABLE history_blob_pin (
 incarnation TEXT NOT NULL, origin_board TEXT NOT NULL, origin_incarnation TEXT NOT NULL, card_id TEXT NOT NULL, card_revision INTEGER NOT NULL,
 attachment_id TEXT NOT NULL, sha256 TEXT NOT NULL, byte_length INTEGER NOT NULL,
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,card_id,card_revision,attachment_id),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,card_id,card_revision) REFERENCES card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision),
 FOREIGN KEY(incarnation,attachment_id) REFERENCES attachment(incarnation,attachment_id),
 FOREIGN KEY(incarnation,sha256,byte_length) REFERENCES blob(incarnation,sha256,byte_length)
) STRICT;
CREATE TABLE operation (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), origin_board TEXT NOT NULL CHECK({{u:origin_board}}),
 origin_incarnation TEXT NOT NULL CHECK({{u:origin_incarnation}}), op_id TEXT NOT NULL CHECK({{u:op_id}}),
 current_ledger INTEGER NOT NULL CHECK(current_ledger IN (0,1)), actor_id TEXT NOT NULL,
 fingerprint_version INTEGER NOT NULL DEFAULT 1 CHECK(fingerprint_version=1), fingerprint TEXT NOT NULL CHECK({{h:fingerprint}}),
 command TEXT NOT NULL CHECK(command IN ('card.create','card.edit','comment.add','attachment.add','link.add','link.remove','review-handoff','approve','grant.create','grant.revoke','history.clear','retention.set')),
 target_kind TEXT CHECK(target_kind IN ('card','grant')), target_id TEXT CHECK(target_id IS NULL OR {{u:target_id}}),
 expected_revision INTEGER NOT NULL CHECK({{n:expected_revision}}), expected_card_revision INTEGER CHECK(expected_card_revision IS NULL OR {{p:expected_card_revision}}),
 terminal_class TEXT NOT NULL CHECK(terminal_class IN ('COMMITTED','NOT_COMMITTED')), code TEXT NOT NULL,
 terminal_at TEXT NOT NULL CHECK({{t:terminal_at}}), committed_revision INTEGER CHECK(committed_revision IS NULL OR {{p:committed_revision}}),
 committed_card_revision INTEGER CHECK(committed_card_revision IS NULL OR {{p:committed_card_revision}}),
 removed_at TEXT CHECK(removed_at IS NULL OR {{t:removed_at}}), removal_reason TEXT CHECK(removal_reason IN ('cleared','expired')),
 removal_revision INTEGER CHECK(removal_revision IS NULL OR {{p:removal_revision}}),
 committed_target_kind TEXT GENERATED ALWAYS AS (CASE WHEN terminal_class='COMMITTED' AND target_kind='card' THEN 'card' END) STORED,
 committed_target_card TEXT GENERATED ALWAYS AS (CASE WHEN terminal_class='COMMITTED' AND target_kind='card' THEN target_id END) STORED,
 committed_target_grant TEXT GENERATED ALWAYS AS (CASE WHEN terminal_class='COMMITTED' AND target_kind='grant' THEN target_id END) STORED,
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,op_id),
 FOREIGN KEY(incarnation,committed_target_kind,committed_target_card) REFERENCES entity(incarnation,kind,entity_id),
 FOREIGN KEY(incarnation,committed_target_grant) REFERENCES grant_fact(incarnation,grant_id), FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id),
 CHECK((target_kind IS NULL)=(target_id IS NULL)),
 CHECK(CASE WHEN command IN ('card.create','grant.create','history.clear','retention.set') THEN target_kind IS NULL AND expected_card_revision IS NULL WHEN command='grant.revoke' THEN target_kind IS 'grant' AND expected_card_revision IS NULL ELSE target_kind IS 'card' AND expected_card_revision IS NOT NULL END),
 CHECK((terminal_class='COMMITTED' AND code='OK' AND committed_revision IS NOT NULL AND ((target_kind IS 'card' OR command='card.create')=(committed_card_revision IS NOT NULL))) OR (terminal_class='NOT_COMMITTED' AND code<>'OK' AND committed_revision IS NULL AND committed_card_revision IS NULL)),
 CHECK((removed_at IS NULL)=(removal_reason IS NULL)), CHECK((removed_at IS NULL)=(removal_revision IS NULL))
) STRICT;
CREATE UNIQUE INDEX live_operation_identity ON operation(incarnation,op_id) WHERE current_ledger=1;
CREATE TABLE operation_full (
 incarnation TEXT NOT NULL, origin_board TEXT NOT NULL, origin_incarnation TEXT NOT NULL, op_id TEXT NOT NULL,
 intent TEXT NOT NULL CHECK(json_valid(intent)), result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,op_id),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,op_id) REFERENCES operation(incarnation,origin_board,origin_incarnation,op_id)
) STRICT;
CREATE TABLE operation_reference (
 incarnation TEXT NOT NULL, origin_board TEXT NOT NULL, origin_incarnation TEXT NOT NULL, op_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('board','card','comment','link','attachment','approval','grant')),
 entity_id TEXT NOT NULL CHECK({{u:entity_id}}), revision INTEGER CHECK(revision IS NULL OR {{n:revision}}),
 entity_kind TEXT GENERATED ALWAYS AS (CASE WHEN kind IN ('card','comment','link','attachment','approval') THEN kind END) STORED,
 grant_id TEXT GENERATED ALWAYS AS (CASE WHEN kind='grant' THEN entity_id END) STORED,
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id),
 FOREIGN KEY(incarnation,entity_kind,entity_id) REFERENCES entity(incarnation,kind,entity_id),
 FOREIGN KEY(incarnation,grant_id) REFERENCES grant_fact(incarnation,grant_id),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,op_id) REFERENCES operation(incarnation,origin_board,origin_incarnation,op_id)
) STRICT;
CREATE TABLE byte_fact (
 incarnation TEXT NOT NULL, origin_board TEXT NOT NULL, origin_incarnation TEXT NOT NULL, op_id TEXT NOT NULL,
 sha256 TEXT NOT NULL CHECK({{h:sha256}}), byte_length INTEGER NOT NULL CHECK(byte_length BETWEEN 0 AND 10485760),
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,op_id), UNIQUE(incarnation,origin_board,origin_incarnation,op_id,sha256,byte_length),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,op_id) REFERENCES operation(incarnation,origin_board,origin_incarnation,op_id)
) STRICT;
CREATE TABLE operation_blob_pin (
 incarnation TEXT NOT NULL, origin_board TEXT NOT NULL, origin_incarnation TEXT NOT NULL, op_id TEXT NOT NULL,
 attachment_id TEXT NOT NULL, sha256 TEXT NOT NULL, byte_length INTEGER NOT NULL,
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,op_id),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,op_id) REFERENCES operation_full(incarnation,origin_board,origin_incarnation,op_id),
 FOREIGN KEY(incarnation,origin_board,origin_incarnation,op_id,sha256,byte_length) REFERENCES byte_fact(incarnation,origin_board,origin_incarnation,op_id,sha256,byte_length) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(incarnation,attachment_id) REFERENCES attachment(incarnation,attachment_id),
 FOREIGN KEY(incarnation,sha256,byte_length) REFERENCES blob(incarnation,sha256,byte_length)
) STRICT;
CREATE TABLE activity (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), origin_board TEXT NOT NULL CHECK({{u:origin_board}}), origin_incarnation TEXT NOT NULL CHECK({{u:origin_incarnation}}),
 event_id INTEGER NOT NULL CHECK({{p:event_id}}), actor_id TEXT NOT NULL, op_id TEXT CHECK(op_id IS NULL OR {{u:op_id}}),
 type TEXT NOT NULL CHECK(type IN ('card.create','card.edit','comment.add','attachment.add','link.add','link.remove','review-handoff','approve','grant.create','grant.revoke','history.clear','retention.set','recovery.expire')),
 board_revision INTEGER NOT NULL CHECK({{n:board_revision}}), at TEXT NOT NULL CHECK({{t:at}}),
 affected TEXT NOT NULL CHECK(json_valid(affected)), PRIMARY KEY(incarnation,origin_board,origin_incarnation,event_id),
 FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id), CHECK((op_id IS NULL)=(type='recovery.expire'))
) STRICT;
CREATE TABLE coverage_gap (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), origin_board TEXT NOT NULL CHECK({{u:origin_board}}), origin_incarnation TEXT NOT NULL CHECK({{u:origin_incarnation}}),
 class TEXT NOT NULL CHECK(class IN ('history','activity','outcome_payload')), reason TEXT NOT NULL CHECK(reason IN ('cleared','expired')),
 from_exclusive TEXT CHECK(from_exclusive IS NULL OR {{t:from_exclusive}}), through_inclusive TEXT NOT NULL CHECK({{t:through_inclusive}}),
 recorded_at TEXT NOT NULL CHECK({{t:recorded_at}}), board_revision INTEGER NOT NULL CHECK({{p:board_revision}}),
 PRIMARY KEY(incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,board_revision), CHECK(from_exclusive IS NULL OR from_exclusive<=through_inclusive)
) STRICT;
CREATE TABLE recovery_preview (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), preview_hash TEXT NOT NULL CHECK({{h:preview_hash}}), actor_id TEXT NOT NULL,
 expected_revision INTEGER NOT NULL CHECK({{n:expected_revision}}), retention_version INTEGER NOT NULL CHECK({{p:retention_version}}),
 command TEXT NOT NULL CHECK(command IN ('history.clear','retention.set')), response TEXT NOT NULL CHECK(json_valid(response)),
 previewed_at TEXT NOT NULL CHECK({{t:previewed_at}}), expires_at TEXT NOT NULL CHECK({{t:expires_at}}),
 PRIMARY KEY(incarnation,preview_hash), FOREIGN KEY(incarnation,actor_id) REFERENCES actor(incarnation,actor_id),
 CHECK(unixepoch(expires_at)-unixepoch(previewed_at)=300)
) STRICT;
CREATE TABLE lineage (
 incarnation TEXT NOT NULL REFERENCES board(incarnation), source_board TEXT NOT NULL CHECK({{u:source_board}}), source_incarnation TEXT NOT NULL CHECK({{u:source_incarnation}}),
 manifest_sha256 TEXT NOT NULL CHECK({{h:manifest_sha256}}), revision INTEGER NOT NULL CHECK({{n:revision}}),
 PRIMARY KEY(incarnation,source_board,source_incarnation,manifest_sha256,revision)
) STRICT;
CREATE TRIGGER ticket_bound BEFORE INSERT ON ticket BEGIN SELECT CASE WHEN NEW.ticket>=(SELECT next_ticket FROM board WHERE incarnation=NEW.incarnation) THEN RAISE(ABORT,'ticket counter must advance before allocation') END; END;
CREATE TRIGGER ticket_immutable BEFORE UPDATE ON ticket BEGIN SELECT RAISE(ABORT,'ticket allocation is immutable'); END;
CREATE TRIGGER ticket_retained BEFORE DELETE ON ticket BEGIN SELECT RAISE(ABORT,'ticket allocation is retained'); END;
CREATE TRIGGER counters_monotone BEFORE UPDATE ON board BEGIN SELECT CASE WHEN NEW.board_id<>OLD.board_id OR NEW.incarnation<>OLD.incarnation OR NEW.revision<OLD.revision OR NEW.next_ticket<OLD.next_ticket OR NEW.next_event<OLD.next_event OR NEW.retention_version<OLD.retention_version THEN RAISE(ABORT,'identity or counter regression') END; END;
CREATE TRIGGER operation_origin BEFORE INSERT ON operation BEGIN SELECT CASE WHEN (NEW.current_ledger=1)<>EXISTS(SELECT 1 FROM board WHERE board_id=NEW.origin_board AND incarnation=NEW.origin_incarnation) THEN RAISE(ABORT,'operation authority mismatch') END; SELECT CASE WHEN NEW.terminal_class='COMMITTED' AND NEW.target_kind='card' AND NOT EXISTS(SELECT 1 FROM entity WHERE incarnation=NEW.incarnation AND kind='card' AND entity_id=NEW.target_id) THEN RAISE(ABORT,'missing committed target') END; SELECT CASE WHEN NEW.terminal_class='COMMITTED' AND NEW.target_kind='grant' AND NOT EXISTS(SELECT 1 FROM grant_fact WHERE incarnation=NEW.incarnation AND grant_id=NEW.target_id) THEN RAISE(ABORT,'missing committed grant') END; END;
CREATE TRIGGER operation_identity_immutable BEFORE UPDATE ON operation BEGIN SELECT CASE WHEN NEW.incarnation<>OLD.incarnation OR NEW.origin_board<>OLD.origin_board OR NEW.origin_incarnation<>OLD.origin_incarnation OR NEW.op_id<>OLD.op_id OR NEW.current_ledger<>OLD.current_ledger OR NEW.actor_id<>OLD.actor_id OR NEW.fingerprint<>OLD.fingerprint OR NEW.command<>OLD.command OR NEW.target_kind IS NOT OLD.target_kind OR NEW.target_id IS NOT OLD.target_id OR NEW.expected_revision<>OLD.expected_revision OR NEW.expected_card_revision IS NOT OLD.expected_card_revision OR NEW.terminal_class<>OLD.terminal_class OR NEW.code<>OLD.code OR NEW.terminal_at<>OLD.terminal_at OR NEW.committed_revision IS NOT OLD.committed_revision OR NEW.committed_card_revision IS NOT OLD.committed_card_revision THEN RAISE(ABORT,'terminal identity is immutable') END; END;
CREATE TRIGGER operation_retained BEFORE DELETE ON operation BEGIN SELECT RAISE(ABORT,'terminal identity is retained'); END;
CREATE TRIGGER result_reference BEFORE INSERT ON operation_reference BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM operation WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND op_id=NEW.op_id AND terminal_class='COMMITTED') THEN RAISE(ABORT,'refusal has no result references') END; SELECT CASE WHEN NEW.kind IN ('card','comment','link','attachment','approval') AND NOT EXISTS(SELECT 1 FROM entity WHERE incarnation=NEW.incarnation AND kind=NEW.kind AND entity_id=NEW.entity_id) THEN RAISE(ABORT,'missing committed result') END; SELECT CASE WHEN NEW.kind='board' AND NOT EXISTS(SELECT 1 FROM board WHERE incarnation=NEW.incarnation AND board_id=NEW.entity_id) THEN RAISE(ABORT,'missing board result') END; SELECT CASE WHEN NEW.kind='grant' AND NOT EXISTS(SELECT 1 FROM grant_fact WHERE incarnation=NEW.incarnation AND grant_id=NEW.entity_id) THEN RAISE(ABORT,'missing grant result') END; END;
CREATE TRIGGER operation_pin BEFORE INSERT ON operation_blob_pin BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM operation WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND op_id=NEW.op_id AND terminal_class='COMMITTED' AND command='attachment.add' AND removed_at IS NULL) THEN RAISE(ABORT,'only full committed attachments pin bytes') END; SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM attachment WHERE incarnation=NEW.incarnation AND attachment_id=NEW.attachment_id AND sha256=NEW.sha256 AND byte_length=NEW.byte_length) THEN RAISE(ABORT,'attachment pin mismatch') END; END;
CREATE TRIGGER operation_pin_fact BEFORE INSERT ON operation_blob_pin BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM byte_fact WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND op_id=NEW.op_id AND (sha256<>NEW.sha256 OR byte_length<>NEW.byte_length)) THEN RAISE(ABORT,'operation byte fact mismatch') END; END;
CREATE TRIGGER byte_fact_pin BEFORE INSERT ON byte_fact BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM operation_blob_pin WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND op_id=NEW.op_id AND (sha256<>NEW.sha256 OR byte_length<>NEW.byte_length)) THEN RAISE(ABORT,'operation byte fact mismatch') END; END;
CREATE TRIGGER current_pin BEFORE INSERT ON current_attachment_pin BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM attachment WHERE incarnation=NEW.incarnation AND attachment_id=NEW.attachment_id AND sha256=NEW.sha256 AND byte_length=NEW.byte_length) THEN RAISE(ABORT,'attachment pin mismatch') END; END;
CREATE TRIGGER byte_fact_length BEFORE INSERT ON byte_fact BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM blob WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) OR EXISTS(SELECT 1 FROM byte_fact WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) OR EXISTS(SELECT 1 FROM attachment WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) THEN RAISE(ABORT,'digest length disagreement') END; SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM operation WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND op_id=NEW.op_id AND command='attachment.add') THEN RAISE(ABORT,'unexpected byte fact') END; END;
CREATE TRIGGER reference_current_full BEFORE INSERT ON current_reference BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM entity WHERE incarnation=NEW.incarnation AND kind=NEW.kind AND entity_id=NEW.entity_id AND removed_at IS NULL) THEN RAISE(ABORT,'current reference cannot use tombstone') END; END;
CREATE TRIGGER reference_history_full BEFORE INSERT ON revision_reference BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM card_revision WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND card_id=NEW.card_id AND card_revision=NEW.card_revision AND state IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM entity WHERE incarnation=NEW.incarnation AND kind=NEW.kind AND entity_id=NEW.entity_id AND removed_at IS NULL) THEN RAISE(ABORT,'full history cannot use tombstone') END; END;
CREATE TRIGGER snapshot_earlier BEFORE INSERT ON card_revision BEGIN SELECT CASE WHEN NEW.card_revision>=(SELECT revision FROM card WHERE incarnation=NEW.incarnation AND card_id=NEW.card_id) THEN RAISE(ABORT,'snapshot must precede current revision') END; END;
CREATE TRIGGER tombstone_guard BEFORE UPDATE ON entity BEGIN SELECT CASE WHEN NEW.incarnation<>OLD.incarnation OR NEW.kind<>OLD.kind OR NEW.entity_id<>OLD.entity_id OR NEW.card_id<>OLD.card_id OR NEW.origin_board<>OLD.origin_board OR NEW.origin_incarnation<>OLD.origin_incarnation OR NEW.creator_id IS NOT OLD.creator_id OR NEW.created_at IS NOT OLD.created_at OR OLD.removed_at IS NOT NULL THEN RAISE(ABORT,'immutable entity identity or tombstone') END; SELECT CASE WHEN NEW.removed_at IS NOT NULL AND (NEW.kind IN ('card','approval') OR EXISTS(SELECT 1 FROM current_reference WHERE incarnation=OLD.incarnation AND kind=OLD.kind AND entity_id=OLD.entity_id) OR EXISTS(SELECT 1 FROM revision_reference r JOIN card_revision s USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) WHERE r.incarnation=OLD.incarnation AND r.kind=OLD.kind AND r.entity_id=OLD.entity_id AND s.state IS NOT NULL)) THEN RAISE(ABORT,'protected full entity') END; END;
CREATE TRIGGER history_pin_full BEFORE INSERT ON history_blob_pin BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM card_revision WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND card_id=NEW.card_id AND card_revision=NEW.card_revision AND state IS NOT NULL) OR NOT EXISTS(SELECT 1 FROM attachment WHERE incarnation=NEW.incarnation AND attachment_id=NEW.attachment_id AND card_id=NEW.card_id AND sha256=NEW.sha256 AND byte_length=NEW.byte_length) THEN RAISE(ABORT,'history pin mismatch') END; END;
CREATE TRIGGER history_compaction BEFORE UPDATE ON card_revision BEGIN SELECT CASE WHEN NEW.incarnation<>OLD.incarnation OR NEW.origin_board<>OLD.origin_board OR NEW.origin_incarnation<>OLD.origin_incarnation OR NEW.card_id<>OLD.card_id OR NEW.card_revision<>OLD.card_revision OR NEW.actor_id<>OLD.actor_id OR NEW.committed_at<>OLD.committed_at OR OLD.state IS NULL OR NEW.state IS NOT NULL OR EXISTS(SELECT 1 FROM history_blob_pin WHERE incarnation=OLD.incarnation AND origin_board=OLD.origin_board AND origin_incarnation=OLD.origin_incarnation AND card_id=OLD.card_id AND card_revision=OLD.card_revision) THEN RAISE(ABORT,'invalid history compaction') END; END;
CREATE TRIGGER history_metadata_retained BEFORE DELETE ON card_revision BEGIN SELECT RAISE(ABORT,'snapshot metadata is retained'); END;
CREATE TRIGGER operation_compaction BEFORE UPDATE ON operation BEGIN SELECT CASE WHEN OLD.removed_at IS NOT NULL OR NEW.removed_at IS NULL OR EXISTS(SELECT 1 FROM operation_full WHERE incarnation=OLD.incarnation AND origin_board=OLD.origin_board AND origin_incarnation=OLD.origin_incarnation AND op_id=OLD.op_id) THEN RAISE(ABORT,'invalid operation compaction') END; END;
CREATE TRIGGER operation_full_not_removed BEFORE INSERT ON operation_full BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM operation WHERE incarnation=NEW.incarnation AND origin_board=NEW.origin_board AND origin_incarnation=NEW.origin_incarnation AND op_id=NEW.op_id AND removed_at IS NULL) THEN RAISE(ABORT,'compact outcome cannot regain full payload') END; END;
CREATE TRIGGER digest_blob_length BEFORE INSERT ON blob BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM byte_fact WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) OR EXISTS(SELECT 1 FROM attachment WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) THEN RAISE(ABORT,'digest length disagreement') END; END;
CREATE TRIGGER digest_attachment_length BEFORE INSERT ON attachment BEGIN SELECT CASE WHEN EXISTS(SELECT 1 FROM byte_fact WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) OR EXISTS(SELECT 1 FROM blob WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) OR EXISTS(SELECT 1 FROM attachment WHERE incarnation=NEW.incarnation AND sha256=NEW.sha256 AND byte_length<>NEW.byte_length) THEN RAISE(ABORT,'digest length disagreement') END; END;
CREATE TRIGGER blob_immutable BEFORE UPDATE ON blob BEGIN SELECT RAISE(ABORT,'immutable content-addressed bytes'); END;
CREATE TRIGGER grant_fact_retained BEFORE DELETE ON grant_fact BEGIN SELECT RAISE(ABORT,'historical grant fact retained'); END;
CREATE TRIGGER cli_credential_current BEFORE INSERT ON cli_credential BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM board WHERE board_id=NEW.board_id AND incarnation=NEW.incarnation) THEN RAISE(ABORT,'credential current board mismatch') END; SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM grant_record WHERE incarnation=NEW.incarnation AND grant_id=NEW.grant_id AND actor_id=NEW.actor_id AND enabled=1) THEN RAISE(ABORT,'credential requires enabled current grant') END; END;
CREATE TRIGGER cli_credential_immutable BEFORE UPDATE ON cli_credential BEGIN SELECT RAISE(ABORT,'replace credential through explicit delete and insert'); END;
CREATE TRIGGER actor_retained BEFORE DELETE ON actor BEGIN SELECT RAISE(ABORT,'historical actor retained'); END;
CREATE TRIGGER byte_fact_retained BEFORE DELETE ON byte_fact BEGIN SELECT RAISE(ABORT,'compact byte fact retained'); END;
CREATE TRIGGER operation_reference_retained BEFORE DELETE ON operation_reference BEGIN SELECT RAISE(ABORT,'compact result reference retained'); END;
CREATE TRIGGER entity_retained BEFORE DELETE ON entity BEGIN SELECT RAISE(ABORT,'entity existence is retained'); END;
CREATE TRIGGER grant_fact_immutable BEFORE UPDATE ON grant_fact BEGIN SELECT RAISE(ABORT,'historical grant fact immutable'); END;
CREATE TRIGGER actor_identity_immutable BEFORE UPDATE ON actor BEGIN SELECT CASE WHEN NEW.incarnation<>OLD.incarnation OR NEW.actor_id<>OLD.actor_id OR NEW.kind<>OLD.kind OR NEW.origin_board<>OLD.origin_board OR NEW.origin_incarnation<>OLD.origin_incarnation THEN RAISE(ABORT,'actor identity immutable') END; END;
CREATE TRIGGER link_identity_immutable BEFORE UPDATE ON link BEGIN SELECT CASE WHEN NEW.incarnation<>OLD.incarnation OR NEW.link_id<>OLD.link_id OR NEW.card_id<>OLD.card_id OR NEW.kind<>OLD.kind OR NEW.target_board<>OLD.target_board OR NEW.target_incarnation<>OLD.target_incarnation OR NEW.target_card_id<>OLD.target_card_id OR NEW.internal_target_id IS NOT OLD.internal_target_id OR NEW.created_at<>OLD.created_at THEN RAISE(ABORT,'link identity immutable') END; END;
CREATE TRIGGER board_retained BEFORE DELETE ON board BEGIN SELECT RAISE(ABORT,'board identity retained'); END;
CREATE TRIGGER policy_retained BEFORE DELETE ON policy BEGIN SELECT RAISE(ABORT,'fixed workflow policy retained'); END;
`
