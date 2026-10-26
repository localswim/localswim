"""Regenerate invented D02 documentary vectors, never product/runtime evidence."""
from pathlib import Path
import hashlib
import json

ROOT = Path(__file__).resolve().parent


def quote(value):
    out = '"'
    for char in value:
        code = ord(char)
        if 0xD800 <= code <= 0xDFFF:
            raise ValueError("invalid Unicode")
        if char in ('"', '\\'):
            out += '\\' + char
        elif code < 32:
            out += '\\u%04x' % code
        else:
            out += char
    return out + '"'


def encode(value, pretty=False, depth=0):
    if value is None:
        return 'null'
    if value is True:
        return 'true'
    if value is False:
        return 'false'
    if isinstance(value, int):
        if not 0 <= value <= 9007199254740991:
            raise ValueError("unsafe integer")
        return str(value)
    if isinstance(value, str):
        return quote(value)
    if isinstance(value, (dict, list)):
        left, right = ('{', '}') if isinstance(value, dict) else ('[', ']')
        values = [(quote(k) + (': ' if pretty else ':') +
                   encode(value[k], pretty, depth + 1)) for k in sorted(value)] \
            if isinstance(value, dict) else [encode(v, pretty, depth + 1) for v in value]
        if not values:
            return left + right
        if pretty:
            pad = '  ' * (depth + 1)
            return left + '\n' + pad + (',\n' + pad).join(values) + '\n' + '  ' * depth + right
        return left + ','.join(values) + right
    raise ValueError("unsupported scalar")


def canonical(value):
    return encode(value).encode('utf-8')


def readable(value):
    return (encode(value, True) + '\n').encode('utf-8')


def sha(data):
    return hashlib.sha256(data).hexdigest()


def write(path, data):
    file = ROOT / path
    file.parent.mkdir(parents=True, exist_ok=True)
    file.write_bytes(data)


def js(path, value):
    write(path, readable(value))


def uuid(n):
    return f'00000000-0000-4000-8000-{n:012x}'


def main():
    board, inc, old, fresh = uuid(1), uuid(2), uuid(3), uuid(4)
    actor, card, comment, attachment, empty_attachment, operation, refused, old_op = [uuid(n) for n in range(10, 18)]
    identity = {'boardId': board, 'incarnation': inc}
    old_identity = {'boardId': board, 'incarnation': old}
    destination = {'boardId': board, 'incarnation': fresh}
    t0, t1, now = '2026-09-01T00:00:00Z', '2026-10-01T00:00:00Z', '2026-10-25T12:00:00Z'
    note, empty = b'Evidence A', b''
    note_fact = {'sha256': sha(note), 'byteLength': len(note)}
    empty_fact = {'sha256': sha(empty), 'byteLength': 0}
    facts = sorted([note_fact, empty_fact], key=lambda item: item['sha256'])
    retention = {'historyDays': 90, 'activityDays': 365, 'outcomeDays': 180}
    gap = {'origin': old_identity, 'class': 'outcome_payload', 'reason': 'expired',
           'fromExclusive': None, 'throughInclusive': t0, 'recordedAt': t1, 'boardRevision': 2}
    coverage = {'historyFloor': None, 'activityFloor': None, 'outcomePayloadFloor': None, 'gaps': [gap]}
    lineage = [{'identity': old_identity, 'manifestSha256': '1' * 64, 'revision': 2}]
    actors = [{'actorId': actor, 'kind': 'human', 'label': 'Synthetic reviewer 雪', 'createdIn': old_identity}]
    attachments = [
        {'attachmentId': attachment, 'cardId': card, 'name': 'note.txt', 'mediaLabel': 'text/plain',
         **note_fact, 'createdAt': t1, 'createdIn': identity},
        {'attachmentId': empty_attachment, 'cardId': card, 'name': 'empty.bin', 'mediaLabel': 'application/octet-stream',
         **empty_fact, 'createdAt': '2026-09-02T00:00:00Z', 'createdIn': identity},
    ]
    board_doc = {'schemaVersion': 1, 'identity': identity, 'revision': 5, 'nextTicket': 2,
                 'policyVersion': 1, 'retention': retention, 'retentionVersion': 1,
                 'coverage': coverage, 'actors': actors, 'cards': [
                     {'cardId': card, 'ticket': 1, 'revision': 4, 'title': 'Alpha 雪',
                      'description': 'Reviewed e\u0301\r\n"quote" \\ path', 'priority': 'P3',
                      'lane': 'Ready for Work', 'archived': False, 'currentApprovalId': None,
                      'commentIds': [comment], 'linkIds': [], 'attachmentIds': [attachment, empty_attachment],
                      'createdIn': old_identity}],
                 'comments': [{'commentId': comment, 'cardId': card, 'actorId': actor,
                               'text': 'Synthetic check', 'createdAt': t0, 'createdIn': old_identity}],
                 'links': [], 'attachments': attachments, 'approvals': [], 'tombstones': []}
    history = {'schemaVersion': 1, 'identity': identity, 'revision': 5, 'records': [
        {'origin': identity, 'cardId': card, 'cardRevision': 3, 'committedAt': '2026-09-02T00:00:00Z', 'actorId': actor,
         'state': {'ticket': 1, 'title': 'Alpha 雪', 'description': 'First draft', 'priority': 'P3',
                   'lane': 'Ready for Work', 'archived': False, 'currentApprovalId': None},
         'references': {'commentIds': [comment], 'linkIds': [], 'attachmentIds': [empty_attachment], 'approvalIds': []}, 'removal': None},
        {'origin': old_identity, 'cardId': card, 'cardRevision': 1, 'committedAt': t0, 'actorId': actor,
         'state': None, 'references': {'commentIds': [], 'linkIds': [], 'attachmentIds': [], 'approvalIds': []},
         'removal': {'reason': 'cleared', 'removedAt': t1, 'boardRevision': 4}},
        {'origin': old_identity, 'cardId': card, 'cardRevision': 2, 'committedAt': t0, 'actorId': actor,
         'state': None, 'references': {'commentIds': [comment], 'linkIds': [], 'attachmentIds': [], 'approvalIds': []},
         'removal': {'reason': 'cleared', 'removedAt': t1, 'boardRevision': 4}}]}
    cleared_gap = {'origin': old_identity, 'class': 'history', 'reason': 'cleared', 'fromExclusive': None,
                   'throughInclusive': t0, 'recordedAt': t1, 'boardRevision': 4}
    # Current full revision 3 is newer than cleared source revisions 1/2.
    coverage['gaps'].insert(0, cleared_gap)
    envelope = {'fingerprintVersion': 1, 'boardId': board, 'incarnation': inc,
                'opId': operation, 'actorId': actor, 'command': 'attachment.add', 'target': {'cardId': card},
                'payload': {'name': 'note.txt', 'mediaLabel': 'text/plain', **note_fact},
                'expectedRevision': 4, 'expectedCardRevision': 3}
    full = {'origin': identity, 'opId': operation, 'actorId': actor, 'fingerprintVersion': 1,
            'fingerprint': sha(canonical(envelope)), 'command': 'attachment.add', 'target': {'cardId': card},
            'expectedRevision': 4, 'expectedCardRevision': 3, 'terminalClass': 'COMMITTED', 'code': 'OK',
            'terminalAt': t1, 'committedRevision': 5, 'committedCardRevision': 4,
            'resultRefs': [{'kind': 'attachment', 'id': attachment, 'revision': None},
                           {'kind': 'card', 'id': card, 'revision': 4}],
            'byteFacts': [note_fact], 'full': {'intent': envelope, 'result': {'cardId': card, 'attachmentId': attachment}},
            'removal': None}
    compact = {'origin': identity, 'opId': refused, 'actorId': actor, 'fingerprintVersion': 1,
               'fingerprint': '2' * 64, 'command': 'attachment.add', 'target': {'cardId': card},
               'expectedRevision': 2, 'expectedCardRevision': 1, 'terminalClass': 'NOT_COMMITTED',
               'code': 'REVISION_CONFLICT', 'terminalAt': t0, 'committedRevision': None,
               'committedCardRevision': None, 'resultRefs': [],
               'byteFacts': [{'sha256': sha(b'not pinned'), 'byteLength': len(b'not pinned')}],
               'full': None, 'removal': {'reason': 'cleared', 'removedAt': t1, 'boardRevision': 4}}
    historical = {**compact, 'origin': old_identity, 'opId': old_op, 'fingerprint': '3' * 64}
    historical['removal'] = {'reason': 'expired', 'removedAt': t1, 'boardRevision': 2}
    # These attempted IDs have never existed. Refusals preserve them without
    # fabricated entities, grants, tombstones or raw BLOBs.
    missing_card, missing_grant = uuid(90), uuid(91)
    attempted_fact = {'sha256': sha(b'never stored'), 'byteLength': len(b'never stored')}
    refusal_intent = {'fingerprintVersion': 1, 'boardId': board, 'incarnation': inc,
                      'opId': uuid(40), 'actorId': actor, 'command': 'attachment.add',
                      'target': {'cardId': missing_card}, 'expectedRevision': 5, 'expectedCardRevision': 1,
                      'payload': {'name': 'attempted.bin', 'mediaLabel': 'application/octet-stream', **attempted_fact}}
    full_not_found = {'origin': identity, 'opId': uuid(40), 'actorId': actor, 'fingerprintVersion': 1,
                      'fingerprint': sha(canonical(refusal_intent)), 'command': 'attachment.add',
                      'target': {'cardId': missing_card}, 'expectedRevision': 5, 'expectedCardRevision': 1,
                      'terminalClass': 'NOT_COMMITTED', 'code': 'NOT_FOUND', 'terminalAt': '2026-10-02T00:00:00Z',
                      'committedRevision': None, 'committedCardRevision': None, 'resultRefs': [],
                      'byteFacts': [attempted_fact], 'full': {'intent': refusal_intent,
                          'result': {'error': {'code': 'NOT_FOUND', 'message': 'Synthetic target never existed',
                                               'field': 'target.cardId'}, 'conflict': None}}, 'removal': None}
    compact_intent = {'fingerprintVersion': 1, 'boardId': board, 'incarnation': inc,
                      'opId': uuid(41), 'actorId': actor, 'command': 'grant.revoke',
                      'target': {'grantId': missing_grant}, 'expectedRevision': 2,
                      'expectedCardRevision': None, 'payload': {}}
    compact_not_found = {'origin': identity, 'opId': uuid(41), 'actorId': actor, 'fingerprintVersion': 1,
                         'fingerprint': sha(canonical(compact_intent)), 'command': 'grant.revoke',
                         'target': {'grantId': missing_grant}, 'expectedRevision': 2, 'expectedCardRevision': None,
                         'terminalClass': 'NOT_COMMITTED', 'code': 'NOT_FOUND', 'terminalAt': t0,
                         'committedRevision': None, 'committedCardRevision': None, 'resultRefs': [], 'byteFacts': [],
                         'full': None, 'removal': {'reason': 'cleared', 'removedAt': t1, 'boardRevision': 4}}
    coverage['gaps'].insert(0, {'origin': identity, 'class': 'outcome_payload', 'reason': 'cleared',
                             'fromExclusive': None, 'throughInclusive': t0, 'recordedAt': t1, 'boardRevision': 4})
    operations = [full, compact, full_not_found, compact_not_found]
    outcomes = {'schemaVersion': 1, 'identity': identity, 'revision': 5, 'operations': operations,
                'sourceArchives': [{'identity': old_identity, 'operations': [historical]}]}
    activity = {'schemaVersion': 1, 'identity': identity, 'revision': 5, 'events': [
        {'origin': identity, 'eventId': 5, 'actorId': actor, 'opId': operation, 'type': 'attachment.add',
         'boardRevision': 5, 'affected': full['resultRefs'], 'at': t1}]}
    payloads = {'board.json': board_doc, 'history.json': history, 'activity.json': activity, 'outcomes.json': outcomes}
    inventory = []
    for path, value in payloads.items():
        data = readable(value)
        write('package/' + path, data)
        inventory.append({'path': path, 'byteLength': len(data), 'sha256': sha(data)})
    for fact, data in [(note_fact, note), (empty_fact, empty)]:
        path = 'blobs/sha256-' + fact['sha256']
        write('package/' + path, data)
        inventory.append({'path': path, 'byteLength': len(data), 'sha256': sha(data)})
    inventory.sort(key=lambda item: item['path'])
    manifest = {'formatVersion': 1, 'schemaVersion': 1, 'hashAlgorithm': 'sha256', 'source': identity,
                'revision': 5, 'exportedAt': now, 'lineage': lineage, 'retention': retention,
                'retentionVersion': 1, 'coverage': coverage, 'inventory': inventory}
    js('package/manifest.json', manifest)
    manifest_hash = sha(readable(manifest))
    write('package/COMPLETE', (manifest_hash + '\n').encode('ascii'))
    selected = [{'origin': identity, 'cardId': card, 'cardRevision': 3}]
    outcome_keys = [{'origin': identity, 'opId': operation}]
    preview = {'apiVersion': 1, 'boardId': board, 'incarnation': inc, 'actorId': actor,
               'expectedRevision': 5, 'retentionVersion': 1, 'command': 'history.clear',
               'input': {'throughTime': t1}, 'previewedAt': now, 'expiresAt': '2026-10-25T12:05:00Z',
               'inventory': {'history': selected, 'activity': [], 'outcomes': outcome_keys,
                             'counts': {'historySnapshots': 1, 'activityEvents': 0, 'outcomePayloads': 1},
                             'bytes': {'domainBefore': facts, 'domainAfter': facts, 'exportPinned': [], 'reclaimable': [],
                                       'domainBeforeBytes': len(note), 'domainAfterBytes': len(note),
                                       'exportPinnedBytes': 0, 'reclaimableBytes': 0},
                             'coverageGaps': [
                                 {'origin': identity, 'class': cls, 'reason': 'cleared', 'fromExclusive': None,
                                  'throughInclusive': t1} for cls in ['history', 'outcome_payload']],
                             'proposedFloors': {'historyFloor': None, 'activityFloor': None, 'outcomePayloadFloor': None},
                             'proposedRetention': retention, 'proposedRetentionVersion': 1, 'proposedBoardRevision': 6}}
    preview_bytes = canonical(preview)
    preview['previewHash'] = sha(preview_bytes)
    js('preview-clear.json', preview)
    write('preview-clear.hash-input.bin', preview_bytes)
    retention_preview = json.loads(json.dumps(preview))
    retention_preview.pop('previewHash')
    retention_preview['command'] = 'retention.set'
    retention_preview['input'] = {'historyDays': 1, 'activityDays': 2, 'outcomeDays': 3}
    ri = retention_preview['inventory']
    ri['activity'] = [{'origin': identity, 'eventId': 5}]
    ri['counts']['activityEvents'] = 1
    ri['outcomes'].append({'origin': identity, 'opId': full_not_found['opId']})
    ri['counts']['outcomePayloads'] = 2
    ri['proposedRetention'] = retention_preview['input']
    ri['proposedRetentionVersion'] = 2
    ri['proposedFloors'] = {'historyFloor': '2026-10-24T12:00:00Z',
                            'activityFloor': '2026-10-23T12:00:00Z',
                            'outcomePayloadFloor': '2026-10-22T12:00:00Z'}
    ri['coverageGaps'] = [
        {'origin': identity, 'class': cls, 'reason': 'expired', 'fromExclusive': None, 'throughInclusive': cutoff}
        for cls, cutoff in [('activity', ri['proposedFloors']['activityFloor']),
                            ('history', ri['proposedFloors']['historyFloor']),
                            ('outcome_payload', ri['proposedFloors']['outcomePayloadFloor'])]]
    retention_preview['previewHash'] = sha(canonical(retention_preview))
    js('preview-retention.json', retention_preview)
    confirm = {'opId': uuid(30), 'incarnation': inc, 'target': None, 'expectedRevision': 5,
               'expectedCardRevision': None, 'command': 'history.clear', 'input': preview['input'],
               'previewHash': preview['previewHash']}
    js('confirm-clear.json', confirm)
    intent = {'fingerprintVersion': 1, 'boardId': board, 'incarnation': inc, 'opId': confirm['opId'],
              'actorId': actor, 'command': confirm['command'], 'target': None,
              'expectedRevision': 5, 'expectedCardRevision': None,
              'payload': {'input': confirm['input'], 'previewHash': confirm['previewHash']}}
    write('confirm-clear.canonical.bin', canonical(intent))
    js('preview-request.json', {key: preview[key] for key in ['incarnation', 'expectedRevision', 'command', 'input']})
    target_payloads = json.loads(json.dumps(payloads))
    for doc in target_payloads.values():
        doc['identity'] = destination
    target_payloads['outcomes.json']['operations'] = []
    target_payloads['outcomes.json']['sourceArchives'].insert(0, {'identity': identity, 'operations': operations})
    target_entries = [entry for entry in inventory if entry['path'].startswith('blobs/')]
    for path, value in target_payloads.items():
        data = readable(value)
        write('prepared-target/' + path, data)
        target_entries.append({'path': path, 'byteLength': len(data), 'sha256': sha(data)})
    target_entries.sort(key=lambda entry: entry['path'])
    anchor = {'schemaVersion': 1, 'entries': target_entries, 'entryCount': len(target_entries),
              'sha256': sha(canonical(target_entries))}
    receipt = {'receiptVersion': 1, 'requestId': uuid(31), 'sourceManifest': manifest,
               'sourceManifestSha256': manifest_hash, 'destination': destination, 'targetInventory': anchor,
               'phase': 'published', 'result': 'complete', 'failureCode': None,
               'createdAt': now, 'updatedAt': now}
    js('restore-receipt.json', receipt)
    js('restore-receipt-prepared.json', {**receipt, 'phase': 'prepared', 'result': 'incomplete'})
    js('restore-receipt-preparing.json', {**receipt, 'phase': 'preparing', 'result': 'incomplete', 'targetInventory': None})
    js('restore-receipt-failed.json', {**receipt, 'phase': 'preparing', 'result': 'failed',
                                     'targetInventory': None, 'failureCode': 'DESTINATION_WRITE_FAILED'})
    js('status-confirmed.json', {'apiVersion': 1, 'scope': 'restore', 'outcome': 'confirmed',
                                'result': {'sourceIdentity': identity, 'sourceRevision': 5, 'destination': destination}})
    js('status-unknown.json', {'apiVersion': 1, 'scope': 'restore', 'outcome': 'unknown',
                              'result': {'sourceIdentity': None, 'sourceRevision': None, 'destination': None}})
    primitives = [
        ('absent', {'z': []}), ('explicit-null', {'z': [], 'a': None}),
        ('unicode-controls', {'z': ['e\u0301', '\u00e9', '雪', '\r\n', '"\\\x00\t'], 'a': 0, 'n': 9007199254740991}),
        ('empty', {'array': [], 'object': {}, 'text': '', 'value': None}),
    ]
    vectors = []
    for name, value in primitives:
        data = canonical(value)
        write('canonical/' + name + '.bin', data)
        vectors.append({'name': name, 'value': value, 'path': 'canonical/' + name + '.bin',
                        'byteLength': len(data), 'sha256': sha(data), 'hex': data.hex()})
    vectors.extend([
        {'name': 'manifest', 'path': 'package/manifest.json', 'byteLength': len(readable(manifest)), 'sha256': manifest_hash},
        {'name': 'preview-hash-input', 'path': 'preview-clear.hash-input.bin', 'byteLength': len(preview_bytes), 'sha256': sha(preview_bytes)},
        {'name': 'confirm-intent', 'path': 'confirm-clear.canonical.bin', 'byteLength': len(canonical(intent)), 'sha256': sha(canonical(intent))},
    ])
    for name, value in [('refusal-attachment-intent', refusal_intent), ('refusal-grant-intent', compact_intent)]:
        data = canonical(value)
        path = 'canonical/' + name + '.bin'
        write(path, data)
        vectors.append({'name': name, 'path': path, 'byteLength': len(data), 'sha256': sha(data)})
    js('refusal-cases.json', {'schemaVersion': 1, 'cases': [
        {'opId': full_not_found['opId'], 'target': full_not_found['target'], 'retention': 'full',
         'intentPath': 'canonical/refusal-attachment-intent.bin'},
        {'opId': compact_not_found['opId'], 'target': compact_not_found['target'], 'retention': 'compact',
         'intentPath': 'canonical/refusal-grant-intent.bin'}],
        'expected': 'Same exact rows move to prepared sourceArchives; attempted targets never become entities or grants.'})
    js('vectors.json', {'schemaVersion': 1, 'vectors': vectors,
                        'notes': ['All identities/content invented.', 'Binary canonical inputs intentionally have no final LF.',
                                  'Empty raw BLOB is zero bytes; note.txt is Evidence A without LF.',
                                  'Documentary parser/encoding checks do not qualify product/runtime behavior.']})
    bad = {
        'duplicate-key.bin': b'{"apiVersion":1,"apiVersion":1}\n',
        'invalid-utf8.bin': b'{"text":"\xff"}\n',
        'unpaired-surrogate.bin': b'{"text":"\\ud800"}\n',
        'overflow.bin': b'{"revision":9007199254740992}\n',
        'fraction.bin': b'{"revision":1.5}\n',
        'negative.bin': b'{"revision":-1}\n',
        'bad-time.bin': b'{"throughTime":"2026-10-25T12:00:00.1Z"}\n',
        'unknown-field.bin': readable({**confirm, 'actorId': actor}),
        'absent-null.bin': readable({k: v for k, v in confirm.items() if k != 'target'}),
        'null-not-input.bin': readable({**confirm, 'input': None}),
    }
    for path, data in bad.items():
        write('negative/' + path, data)
    binary_cases = []
    for path, data, length_mismatch in [('tampered-blob.bin', b'Evidence B', False), ('truncated-blob.bin', b'Evidence', True)]:
        write('negative/' + path, data)
        binary_cases.append({'path': 'negative/' + path, 'expected': 'reject', 'expectedByteFact': note_fact,
                             'lengthMismatch': length_mismatch, 'digestMismatch': True})
    semantic_cases = []
    semantic_values = []
    broken = json.loads(json.dumps(full))
    broken['target']['cardId'] = missing_card
    broken['full']['intent']['target']['cardId'] = missing_card
    broken['fingerprint'] = sha(canonical(broken['full']['intent']))
    semantic_values.append(('broken-committed-reference.json', broken, 'COMMITTED_REFERENCE'))
    omitted = json.loads(json.dumps(full))
    omitted['byteFacts'] = []
    semantic_values.append(('byte-facts-omitted.json', omitted, 'BYTE_FACTS_CARDINALITY'))
    substituted = json.loads(json.dumps(full))
    substituted['byteFacts'] = [attempted_fact]
    semantic_values.append(('byte-facts-substituted.json', substituted, 'BYTE_FACTS_MAPPING'))
    conflicting = json.loads(json.dumps(full))
    conflicting['byteFacts'][0]['byteLength'] += 1
    conflicting['full']['intent']['payload']['byteLength'] += 1
    conflicting['fingerprint'] = sha(canonical(conflicting['full']['intent']))
    semantic_values.append(('byte-facts-conflicting-length.json', conflicting, 'BYTE_LENGTH_CONFLICT'))
    nonbyte = json.loads(json.dumps(compact_not_found))
    nonbyte['byteFacts'] = [note_fact]
    semantic_values.append(('nonbyte-command-facts.json', nonbyte, 'BYTE_FACTS_CARDINALITY'))
    for path, value, predicate in semantic_values:
        js('negative/semantic/' + path, value)
        semantic_cases.append({'path': 'negative/semantic/' + path, 'expected': 'reject', 'predicate': predicate})
    js('negative-cases.json', {'schemaVersion': 1, 'jsonCases': [
        {'path': 'negative/' + path, 'expected': 'reject', 'reason': reason}
        for path, reason in zip(bad, [
            'duplicate JSON key', 'invalid UTF-8', 'invalid Unicode scalar', 'unsafe integer overflow',
            'fractional number', 'negative number', 'noncanonical timestamp', 'unknown confirm field',
            'required null key absent', 'required typed input null'])],
        'binaryCases': binary_cases, 'semanticCases': semantic_cases,
        'futureArchiveCases': [
            'missing COMPLETE or wrong manifest hash: incomplete/refuse restore',
            'missing/extra/duplicate/case-aliased member: refuse restore',
            'stored member local/central mismatch, traversal, symlink or compression: refuse restore',
            'package/JSON/entry/depth/BLOB bounds exceeded: unsupported scope',
            'receipt absent/incomplete/both locations/unreadable or target anchor mismatch: unknown',
            'source outcome at fresh destination: unknown/source authority unavailable',
            'confirm at expiresAt: terminal PREVIEW_EXPIRED if recorded; otherwise unknown',
            'confirm recomputed using a new now: mismatch; original immutable preview time must be used']})


if __name__ == '__main__':
    main()
