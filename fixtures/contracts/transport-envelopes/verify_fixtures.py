"""D03 documentary fixture checks; no product code, generator import or runtime."""
import base64, datetime, hashlib, json, re
from pathlib import Path
ROOT=Path(__file__).resolve().parent
D02=ROOT.parent/'archive-recovery'
U=re.compile(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z')
H=re.compile(r'[0-9a-f]{64}\Z')
RIGHTS=['approve','attach','comment','create','edit','export','grant-admin','history-admin','read','read-own-outcome','restore','review-handoff']
def need(x,msg='predicate'):
    if not x:raise ValueError(msg)
def integer(s):
    need(re.fullmatch(r'0|[1-9][0-9]*',s),'integer lexeme'); n=int(s);need(n<=9007199254740991,'safe integer');return n
def pairs(xs):
    d={}
    for k,v in xs:need(k not in d,'duplicate key');d[k]=v
    return d
def walk(x,depth=0):
    if isinstance(x,(dict,list)):
        need(depth<32,'depth')
        for v in (x.values() if isinstance(x,dict) else x):walk(v,depth+1)
    elif isinstance(x,str):need(not any(0xd800<=ord(c)<=0xdfff for c in x),'Unicode scalar')
def decode(raw):
    need(not raw.startswith(b'\xef\xbb\xbf'),'BOM')
    x=json.loads(raw.decode('utf-8'),object_pairs_hook=pairs,parse_int=integer,parse_float=lambda _:need(False,'noninteger'),parse_constant=lambda _:need(False,'nonfinite'))
    walk(x);return x
def quote(s):
    return '"'+''.join('\\'+c if c in '\\"' else '\\u%04x'%ord(c) if ord(c)<32 else c for c in s)+'"'
def enc(x,pretty=False,d=0):
    if x is None:return 'null'
    if type(x) is bool:return 'true' if x else 'false'
    if type(x) is int:return str(x)
    if isinstance(x,str):return quote(x)
    ob=isinstance(x,dict);items=[quote(k)+(': ' if pretty else ':')+enc(x[k],pretty,d+1) for k in sorted(x)] if ob else [enc(v,pretty,d+1) for v in x]
    a,z=('{','}') if ob else ('[',']')
    if not items:return a+z
    return a+'\n'+'  '*(d+1)+(',\n'+'  '*(d+1)).join(items)+'\n'+'  '*d+z if pretty else a+','.join(items)+z
def c(x):return enc(x).encode('utf-8')
def r(x):return (enc(x,True)+'\n').encode('utf-8')
def keys(x,s,optional=''):
    need(type(x) is dict,'object');a=set(s.split());o=set(optional.split());need(a<=set(x)<=a|o,'keys')
def uid(x):need(isinstance(x,str) and U.fullmatch(x) and x!='00000000-0000-0000-0000-000000000000','UUID')
def num(x,minimum=0):need(type(x) is int and minimum<=x<=9007199254740991,'number')
def tm(x):
    need(isinstance(x,str) and re.fullmatch(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z',x),'time shape');datetime.datetime.strptime(x,'%Y-%m-%dT%H:%M:%SZ')
def error(x,field=True):
    keys(x,'code message'+(' field' if field else ''));need(bool(x['code']) and bool(x['message']),'error text')
    if field:need(x['field'] is None or type(x['field']) is str,'field')
def conflict(x):
    if x is None:return
    keys(x,'incarnation revision cardId cardRevision');uid(x['incarnation']);num(x['revision']);need((x['cardId'] is None)==(x['cardRevision'] is None),'conflict pair')
    if x['cardId'] is not None:uid(x['cardId']);num(x['cardRevision'],1)
def result(command,x):
    shapes={'card.create':'cardId','card.edit':'cardId','review-handoff':'cardId','comment.add':'cardId commentId','attachment.add':'cardId attachmentId','link.add':'cardId linkId','link.remove':'cardId linkId','approve':'cardId approvalId','grant.create':'grantId','grant.revoke':'grantId','history.clear':'retentionVersion coverageRevision','retention.set':'retentionVersion coverageRevision'}
    keys(x,shapes[command])
    for k,v in x.items():num(v,1) if k.endswith('Version') or k=='coverageRevision' else uid(v)
def target(command,x):
    kind='cardId' if command not in ['card.create','grant.create','grant.revoke','history.clear','retention.set'] else 'grantId' if command=='grant.revoke' else None
    if kind:keys(x,kind);uid(x[kind])
    else:need(x is None,'null target')
def secret(s,n):
    need(type(s) is str and re.fullmatch('[A-Za-z0-9_-]+',s),'secret encoding')
    raw=base64.urlsafe_b64decode(s+'='*((-len(s))%4));need(len(raw)==n and base64.urlsafe_b64encode(raw).decode().rstrip('=')==s,'canonical secret')
def session(x):
    keys(x,'apiVersion actorId kind grant');need(x['apiVersion']==1,'version');uid(x['actorId']);need(x['kind'] in ['human','automation'],'kind');g=x['grant'];keys(g,'grantId boardId incarnation rights')
    for k in ['grantId','boardId','incarnation']:uid(g[k])
    need(g['rights']==sorted(set(g['rights'])) and all(q in RIGHTS for q in g['rights']),'rights')
    if x['kind']=='automation':need(not set(g['rights'])&{'approve','grant-admin','history-admin'},'automation rights')
def validate(x,f):
    need(type(x) is dict,'root')
    if f=='strict-json':return
    if f=='terminal':
        keys(x,'apiVersion outcome boardId incarnation opId actorId fingerprintVersion fingerprint command target expectedRevision expectedCardRevision terminalAt code revision cardRevision retention resultRefs byteFacts result removal')
        need(x['apiVersion']==x['fingerprintVersion']==1,'version');need(H.fullmatch(x['fingerprint']),'hash')
        for k in ['boardId','incarnation','opId','actorId']:uid(x[k])
        target(x['command'],x['target']);num(x['expectedRevision']);tm(x['terminalAt'])
        card=x['command'] not in ['card.create','grant.create','grant.revoke','history.clear','retention.set']
        if card:num(x['expectedCardRevision'],1)
        else:need(x['expectedCardRevision'] is None,'board precondition')
        need(x['outcome'] in ['committed','not_committed'],'outcome');need(x['retention'] in ['full','compact'],'retention')
        if x['outcome']=='committed':
            need(x['code']=='OK','commit code');num(x['revision'],1)
            if card or x['command']=='card.create':num(x['cardRevision'],1)
            else:need(x['cardRevision'] is None,'board card revision')
        else:need(x['revision'] is None and x['cardRevision'] is None and x['resultRefs']==[] and x['code']!='OK','refusal terminal')
        for ref in x['resultRefs']:
            keys(ref,'kind id revision');need(ref['kind'] in ['board','card','comment','link','attachment','approval','grant'],'fact kind');uid(ref['id'])
            if ref['revision'] is not None:num(ref['revision'])
        need(x['resultRefs']==sorted(x['resultRefs'],key=lambda v:(v['kind'],v['id'],-1 if v['revision'] is None else v['revision'])),'fact ordering')
        digests=[]
        for fact in x['byteFacts']:
            keys(fact,'sha256 byteLength');need(H.fullmatch(fact['sha256']),'byte hash');num(fact['byteLength']);digests.append(fact['sha256'])
        need(digests==sorted(set(digests)),'byte ordering')
        need(len(x['byteFacts'])==(1 if x['command']=='attachment.add' else 0),'command byte facts')
        if x['retention']=='full':
            need(x['removal'] is None,'full removal')
            if x['outcome']=='committed':result(x['command'],x['result'])
            else:keys(x['result'],'error conflict');error(x['result']['error']);conflict(x['result']['conflict']);need(x['result']['error']['code']==x['code'],'stored code')
        else:
            need(x['result'] is None,'compact result');keys(x['removal'],'reason removedAt boardRevision');need(x['removal']['reason'] in ['expired','cleared'],'removal');tm(x['removal']['removedAt']);num(x['removal']['boardRevision'])
    elif f=='refused':
        keys(x,'apiVersion outcome error conflict','opId');need(x['apiVersion']==1 and x['outcome']=='refused','refusal');error(x['error']);conflict(x['conflict'])
        if 'opId' in x:uid(x['opId'])
    elif f=='unknown':
        keys(x,'apiVersion outcome boardId incarnation opId actorId fingerprintVersion fingerprint error');need(x['apiVersion']==x['fingerprintVersion']==1 and x['outcome']=='unknown','unknown');need(H.fullmatch(x['fingerprint']),'hash');error(x['error'])
        for k in ['boardId','incarnation','opId','actorId']:uid(x[k])
    elif f=='session-read':session(x)
    elif f in ['credential-secret','code-secret']:
        extra='kind rights credentialId token' if f=='credential-secret' else 'code'
        keys(x,'apiVersion installationId boardId incarnation grantId actorId issuedAt expiresAt '+extra)
        for k in ['installationId','boardId','incarnation','grantId','actorId']+(['credentialId'] if f=='credential-secret' else []):uid(x[k])
        for k in ['issuedAt','expiresAt']:tm(x[k])
        need(x['apiVersion']==1,'version');secret(x['token'] if f=='credential-secret' else x['code'],32 if f=='credential-secret' else 16)
        need(datetime.datetime.fromisoformat(x['expiresAt'])-datetime.datetime.fromisoformat(x['issuedAt'])==datetime.timedelta(seconds=2592000 if f=='credential-secret' else 60),'expiry')
        if f=='credential-secret':session({'apiVersion':1,'actorId':x['actorId'],'kind':x['kind'],'grant':{k:x[k] for k in ['grantId','boardId','incarnation','rights']}})
    elif f=='exchange-secret':
        keys(x,'apiVersion token session issuedAt idleExpiresAt expiresAt');secret(x['token'],32);session(x['session'])
        for k in ['issuedAt','idleExpiresAt','expiresAt']:tm(x[k])
        for k,n in [('idleExpiresAt',1800),('expiresAt',28800)]:need(datetime.datetime.fromisoformat(x[k])-datetime.datetime.fromisoformat(x['issuedAt'])==datetime.timedelta(seconds=n),'session expiry')
    elif f=='credential-cli':
        keys(x,'apiVersion scope command outcome result error');need(x['scope']=='credential' and x['command'] in ['issue','revoke'],'credential scope');need(x['outcome'] in ['confirmed','refused','failed','unavailable','unknown'],'credential outcome')
        if x['result'] is not None:
            keys(x['result'],'installationId boardId incarnation grantId actorId credentialId issuedAt expiresAt locallyAvailable')
            for k,v in x['result'].items():
                if v is not None and k.endswith('Id') or k in ['boardId','incarnation'] and v is not None:uid(v)
                if k in ['issuedAt','expiresAt'] and v is not None:tm(v)
            need(type(x['result']['locallyAvailable']) is bool,'available')
        if x['outcome']=='confirmed':need(x['error'] is None and x['result'] is not None,'confirmed');need(x['result']['locallyAvailable']==(x['command']=='issue'),'availability')
        else:error(x['error'],False)
    elif f in ['export','restore']:
        keys(x,'apiVersion scope outcome result');need(x['apiVersion']==1 and x['scope']==f,'scope');keys(x['result'],'sourceIdentity sourceRevision destination')
        need(x['outcome'] in (['confirmed','failed','unknown','offered'] if f=='export' else ['confirmed','failed','unknown']),'delivery outcome')
        for k in ['sourceIdentity','destination']:
            v=x['result'][k]
            if v is not None:keys(v,'boardId incarnation');uid(v['boardId']);uid(v['incarnation'])
        if f=='export':need(x['result']['destination'] is None,'no export path')
    elif f=='read':
        keys(x,'apiVersion scope result');need(x['apiVersion']==1,'version')
        shapes={'board':'identity revision policyVersion retention retentionVersion coverage actors cards comments links attachments approvals','card':'identity revision card actors comments links attachments approvals historyReferences coverage','history':'identity revision cardId cardRevision coverage records actors comments links attachments approvals tombstones'}
        keys(x['result'],shapes[x['scope']]);keys(x['result']['identity'],'boardId incarnation');num(x['result']['revision'])
        if x['scope']=='history':
            view=x['result']
            referenced={identifier for record in view['records'] for identifier in record['references']['attachmentIds']}
            full_ids=[row['attachmentId'] for row in view['attachments']]
            tombstone_ids=[row['id'] for row in view['tombstones'] if row['kind']=='attachment']
            need(set(full_ids).isdisjoint(tombstone_ids) and len(full_ids)==len(set(full_ids)) and len(tombstone_ids)==len(set(tombstone_ids)) and set(full_ids+tombstone_ids)==referenced,'history attachment reference set')
            required_full={identifier for record in view['records'] if record['state'] is not None for identifier in record['references']['attachmentIds']}
            need(required_full<=set(full_ids),'full history attachment must be complete')
    elif f=='session-revoked':need(x=={'apiVersion':1,'scope':'session','outcome':'revoked'},'revoked')
    elif f=='preview':
        need(x==decode((D02/'preview-clear.json').read_bytes()),'unchanged preview');p=dict(x);h=p.pop('previewHash');need(hashlib.sha256(c(p)).hexdigest()==h,'preview hash')
    elif f=='request':pass # Requests mirror existing C01/D02 examples; not a product request parser.
    else:raise ValueError('unknown fixture family')
def main():
    index=decode((ROOT/'cases.json').read_bytes());count=0
    for entry in index['positive']+index['negative']:
        raw=(ROOT/entry['path']).read_bytes();need(len(raw)==entry['byteLength'] and hashlib.sha256(raw).hexdigest()==entry['sha256'],'byte anchor')
    for entry in index['positive']:
        raw=(ROOT/entry['path']).read_bytes();x=decode(raw);need(raw==r(x),'exact R bytes');validate(x,entry['family']);count+=1
        if entry['family']=='terminal':need(entry['proof'] and entry['exit']==(0 if x['outcome']=='committed' else 2) and entry['http']==200,'terminal mapping')
        else:need(not entry['proof'],'nonterminal proof')
        if entry['family']=='unknown':need(entry['exit']==4,'unknown exit')
        if entry.get('dispatch') in ['lookup','possibly-sent']:need(entry['exit']==4,'uncertain exit')
    for entry in index['negative']:
        try:validate(decode((ROOT/entry['path']).read_bytes()),entry['family'])
        except (ValueError,UnicodeError,TypeError,KeyError) as rejection:
            if entry['path'].startswith('negative-history-'):
                need(type(rejection) is ValueError and str(rejection)==entry['reason'],'history negative must exercise reference dependency')
            continue
        raise ValueError('negative was accepted: '+entry['path'])
    source=decode((D02/'package/outcomes.json').read_bytes())['operations'][0]
    raw=(ROOT/'intent.canonical.bin').read_bytes();need(raw==c(source['full']['intent']) and hashlib.sha256(raw).hexdigest()==source['fingerprint'],'original semantic anchor')
    for p in ROOT.glob('intent-*.canonical.bin'):
        raw=p.read_bytes();intent=decode(raw);need(raw==c(intent),'canonical intent bytes')
        terminal=decode((ROOT/('command-'+intent['command'].replace('.','-')+'.json')).read_bytes())
        need(hashlib.sha256(raw).hexdigest()==terminal['fingerprint'],'command fingerprint')
        for k in intent:
            if k!='payload':need(terminal[k]==intent[k],'original terminal identity')
    preview=(ROOT/'preview.json').read_bytes();need(preview==(D02/'preview-clear.json').read_bytes(),'preview unchanged bytes')
    for own,src in [('restore-confirmed.json','status-confirmed.json'),('restore-unknown.json','status-unknown.json')]:need((ROOT/own).read_bytes()==(D02/src).read_bytes(),'D02 envelope unchanged')
    for p in list(ROOT.glob('*.py'))+list(ROOT.glob('*.txt'))+list(ROOT.glob('*.json')):
        raw=p.read_bytes();need(b'\r' not in raw and raw.endswith(b'\n'),'LF final-newline text');raw.decode('utf-8')
    print('PASS: %d positive, %d negative documentary fixtures; byte, shape, proof/exit, original fingerprint and unchanged D02 anchors'%(count,len(index['negative'])))
if __name__=='__main__':main()
