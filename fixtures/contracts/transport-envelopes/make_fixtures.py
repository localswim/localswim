"""Generate invented documentary transport examples; never product/runtime data."""
import base64, copy, hashlib, json
from pathlib import Path
ROOT=Path(__file__).resolve().parent
D02=ROOT.parent/'archive-recovery'
def c(x):
    return enc(x).encode('utf-8')
def r(x):
    return (enc(x,True)+'\n').encode('utf-8')
def quote(s):
    return '"'+''.join('\\'+v if v in '\\"' else '\\u%04x'%ord(v) if ord(v)<32 else v for v in s)+'"'
def enc(x,pretty=False,d=0):
    if x is None:return 'null'
    if type(x) is bool:return 'true' if x else 'false'
    if type(x) is int:return str(x)
    if isinstance(x,str):return quote(x)
    ob=isinstance(x,dict)
    items=[quote(k)+(': ' if pretty else ':')+enc(x[k],pretty,d+1) for k in sorted(x)] if ob else [enc(v,pretty,d+1) for v in x]
    a,z=('{','}') if ob else ('[',']')
    if not items:return a+z
    return a+'\n'+'  '*(d+1)+(',\n'+'  '*(d+1)).join(items)+'\n'+'  '*d+z if pretty else a+','.join(items)+z
def load(p):return json.loads((D02/p).read_bytes())
def u(n):return '00000000-0000-4000-8000-%012x'%n
cases=[]
def add(name,body,family,http,exit,proof=False,secret=False,**extra):
    data=r(body); (ROOT/(name+'.json')).write_bytes(data)
    cases.append(dict(path=name+'.json',family=family,http=http,exit=exit,proof=proof,
        secretTransport=secret,byteLength=len(data),sha256=hashlib.sha256(data).hexdigest(),**extra))
b=load('package/board.json'); identity=b['identity']; B=identity['boardId']; I=identity['incarnation']; A=u(10); C=u(11); G=u(80); ID=u(81)
ops=load('package/outcomes.json')['operations']
for n,op in enumerate(ops):
    t={k:op[k] for k in ['opId','actorId','fingerprintVersion','fingerprint','command','target','expectedRevision','expectedCardRevision','terminalAt','code','resultRefs','byteFacts','removal']}
    t.update(apiVersion=1,outcome='committed' if op['terminalClass']=='COMMITTED' else 'not_committed',**op['origin'],revision=op['committedRevision'],cardRevision=op['committedCardRevision'],retention='full' if op['full'] else 'compact',result=op['full']['result'] if op['full'] else None)
    add('terminal-source-%02d'%n,t,'terminal',200,0 if t['outcome']=='committed' else 2,True,sourceOperation=op['opId'])
    if n==0:
        (ROOT/'intent.canonical.bin').write_bytes(c(op['full']['intent']))
        add('request-attachment',{k:op['full']['intent'][k] for k in ['opId','incarnation','target','expectedRevision','expectedCardRevision']}|{'input':op['full']['intent']['payload']},'request',None,None)
        full=t
compact=copy.deepcopy(full);compact.update(retention='compact',result=None,removal={'reason':'expired','removedAt':'2026-10-25T00:00:00Z','boardRevision':6})
add('terminal-committed-compact',compact,'terminal',200,0,True)
inputs={
 'card.create':{'title':'Synthetic','description':'','priority':None},
 'card.edit':{'title':None,'description':'Draft','priority':None,'lane':None},
 'comment.add':{'text':'Synthetic comment'},
 'attachment.add':ops[0]['full']['intent']['payload'],
 'link.add':{'kind':'informational','targetCardId':u(90)},'link.remove':{'linkId':u(91)},
 'review-handoff':{},'approve':{'policyVersion':1},
 'grant.create':{'granteeActorId':u(92),'kind':'automation','rights':['read']},
 'grant.revoke':{},
 'history.clear':{'input':{'throughTime':'2026-10-01T00:00:00Z'},'previewHash':load('preview-clear.json')['previewHash']},
 'retention.set':{'input':{'historyDays':90,'activityDays':365,'outcomeDays':180},'previewHash':load('preview-retention.json')['previewHash']}}
for n,(command,payload) in enumerate(inputs.items()):
    card=command not in ['card.create','grant.create','grant.revoke','history.clear','retention.set']
    target={'cardId':C} if card else {'grantId':G} if command=='grant.revoke' else None
    intent={'fingerprintVersion':1,'boardId':B,'incarnation':I,'opId':u(200+n),'actorId':A,'command':command,'target':target,'payload':payload,'expectedRevision':5,'expectedCardRevision':4 if card else None}
    res={'cardId':C}
    if command in ['grant.create','grant.revoke']:res={'grantId':G}
    elif command in ['history.clear','retention.set']:res={'retentionVersion':2 if command=='retention.set' else 1,'coverageRevision':6}
    else:
        key={'comment.add':'commentId','attachment.add':'attachmentId','link.add':'linkId','link.remove':'linkId','approve':'approvalId'}.get(command)
        if key:res[key]=u(91) if key=='linkId' else u(300+n)
    refs=[{'kind':'card','id':C,'revision':5}] if card or command=='card.create' else [{'kind':'grant','id':G,'revision':None}] if command.startswith('grant.') else [{'kind':'board','id':B,'revision':6}]
    for key,value in res.items():
        if key not in ['cardId','grantId','coverageRevision','retentionVersion']:refs.append({'kind':key[:-2],'id':value,'revision':None})
    refs.sort(key=lambda v:(v['kind'],v['id']))
    z={k:v for k,v in intent.items() if k!='payload'}
    z.update(apiVersion=1,outcome='committed',fingerprint=hashlib.sha256(c(intent)).hexdigest(),terminalAt='2026-10-25T00:00:00Z',code='OK',revision=6,cardRevision=5 if card or command=='card.create' else None,retention='full',resultRefs=refs,byteFacts=ops[0]['byteFacts'] if command=='attachment.add' else [],result=res,removal=None)
    add('command-'+command.replace('.','-'),z,'terminal',200,0,True)
    (ROOT/('intent-'+command.replace('.','-')+'.canonical.bin')).write_bytes(c(intent))
for name,code,http,ex in [('unauthenticated','UNAUTHENTICATED',401,2),('invalid-input','INVALID_INPUT',400,2),('forbidden','FORBIDDEN',403,2),('not-found','NOT_FOUND',404,2),('oversize','INPUT_TOO_LARGE',413,2),('unavailable-read','UNAVAILABLE',503,3),('local-io','INVOCATION_OR_IO',None,1),('selector-required','CREDENTIAL_SELECTION_REQUIRED',None,2)]:
    add(name,{'apiVersion':1,'outcome':'refused','error':{'code':code,'message':'Synthetic diagnostic','field':None},'conflict':None},'refused',http,ex)
ref={'apiVersion':1,'outcome':'refused','opId':full['opId'],'error':{'code':'REVISION_CONFLICT','message':'Synthetic stale revision','field':'expectedRevision'},'conflict':{'incarnation':I,'revision':5,'cardRevision':4,'cardId':C}}
add('conflict-no-proof',ref,'refused',409,4,dispatch='possibly-sent')
ref=copy.deepcopy(ref);ref['error']['code']='OPERATION_MISMATCH';ref['conflict']=None
add('lookup-mismatch-no-proof',ref,'refused',409,4,dispatch='lookup')
for name,code,http in [('unknown','OUTCOME_UNKNOWN',503),('lookup-missing','OUTCOME_UNKNOWN',200),('lookup-denied','UNAUTHENTICATED',None),('source-authority','SOURCE_AUTHORITY_UNAVAILABLE',200)]:
    z={k:full[k] for k in ['apiVersion','boardId','incarnation','opId','actorId','fingerprintVersion','fingerprint']};z.update(outcome='unknown',error={'code':code,'message':'Original outcome unavailable','field':None})
    add(name,z,'unknown',http,4)
view={k:b[k] for k in ['identity','revision','policyVersion','retention','retentionVersion','coverage','actors','cards','comments','links','attachments','approvals']}
add('board-read',{'apiVersion':1,'scope':'board','result':view},'read',200,0)
h=load('package/history.json'); refs=[{k:x[k] for k in ['origin','cardId','cardRevision','committedAt']}|{'available':x['state'] is not None} for x in h['records']]
cv={k:b[k] for k in ['identity','revision','actors','comments','links','attachments','approvals','coverage']};cv.update(card=b['cards'][0],historyReferences=refs)
add('card-read',{'apiVersion':1,'scope':'card','result':cv},'read',200,0)
hv={k:b[k] for k in ['identity','revision','coverage','actors','comments','links','attachments','approvals','tombstones']};hv.update(cardId=C,cardRevision=b['cards'][0]['revision'],records=h['records'])
history_attachment_ids={attachment_id for snapshot in hv['records'] for attachment_id in snapshot['references']['attachmentIds']}
hv['attachments']=[row for row in b['attachments'] if row['attachmentId'] in history_attachment_ids]
add('history-read',{'apiVersion':1,'scope':'history','result':hv},'read',200,0)
session={'apiVersion':1,'actorId':A,'kind':'human','grant':{'grantId':G,'boardId':B,'incarnation':I,'rights':['read','read-own-outcome']}}
add('session-read',session,'session-read',200,0)
issue={'installationId':ID,'boardId':B,'incarnation':I,'grantId':G}
add('code-request',issue,'request',None,None)
add('credential-issue-request',issue,'request',None,None)
code=base64.urlsafe_b64encode(bytes(16)).decode().rstrip('='); token=base64.urlsafe_b64encode(bytes(32)).decode().rstrip('=')
base=issue|{'apiVersion':1,'actorId':A,'issuedAt':'2026-10-25T00:00:00Z','expiresAt':'2026-11-24T00:00:00Z'}
add('credential-secret',base|{'kind':'human','rights':['read','read-own-outcome'],'credentialId':u(82),'token':token},'credential-secret',201,None,secret=True)
add('code-secret',base|{'code':code,'expiresAt':'2026-10-25T00:01:00Z'},'code-secret',201,None,secret=True)
add('exchange-request',{'code':code},'request',None,None,secret=True)
add('exchange-secret',{'apiVersion':1,'token':token,'session':session,'issuedAt':'2026-10-25T00:00:00Z','idleExpiresAt':'2026-10-25T00:30:00Z','expiresAt':'2026-10-25T08:00:00Z'},'exchange-secret',200,None,secret=True)
add('session-revoked',{'apiVersion':1,'scope':'session','outcome':'revoked'},'session-revoked',200,0)
public={k:v for k,v in base.items() if k!='apiVersion'}|{'credentialId':u(82),'locallyAvailable':True}
for name,out,ex in [('confirmed','confirmed',0),('refused','refused',2),('failed','failed',1),('unavailable','unavailable',3),('unknown','unknown',4)]:
    add('credential-cli-'+name,{'apiVersion':1,'scope':'credential','command':'issue','outcome':out,'result':public if ex==0 else None,'error':None if ex==0 else {'code':'CREDENTIAL_OUTCOME_UNKNOWN' if ex==4 else 'INVOCATION_OR_IO' if ex==1 else 'UNAVAILABLE' if ex==3 else 'FORBIDDEN','message':'Synthetic credential diagnostic'}},'credential-cli',None,ex)
rev=copy.deepcopy(public);rev['locallyAvailable']=False
add('credential-cli-revoke',{'apiVersion':1,'scope':'credential','command':'revoke','outcome':'confirmed','result':rev,'error':None},'credential-cli',None,0)
add('preview',load('preview-clear.json'),'preview',200,0)
add('preview-request',load('preview-request.json'),'request',None,None)
add('confirm-request',load('confirm-clear.json'),'request',None,None)
add('restore-confirmed',load('status-confirmed.json'),'restore',None,0)
add('restore-unknown',load('status-unknown.json'),'restore',None,4)
add('restore-failed',{'apiVersion':1,'scope':'restore','outcome':'failed','result':{'sourceIdentity':None,'sourceRevision':None,'destination':None}},'restore',None,1)
add('export-request',{'incarnation':I,'expectedRevision':5,'input':{}},'request',None,None)
for out,ex in [('offered',None),('confirmed',0),('failed',1),('unknown',4)]:
    add('export-'+out,{'apiVersion':1,'scope':'export','outcome':out,'result':{'sourceIdentity':identity if out in ['offered','confirmed'] else None,'sourceRevision':5 if out in ['offered','confirmed'] else None,'destination':None}},'export',None,ex)
negative=[]
def neg(name,body,family,reason):
    data=body if isinstance(body,bytes) else r(body); (ROOT/(name+'.bin')).write_bytes(data)
    negative.append({'path':name+'.bin','family':family,'reason':reason,'byteLength':len(data),'sha256':hashlib.sha256(data).hexdigest()})
x=copy.deepcopy(full);x['result']['token']=token;neg('negative-terminal-secret',x,'terminal','unknown typed result key')
x=copy.deepcopy(full);x['revision']=None;neg('negative-committed-no-revision',x,'terminal','committed revision required')
x=copy.deepcopy(compact);x['result']=full['result'];neg('negative-compact-result',x,'terminal','pruned full result cannot be invented')
x=copy.deepcopy(full);del x['expectedCardRevision'];neg('negative-missing-key',x,'terminal','required key absent')
x=copy.deepcopy(ref);x['opId']=None;neg('negative-opid-null',x,'refused','optional opId absent or U, never null')
x=copy.deepcopy(ref);x['actorId']=A;neg('negative-refused-identity',x,'refused','refused cannot imply terminal identity')
x={'apiVersion':1,'outcome':'unknown','boardId':B,'incarnation':I,'opId':full['opId'],'actorId':A,'fingerprintVersion':1,'fingerprint':full['fingerprint'],'error':{'code':'OUTCOME_UNKNOWN','message':'Synthetic','field':None},'revision':5};neg('negative-unknown-revision',x,'unknown','unknown cannot carry committed revision')
for name,data in [('duplicate',b'{"apiVersion":1,"apiVersion":1}\n'),('overflow',b'{"n":9007199254740992}\n'),('exponent',b'{"n":1e0}\n'),('invalid-utf8',b'{"x":"\xff"}\n'),('surrogate',b'{"x":"\\ud800"}\n'),('bom',b'\xef\xbb\xbf{}\n')]:neg('negative-'+name,data,'strict-json','strict decoded bytes')
x=copy.deepcopy(full);x['actorId']=x['actorId'].upper();neg('negative-uuid-uppercase',x,'terminal','canonical UUID')
x=copy.deepcopy(full);x['terminalAt']='2026-02-30T00:00:00Z';neg('negative-invalid-time',x,'terminal','Gregorian date')
x=copy.deepcopy(session);x['grant']['rights']=['read-own-outcome','read'];neg('negative-right-order',x,'session-read','sorted rights')
x=copy.deepcopy(session);x['kind']='automation';x['grant']['rights']=['approve'];neg('negative-automation-approve',x,'session-read','forbidden kind/right combination')
neg('negative-secret-padding',base|{'code':code+'=','expiresAt':'2026-10-25T00:01:00Z'},'code-secret','unpadded canonical secret')
x={'apiVersion':1,'scope':'history','result':copy.deepcopy(hv)}
x['result']['attachments']=copy.deepcopy(b['attachments'])
neg('negative-history-extra-attachment',x,'read','history attachment reference set')
x={'apiVersion':1,'scope':'history','result':copy.deepcopy(hv)}
x['result']['attachments']=[]
neg('negative-history-missing-attachment',x,'read','history attachment reference set')
(ROOT/'cases.json').write_bytes(r({'schemaVersion':1,'positive':cases,'negative':negative}))
(ROOT/'README.txt').write_text('D03 SYNTHETIC DOCUMENTARY WIRE FIXTURES\n\nAll values are invented. Secret-transport examples use canonical base64url of\nzero bytes for shape checks only: they are not issued credentials, grants or\nruntime-ready randomness. Never use them as secrets. No real board was read.\n\nRun python verify_fixtures.py. Generation: python make_fixtures.py.\nThe verifier reads files without executing the generator. cases.json anchors each\nexact byte length/hash and HTTP/CLI/proof context. *.bin are deliberately exact\nbytes, including invalid UTF-8/BOM/duplicate/unsafe-number negatives.\nintent.canonical.bin is exact no-LF C bytes; JSON files are D02 R UTF-8/LF bytes.\nThe source terminal/read/preview/status examples derive only from the sibling\nsynthetic D02 fixture tree; D02 remains authoritative for its internal structures.\n\nThis checks documentary shapes, fixture integrity and selected invariants only;\nno product/native/authentication/HTTP/archive/browser/durability qualification.\n',encoding='utf-8',newline='\n')
# Normalize this owned text document only; never normalize exact binary vectors.
p=ROOT.parents[2]/'docs/contracts/transport-envelopes.txt'
p.write_text(p.read_text(encoding='utf-8'),encoding='utf-8',newline='\n')
print('Generated documentary transport fixtures:',len(cases),'positive,',len(negative),'negative')
