"""D02 documentary self-check. This is not a product or hostile-ZIP validator."""
from pathlib import Path
from datetime import datetime, timedelta
import json
import re
import sys
sys.dont_write_bytecode = True
from make_fixtures import canonical, readable, sha

ROOT = Path(__file__).resolve().parent


def pairs(items):
    value = {}
    for key, item in items:
        if key in value:
            raise ValueError('duplicate key')
        value[key] = item
    return value


def integer(text):
    if not re.fullmatch(r'0|[1-9][0-9]*', text) or int(text) > 9007199254740991:
        raise ValueError('unsafe integer')
    return int(text)


def bad_number(text):
    raise ValueError('noninteger/nonfinite number')


def parse(data):
    value = json.loads(data.decode('utf-8'), object_pairs_hook=pairs, parse_int=integer,
                       parse_float=bad_number, parse_constant=bad_number)
    canonical(value)  # rejects unpaired surrogate scalars anywhere
    return value


def read(path):
    return parse((ROOT / path).read_bytes())


def timestamp(value):
    if not isinstance(value, str) or not re.fullmatch(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z', value):
        raise ValueError('invalid time encoding')
    return datetime.strptime(value, '%Y-%m-%dT%H:%M:%SZ')


def origin_key(value):
    return value['boardId'], value['incarnation']


def unique_sorted(values, key):
    keys = [key(v) for v in values]
    assert keys == sorted(set(keys)), 'duplicate or wrong order'


def operation_semantics(row, board):
    """Only the reviewed R1/R3 fixture predicates, not a full product validator."""
    cards = {item['cardId'] for item in board['cards']}
    attachments = {item['attachmentId']: item for item in board['attachments']}
    registry = {'card': cards, 'attachment': set(attachments),
                'comment': {item['commentId'] for item in board['comments']},
                'link': {item['linkId'] for item in board['links']},
                'approval': {item['approvalId'] for item in board['approvals']}}
    for tombstone in board['tombstones']:
        registry[tombstone['kind']].add(tombstone['id'])
    if row['terminalClass'] == 'COMMITTED':
        target = row['target']
        if target is not None and 'cardId' in target and target['cardId'] not in registry['card']:
            raise ValueError('COMMITTED_REFERENCE')
        for ref in row['resultRefs']:
            if ref['kind'] in registry and ref['id'] not in registry[ref['kind']]:
                raise ValueError('COMMITTED_REFERENCE')
    else:
        # No target lookup: NOT_FOUND may name a never-existing card or grant.
        if row['resultRefs'] or row['committedRevision'] is not None or row['committedCardRevision'] is not None:
            raise ValueError('REFUSAL_EFFECT')
    expected_count = 1 if row['command'] == 'attachment.add' else 0
    if len(row['byteFacts']) != expected_count:
        raise ValueError('BYTE_FACTS_CARDINALITY')
    if row['full'] is not None:
        intent = row['full']['intent']
        if sha(canonical(intent)) != row['fingerprint']:
            raise ValueError('FINGERPRINT')
        if row['command'] == 'attachment.add':
            payload = intent['payload']
            expected = {'sha256': payload['sha256'], 'byteLength': payload['byteLength']}
            if row['byteFacts'] != [expected]:
                raise ValueError('BYTE_FACTS_MAPPING')
    lengths = {item['sha256']: item['byteLength'] for item in board['attachments']}
    for fact in row['byteFacts']:
        if fact['sha256'] in lengths and fact['byteLength'] != lengths[fact['sha256']]:
            raise ValueError('BYTE_LENGTH_CONFLICT')
    if row['terminalClass'] == 'COMMITTED' and row['command'] == 'attachment.add':
        attachment_refs = [ref for ref in row['resultRefs'] if ref['kind'] == 'attachment']
        card_refs = [ref for ref in row['resultRefs'] if ref['kind'] == 'card']
        if (len(row['resultRefs']) != 2 or len(attachment_refs) != 1 or len(card_refs) != 1 or
                attachment_refs[0]['revision'] is not None or
                card_refs[0] != {'kind': 'card', 'id': row['target']['cardId'], 'revision': row['committedCardRevision']}):
            raise ValueError('ATTACHMENT_RESULT_MAPPING')
        item = attachments.get(attachment_refs[0]['id'])
        if row['full'] is not None:
            result = row['full']['result']
            if item is None:
                raise ValueError('COMMITTED_REFERENCE')
            if result != {'cardId': row['target']['cardId'], 'attachmentId': item['attachmentId']}:
                raise ValueError('ATTACHMENT_RESULT_MAPPING')
        if item is not None and (item['cardId'] != row['target']['cardId'] or
                {'sha256': item['sha256'], 'byteLength': item['byteLength']} != row['byteFacts'][0]):
            raise ValueError('ATTACHMENT_RESULT_MAPPING')


def main():
    # Byte/format checks are independent of generating new fixture files.
    for file in ROOT.rglob('*'):
        if file.is_file() and file.suffix in ('.json', '.py', '.txt'):
            data = file.read_bytes()
            assert not data.startswith(b'\xef\xbb\xbf') and b'\r' not in data and data.endswith(b'\n'), file
            data.decode('utf-8')
            if file.suffix == '.json':
                assert readable(parse(data)) == data, file
    manifest = read('package/manifest.json')
    assert set(manifest) == {'formatVersion', 'schemaVersion', 'hashAlgorithm', 'source', 'revision',
                             'exportedAt', 'lineage', 'retention', 'retentionVersion', 'coverage', 'inventory'}
    assert (ROOT / 'package/COMPLETE').read_bytes() == (sha(readable(manifest)) + '\n').encode('ascii')
    unique_sorted(manifest['inventory'], lambda item: item['path'])
    expected_files = {'manifest.json', 'COMPLETE'}
    for entry in manifest['inventory']:
        data = (ROOT / 'package' / entry['path']).read_bytes()
        assert len(data) == entry['byteLength'] and sha(data) == entry['sha256']
        expected_files.add(entry['path'])
    actual_files = {file.relative_to(ROOT / 'package').as_posix() for file in (ROOT / 'package').rglob('*') if file.is_file()}
    assert expected_files == actual_files
    assert len(actual_files) <= 16384
    json_lengths = [(ROOT / 'package' / path).stat().st_size for path in actual_files if path.endswith('.json')]
    assert max(json_lengths) <= 33554432 and sum(json_lengths) <= 67108864
    docs = {name: read('package/' + name + '.json') for name in ['board', 'history', 'activity', 'outcomes']}
    for doc in docs.values():
        assert doc['identity'] == manifest['source'] and doc['revision'] == manifest['revision'] and doc['schemaVersion'] == 1
    board, history, activity, outcomes = [docs[k] for k in ['board', 'history', 'activity', 'outcomes']]
    assert board['coverage'] == manifest['coverage'] and board['retention'] == manifest['retention']
    unique_sorted(board['cards'], lambda row: (row['ticket'], row['cardId']))
    cards = {row['cardId']: row for row in board['cards']}
    attachments = {row['attachmentId']: row for row in board['attachments']}
    comments = {row['commentId']: row for row in board['comments']}
    actors = {row['actorId'] for row in board['actors']}
    assert board['nextTicket'] > max(row['ticket'] for row in cards.values())
    pinned = {}
    for card in cards.values():
        assert card['currentApprovalId'] is None
        for attachment in card['attachmentIds']:
            row = attachments[attachment]
            pinned[row['sha256']] = row['byteLength']
        assert set(card['commentIds']) <= comments.keys()
    unique_sorted(history['records'], lambda row: (*origin_key(row['origin']), row['cardId'], row['cardRevision']))
    for row in history['records']:
        assert row['cardRevision'] < cards[row['cardId']]['revision'] and row['actorId'] in actors
        assert (row['state'] is None) != (row['removal'] is None)
        if row['state'] is not None:
            for aid in row['references']['attachmentIds']:
                item = attachments[aid]
                pinned[item['sha256']] = item['byteLength']
    unique_sorted(outcomes['sourceArchives'], lambda group: origin_key(group['identity']))
    all_ops = []
    for identity, rows in [(outcomes['identity'], outcomes['operations'])] + [(group['identity'], group['operations']) for group in outcomes['sourceArchives']]:
        unique_sorted(rows, lambda row: row['opId'])
        for row in rows:
            assert row['origin'] == identity and row['actorId'] in actors
            assert (row['full'] is None) != (row['removal'] is None)
            operation_semantics(row, board)
            all_ops.append(row)
            if row['full'] is not None:
                intent = row['full']['intent']
                assert sha(canonical(intent)) == row['fingerprint']
                assert intent['boardId'] == identity['boardId'] and intent['incarnation'] == identity['incarnation']
                for key in ['opId', 'actorId', 'command', 'target', 'expectedRevision', 'expectedCardRevision']:
                    assert intent[key] == row[key]
                if row['terminalClass'] == 'COMMITTED':
                    for item in row['byteFacts']:
                        pinned[item['sha256']] = item['byteLength']
            if row['terminalClass'] == 'NOT_COMMITTED':
                assert row['committedRevision'] is None and row['committedCardRevision'] is None and row['resultRefs'] == []
    # Equal digest lengths must agree even when a fact is compact/refused and does
    # not protect a BLOB. This detects conflicts between any retained fact rows.
    all_lengths = {item['sha256']: item['byteLength'] for item in attachments.values()}
    for row in all_ops:
        for fact in row['byteFacts']:
            assert all_lengths.setdefault(fact['sha256'], fact['byteLength']) == fact['byteLength']
    blob_entries = {entry['sha256']: entry['byteLength'] for entry in manifest['inventory'] if entry['path'].startswith('blobs/')}
    assert pinned == blob_entries and sum(pinned.values()) <= 67108864 and max(pinned.values()) <= 10485760
    assert 0 in blob_entries.values()
    for name in ['preview-clear.json', 'preview-retention.json']:
        preview = read(name)
        original_hash = preview.pop('previewHash')
        assert sha(canonical(preview)) == original_hash
        assert timestamp(preview['expiresAt']) - timestamp(preview['previewedAt']) == timedelta(seconds=300)
        inventory = preview['inventory']
        assert inventory['counts'] == {'historySnapshots': len(inventory['history']),
                                       'activityEvents': len(inventory['activity']), 'outcomePayloads': len(inventory['outcomes'])}
        when = timestamp(preview['input']['throughTime']) if preview['command'] == 'history.clear' else timestamp(preview['previewedAt']) - timedelta(days=preview['input']['historyDays'])
        selected = [{'origin': row['origin'], 'cardId': row['cardId'], 'cardRevision': row['cardRevision']}
                    for row in history['records'] if row['state'] is not None and timestamp(row['committedAt']) <= when]
        assert inventory['history'] == selected
        when = timestamp(preview['input']['throughTime']) if preview['command'] == 'history.clear' else timestamp(preview['previewedAt']) - timedelta(days=preview['input']['outcomeDays'])
        selected = [{'origin': row['origin'], 'opId': row['opId']} for row in all_ops
                    if row['full'] is not None and timestamp(row['terminalAt']) <= when]
        assert inventory['outcomes'] == selected
        for key in ['domainBefore', 'domainAfter', 'exportPinned', 'reclaimable']:
            values = inventory['bytes'][key]
            unique_sorted(values, lambda row: row['sha256'])
            assert sum(row['byteLength'] for row in values) == inventory['bytes'][key + 'Bytes']
    preview = read('preview-clear.json')
    assert canonical({k: v for k, v in preview.items() if k != 'previewHash'}) == (ROOT / 'preview-clear.hash-input.bin').read_bytes()
    confirm = read('confirm-clear.json')
    intent = parse((ROOT / 'confirm-clear.canonical.bin').read_bytes())
    assert intent['payload'] == {'input': confirm['input'], 'previewHash': confirm['previewHash']}
    assert intent['target'] is None and intent['expectedCardRevision'] is None
    assert read('status-unknown.json')['result'] == {'sourceIdentity': None, 'sourceRevision': None, 'destination': None}
    receipt = read('restore-receipt.json')
    assert sha(readable(receipt['sourceManifest'])) == receipt['sourceManifestSha256']
    anchor = receipt['targetInventory']
    assert anchor['entryCount'] == len(anchor['entries']) and sha(canonical(anchor['entries'])) == anchor['sha256']
    for entry in anchor['entries']:
        prefix = 'package/' if entry['path'].startswith('blobs/') else 'prepared-target/'
        data = (ROOT / prefix / entry['path']).read_bytes()
        assert len(data) == entry['byteLength'] and sha(data) == entry['sha256']
    target_outcomes = read('prepared-target/outcomes.json')
    assert target_outcomes['operations'] == []
    assert target_outcomes['sourceArchives'][0]['identity'] == manifest['source']
    assert target_outcomes['identity']['incarnation'] != manifest['source']['incarnation']
    source_rows = {row['opId']: row for row in outcomes['operations']}
    restored_rows = {row['opId']: row for row in target_outcomes['sourceArchives'][0]['operations']}
    refusal_cases = read('refusal-cases.json')['cases']
    for case in refusal_cases:
        row = source_rows[case['opId']]
        assert row == restored_rows[case['opId']] and row['code'] == 'NOT_FOUND'
        assert row['terminalClass'] == 'NOT_COMMITTED' and row['target'] == case['target']
        assert (row['full'] is None) == (case['retention'] == 'compact')
        assert row['fingerprint'] == sha((ROOT / case['intentPath']).read_bytes())
        if 'cardId' in row['target']:
            assert row['target']['cardId'] not in cards
        else:
            assert 'grantId' in row['target']  # no live grant table/fabricated grant fact
        assert all(item['id'] not in row['target'].values() for item in board['tombstones'])
        assert row['resultRefs'] == []
        for fact in row['byteFacts']:
            assert fact['sha256'] not in blob_entries  # attempted bytes were never stored
    vectors = read('vectors.json')['vectors']
    for vector in vectors:
        data = (ROOT / vector['path']).read_bytes()
        assert len(data) == vector['byteLength'] and sha(data) == vector['sha256']
        if 'value' in vector:
            assert canonical(vector['value']) == data and data.hex() == vector['hex']
    assert vectors[0]['sha256'] != vectors[1]['sha256']  # absent differs from explicit null
    confirm_fields = set(confirm)
    cases = read('negative-cases.json')
    binary_rejected = 0
    # Binary negatives NEVER enter JSON parsing. Explicit equality assertions show
    # whether this exact byte case exercises digest-only or digest+length failure.
    for case in cases['binaryCases']:
        raw = (ROOT / case['path']).read_bytes()
        expected = case['expectedByteFact']
        length_mismatch = len(raw) != expected['byteLength']
        digest_mismatch = sha(raw) != expected['sha256']
        assert length_mismatch == case['lengthMismatch']
        assert digest_mismatch == case['digestMismatch']
        assert length_mismatch or digest_mismatch
        binary_rejected += 1
    json_rejected = 0
    for case in cases['jsonCases']:
        file = ROOT / case['path']
        try:
            value = parse(file.read_bytes())
            if file.name == 'bad-time.bin':
                timestamp(value['throughTime'])
            elif file.name in ['unknown-field.bin', 'absent-null.bin', 'null-not-input.bin']:
                if set(value) != confirm_fields or not isinstance(value['input'], dict):
                    raise ValueError('confirm shape')
        except (ValueError, UnicodeError):
            json_rejected += 1
        else:
            raise AssertionError('negative fixture unexpectedly accepted: ' + file.name)
    semantic_rejected = 0
    for case in cases['semanticCases']:
        row = read(case['path'])  # semantic negatives are valid readable JSON
        try:
            operation_semantics(row, board)
        except ValueError as error:
            assert str(error) == case['predicate'], (case['path'], str(error))
            semantic_rejected += 1
        else:
            raise AssertionError('semantic fixture unexpectedly accepted: ' + case['path'])
    print(f'D02 documentary fixtures PASS: {len(vectors)} byte/hash vectors, {len(manifest["inventory"])} payload entries, '
          f'{len(refusal_cases)} unresolved-target refusal positives, '
          f'{json_rejected} parser/schema + {binary_rejected} binary + {semantic_rejected} semantic negatives; no runtime qualification.')


if __name__ == '__main__':
    main()
