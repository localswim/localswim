import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createLocalpostDiagnostics, projectDiagnostic } from '../../qualification/browser/lib/localpost.mjs';

const registrationId='10000000-0000-4000-8000-000000000001';
const runId='20000000-0000-4000-8000-000000000001';
const caseIds=['V02-LOCAL-01'];
const event={ caseId:caseIds[0], kind:'case', phase:'start', code:'OK' };
const reply=bytes=>new Response(JSON.stringify({filename:'localpost_019a4fb0-1a00-7000-8000-000000000001.dat',bytes}),{status:200});

test('fixed projection refuses arbitrary payload fields and identifiers',()=>{
  assert.deepEqual(Object.keys(projectDiagnostic(event,runId,1,new Set(caseIds))).sort(),['caseId','code','kind','phase','runId','schemaVersion','sequence','source']);
  for(const bad of [{...event,token:'secret'},{...event,code:'raw error'},{...event,caseId:'V02-UNKNOWN-01'},{...event,kind:'url'},null])
    assert.equal(projectDiagnostic(bad,runId,1,new Set(caseIds)),null);
  assert.equal(projectDiagnostic({caseId:caseIds[0],kind:'case',phase:'start',get code(){throw Error('hostile getter');}},runId,1,new Set(caseIds)),null);
});
test('one send preserves fixed bytes, omits credentials, validates receipt and never retries',async()=>{
  let count=0;
  const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher:async(url,request)=>{
    count++;assert.equal(url,`http://127.0.0.1:53147/api/2026-11-05/registrations/${registrationId}/messages`);
    assert.equal(request.credentials,'omit');assert.equal(request.redirect,'error');assert.equal(request.method,'POST');
    assert.equal(JSON.parse(request.body).sequence,1);return reply(Buffer.byteLength(request.body));
  }});
  assert.equal(await d.emit(event),true);await d.close();assert.equal(count,1);assert.equal(d.stats().delivered,1);
});
test('disabled or invalid configuration never invokes transport',async()=>{
  for(const options of [
    {registrationId:''},
    {serviceUrl:'https://127.0.0.1:53147/api/2026-11-05'},
    {serviceUrl:'http://example.com:53147/api/2026-11-05'},
    {serviceUrl:'http://127.0.0.1:53147'},
    {serviceUrl:'http://127.0.0.1:53147/api/2026-11-04'},
    {serviceUrl:'http://127.0.0.1:53147/api/latest'},
    {serviceUrl:'http://127.0.0.1:53147/api/2026-11-05/'},
    {serviceUrl:'http://127.0.0.1:53147/api/2026-11-05?extra=1'},
    {serviceUrl:'http://127.0.0.1:53147/api/2026-11-05#fragment'},
    {serviceUrl:'http://user@127.0.0.1:53147/api/2026-11-05'},
    {serviceUrl:'http://127.0.0.1:80/api/2026-11-05'},
  ]){
    const d=createLocalpostDiagnostics({registrationId,runId,caseIds,...options,fetcher:()=>{throw Error('must not run');}});
    assert.equal(await d.emit(event),false);assert.equal(d.stats().enabled,false);await d.close();
  }
});

test('dated API base supports a configured loopback service port',async()=>{
  const d=createLocalpostDiagnostics({registrationId,runId,caseIds,
    serviceUrl:'http://127.0.0.1:54147/api/2026-11-05',fetcher:async(url,request)=>{
      assert.equal(url,`http://127.0.0.1:54147/api/2026-11-05/registrations/${registrationId}/messages`);
      return reply(Buffer.byteLength(request.body));
    }});
  assert.equal(await d.emit(event),true);await d.close();
});
test('delivery failure is bounded, nonthrowing, disables later sends and does not retry',async()=>{
  let count=0;const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher:async()=>{count++;throw Error('private failure');}});
  const first=d.emit(event),second=d.emit(event);assert.equal(await first,false);assert.equal(await second,false);
  assert.equal(await d.emit(event),false);await d.close();assert.equal(count,1);assert.equal(d.stats().failed,1);
});

test('expired registration disables delivery without recreating or replaying it',async()=>{
  let count=0;
  const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher:async()=>{
    count++;return new Response('{"error":{"code":"registration_not_found"}}',{status:404});
  }});
  assert.equal(await d.emit(event),false);assert.equal(await d.emit(event),false);
  await d.close();assert.equal(count,1);assert.equal(d.stats().offline,true);
});
test('malformed, oversized or wrong-size receipt is not accepted',async()=>{
  for(const fetcher of [async()=>new Response('{}'),async()=>new Response('x'.repeat(2049)),async()=>reply(0),async()=>new Response('',{status:503})]){
    const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher});assert.equal(await d.emit(event),false);await d.close();assert.equal(d.stats().failed,1);
  }
});
test('message volume is capped for the complete run',async()=>{
  let count=0;const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher:async(_url,r)=>{count++;return reply(Buffer.byteLength(r.body));}});
  await Promise.all(Array.from({length:70},()=>d.emit(event)));await d.close();assert.equal(count,64);assert.equal(d.stats().rejected,6);
});
test('browser hooks never read error, request or console prose and detach exactly',async()=>{
  const sent=[];const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher:async(_url,r)=>{sent.push(JSON.parse(r.body));return reply(Buffer.byteLength(r.body));}});
  const page=new EventEmitter(),detach=d.attach(page,caseIds[0]);
  const secret=new Proxy({},{get(){throw Error('raw browser payload read');}});
  page.emit('pageerror',secret);page.emit('requestfailed',secret);page.emit('console',{type:()=> 'error',text:()=>{throw Error('prose read');}});
  assert.doesNotThrow(()=>page.emit('console',{type:()=>{throw Error('unavailable browser message');}}));
  detach();page.emit('pageerror',secret);await d.close();assert.equal(sent.length,3);assert.deepEqual(sent.map(x=>x.code),['PAGE_ERROR','REQUEST_FAILED','CONSOLE_ERROR']);
  assert.equal(page.listenerCount('pageerror'),0);assert.equal(page.listenerCount('console'),0);
});
test('stalled transport is cancelled within the bounded diagnostic close',async()=>{
  const d=createLocalpostDiagnostics({registrationId,runId,caseIds,fetcher:(_url,{signal})=>new Promise((resolve,reject)=>{
    signal.addEventListener('abort',()=>reject(Error('aborted')),{once:true});
  })});
  const before=Date.now();void d.emit(event);await d.close();
  assert.ok(Date.now()-before<2500);assert.equal(d.stats().failed,1);
});
