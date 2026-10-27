import hashlib,json,os,pathlib,shutil,subprocess,sys,time
REPO=pathlib.Path(__file__).resolve().parents[3]
ART=REPO.parent/'artifacts'/'2026-10-25-slice-01'
# New attempt every invocation; never reuses an old runtime, source or evidence.
RUN=ART/('v02-native-'+time.strftime('%Y%m%d-%H%M%S'))
RUN.mkdir();(RUN/'logs').mkdir();(RUN/'results').mkdir()
(RUN/'results'/'attempt.json').write_text(json.dumps({'status':'STARTED','started':time.strftime('%Y-%m-%dT%H:%M:%S'),'scope':'prepare only; no service/trust/grant/code/browser actions'})+'\n')
subprocess.run(['git','archive','--format=tar',f'--output={RUN/"source-before.tar"}','HEAD'],cwd=REPO,check=True)
SOURCE=RUN/'source';SOURCE.mkdir()
import tarfile
with tarfile.open(RUN/'source-before.tar') as t:t.extractall(SOURCE,filter='data')
for p in pathlib.Path(__file__).parent.glob('*'):
 if p.is_file():
  target=SOURCE/'qualification'/'browser'/'native'/p.name;target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(p,target)
BASE=ART/'WORK'/('v2-'+time.strftime('%H%M%S'))
BASE.mkdir();ROOT=BASE/'root'
# ACL protected using exact current user plus SYSTEM; no group grants. No actual SID is published.
import ctypes,re
from ctypes import wintypes
identity=subprocess.run(['whoami.exe','/user','/fo','csv','/nh'],capture_output=True,check=True)
sid=re.search(rb'S-1-5-[0-9-]+',identity.stdout)
if not sid:raise SystemExit('current user SID unavailable')
sddl='O:'+sid.group().decode()+'D:P(A;OICI;FA;;;'+sid.group().decode()+')(A;OICI;FA;;;SY)'
adv=ctypes.WinDLL('advapi32',use_last_error=True);kernel=ctypes.WinDLL('kernel32',use_last_error=True)
convert=adv.ConvertStringSecurityDescriptorToSecurityDescriptorW;convert.argtypes=[wintypes.LPCWSTR,wintypes.DWORD,ctypes.POINTER(ctypes.c_void_p),ctypes.POINTER(wintypes.DWORD)];convert.restype=wintypes.BOOL
setsec=adv.SetFileSecurityW;setsec.argtypes=[wintypes.LPCWSTR,wintypes.DWORD,ctypes.c_void_p];setsec.restype=wintypes.BOOL
sd=ctypes.c_void_p()
if not convert(sddl,1,ctypes.byref(sd),None):raise SystemExit('private descriptor unavailable')
try:
 if not setsec(str(BASE),0x80000005,sd):raise SystemExit('private ACL preparation failed')
finally:kernel.LocalFree(sd)
(RUN/'logs'/'acl.log').write_text('Protected current-user and SYSTEM only ACL applied.\n')
original=SOURCE/'internal'/'transport'/'setup'/'native_windows.go'
s=original.read_text();start=s.index('func nativeRoot() (string, error) {');end=s.index('\nfunc newNativeFS',start)
guard='func nativeRoot() (string, error) {\n p := '+json.dumps(str(ROOT))+'\n if !filepath.IsAbs(p) || filepath.Clean(p) != p { return "", fail("PLATFORM_UNQUALIFIED",3) }; return p,nil\n}\nfunc V02QualificationRoot() (string,error) { return nativeRoot() }\n'
overlay=RUN/'native-root-overlay.go';overlay.write_text(s[:start]+guard+s[end:],encoding='utf-8',newline='\n')
(RUN/'overlay.json').write_text(json.dumps({'Replace':{str(original):str(overlay)}}),encoding='utf-8')
cmd=SOURCE/'cmd'/'v02fixture';cmd.mkdir()
shutil.copyfile(SOURCE/'cmd'/'localswim'/'routes.go',cmd/'routes.go')
for p in pathlib.Path(__file__).parent.glob('*.go'):shutil.copyfile(p,cmd/p.name)
ENV=os.environ.copy();ENV.update(GOTOOLCHAIN='local',CGO_ENABLED='0',GOOS='windows',GOARCH='amd64',GOFLAGS='-mod=readonly',GOPROXY='off',GOSUMDB='off',GOMODCACHE=str(ART/'q01-cache'/'gomodcache'),GOCACHE=str(RUN/'cache'),GOTMPDIR=str(RUN/'gotmp'))
shutil.copytree(ART/'p02-integration-r2'/'cache',RUN/'cache');(RUN/'gotmp').mkdir()
GO='C:/Program Files/Go/bin/go.exe'
GO_SHA256=hashlib.sha256(pathlib.Path(GO).read_bytes()).hexdigest()
Q04=json.loads((REPO/'qualification'/'build'/'results.json').read_text())
if GO_SHA256!=Q04['tool_sha256']['go.exe']:raise SystemExit('qualified Go executable hash mismatch')
def run(label,args):
 p=subprocess.run(args,cwd=SOURCE,env=ENV,capture_output=True);(RUN/'logs'/f'{label}.log').write_bytes(p.stdout+p.stderr);return p
v=run('version',[GO,'version'])
if b'go1.27.3 windows/amd64' not in v.stdout:raise SystemExit('unqualified Go')
# Mandatory negative compiled first, never executed; sentinel must be undefined.
p=run('missing-overlay',[GO,'build','-tags=v02qualification','-trimpath','-buildvcs=false','-o',str(RUN/'negative.exe'),'./cmd/v02fixture'])
if p.returncode==0 or b'undefined: setup.V02QualificationRoot' not in p.stderr:raise SystemExit('missing overlay guard did not fail as expected')
p=run('build',[GO,'build','-tags=v02qualification','-overlay',str(RUN/'overlay.json'),'-trimpath','-buildvcs=false','-o',str(BASE/'fixture.exe'),'./cmd/v02fixture'])
if p.returncode:raise SystemExit('fixture compile failed; inspect private build log; no automatic retry')
p=run('mechanics-tests',[GO,'test','-tags=v02qualification','-overlay',str(RUN/'overlay.json'),'-count=1','-timeout=30s','./cmd/v02fixture'])
if p.returncode:raise SystemExit('fixture mechanical tests failed; preserve attempt; no automatic retry')
p=run('vet',[GO,'vet','-tags=v02qualification','-overlay',str(RUN/'overlay.json'),'./cmd/v02fixture'])
if p.returncode:raise SystemExit('fixture vet failed; preserve attempt; no automatic retry')
# No executable can run until guard negative and instrumented build both passed.
p=run('prepare',[str(BASE/'fixture.exe'),'prepare'])
if p.returncode:raise SystemExit('instrumented preparation failed; preserve attempt; no automatic retry')
h=lambda p:hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
result={'status':'PREPARED_ONLY','attempt':str(RUN),'anchor':str(BASE),'manifest':str(BASE/'run-manifest.json'),'rootAllowlist':[str(ROOT)],'originalRootSha256':h(original),'overlaySha256':h(overlay),'fixtureSha256':h(BASE/'fixture.exe'),'sourceBeforeSha256':h(RUN/'source-before.tar'),'normalMissingSentinel':'PASS','mechanicsTest':'PASS delivery barrier preserves actual handler response; no listener','vet':'PASS','goSha256':GO_SHA256,'trustEnrollment':False,'humanEnrollment':False,'serviceListenersStarted':False,'nativeSetupPortAvailabilityProbe':'production Init temporary exclusive bind and close','codeIssued':False,'browserActivated':False,'defaultReleaseResultsSha256':h(REPO/'qualification'/'build'/'results.json'),'productionRoutesSha256':h(SOURCE/'cmd'/'localswim'/'routes.go'),'fixtureRoutesSha256':h(cmd/'routes.go'),'sourceHashes':{str(p.relative_to(SOURCE)):h(p) for p in SOURCE.rglob('*') if p.is_file()}}
(RUN/'results'/'preparation.json').write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8');print(str(RUN))
