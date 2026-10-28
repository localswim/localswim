"""Validate the frozen workspace inventory against hashed, private run evidence.

The manifest contains no credentials or board prose. Evidence stays private. This
checker does not execute scenarios and cannot turn a component test into UI proof.
"""
import argparse
import hashlib
import json
import re
import datetime
import tarfile
from pathlib import Path, PureWindowsPath

GO_SHA256 = "83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d"
NATIVE_FILES = {"invocation.json", "execution.jsonl", "source-before.json", "source-after.json", "native_targeted.py"}
CASE_INPUTS = {"internal/transport/local/" + name for name in ("workspace_files.go", "workspace_files_windows.go", "workspace_files_unsupported.go", "workspace_files_windows_test.go", "files_windows.go", "runtime_windows_test.go")} | {"go.mod", "go.sum"}


def require(value, message):
    if not value:
        raise ValueError(message)


def object_pairs(rows):
    result = {}
    for key, value in rows:
        require(key not in result, "duplicate JSON field: " + key)
        result[key] = value
    return result


def load_json(path):
    return json.loads(path.read_text(encoding="utf-8-sig"), object_pairs_hook=object_pairs)


def load(path):
    value = load_json(path)
    require(isinstance(value, dict), "JSON report must be an object")
    return value


def string_list(value, label, nonempty=True):
    require(isinstance(value, list) and (value or not nonempty), label + " must be a list")
    require(all(isinstance(v, str) and v for v in value), label + " must contain strings")
    require(len(value) == len(set(value)), label + " has duplicates")
    return value


def safe_file(root, relative, digest):
    require(isinstance(relative, str) and relative and "\\" not in relative and not Path(relative).is_absolute() and not PureWindowsPath(relative).drive and all(v not in (".", "..", "") for v in relative.split("/")), "unsafe relative evidence path")
    target = (root / relative).resolve()
    require(target.is_relative_to(root) and target.is_file(), "missing or unsafe evidence file")
    require(isinstance(digest, str) and re.fullmatch(r"[0-9a-f]{64}", digest) and sha(target) == digest, "evidence SHA256 mismatch")
    return target


def timestamp(value):
    require(isinstance(value, str), "invalid timestamp")
    result = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    require(result.tzinfo is not None, "timestamp requires timezone")
    return result


def actual_outcome(value):
    if isinstance(value, list):
        require(value and all(isinstance(v, dict) for v in value), "malformed command attempt list")
        outcomes = [actual_outcome(v.get("command", v)) for v in value]
        return "FAIL" if "FAIL" in outcomes else "PASS"
    require(isinstance(value, dict), "attempt report must be object")
    code = value.get("exit", value.get("exitCode"))
    require(code is None or type(code) is int, "invalid attempt exit")
    counts = value.get("counts", {})
    require(isinstance(counts, dict) and all(type(v) is int and v >= 0 for v in counts.values()), "invalid attempt counts")
    status = value.get("status", "")
    require(isinstance(status, str), "invalid attempt status")
    failed = code is not None and code != 0 or any(counts.get(k, 0) for k in ("fail", "skip"))
    failed = failed or any(value.get(k) for k in ("error", "errorType", "failure", "cleanupFailure", "validationError", "failuresOrSkips", "timedOut", "goFailureEvents", "goFailEvents")) or any(value.get(k) is False for k in ("complete", "sourceStable", "candidateStable"))
    if failed or status.startswith("FAIL") or status in ("ERROR", "INCOMPLETE"):
        return "FAIL"
    if "commands" in value:
        return actual_outcome(value["commands"])
    require(code == 0 or status.startswith("PASS"), "attempt has no terminal outcome")
    return "PASS"


def parse_events(path):
    events = [json.loads(line, object_pairs_hook=object_pairs) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    require(events and all(isinstance(e, dict) and e.get("Action") in {"start", "run", "pause", "cont", "pass", "output", "bench", "fail", "skip", "build-output", "build-fail"} and isinstance(e.get("Package"), str) and e["Package"] and ("Test" not in e or isinstance(e["Test"], str) and e["Test"]) for e in events), "malformed Go JSON event stream")
    require(not any(e["Action"] in ("fail", "skip", "build-fail") for e in events), "Go invocation failed or skipped")
    require(all(sum(e["Action"] == "pass" and not e.get("Test") and e["Package"] == p for e in events) == 1 for p in {e["Package"] for e in events}), "Go package completion missing or duplicate")
    passed = [e["Test"] for e in events if e["Action"] == "pass" and e.get("Test")]
    require(all(isinstance(t, str) for t in passed) and len(passed) == len(set(passed)), "duplicate or malformed Go test PASS")
    return events


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def product_bindings(root):
    files = {root / name for name in ("go.mod", "go.sum", "web/package-lock.json", "web/index.html", "web/package.json", "web/tsconfig.json")}
    for name in ("internal", "cmd", "schemas", "web/src", "web/generated", "web/styles", "web/scripts"):
        require((root / name).is_dir(), "missing product directory")
        files.update(p for p in (root / name).rglob("*") if p.is_file())
    require(all(p.is_file() and not p.is_symlink() and p.resolve().is_relative_to(root) for p in files), "unsafe/missing product source")
    return {p.relative_to(root).as_posix(): sha(p) for p in sorted(files)}


def only_product(bindings, expected):
    # Historical execution snapshots may additionally bind qualification metadata;
    # those bytes remain retained but are outside product-source correspondence.
    require(isinstance(bindings, dict) and all(isinstance(k, str) and isinstance(v, str) for k, v in bindings.items()), "invalid source binding map")
    return {key: value for key, value in bindings.items() if not key.startswith("qualification/")}


def reference(root, value):
    require(isinstance(value, dict), "malformed artifact reference")
    return safe_file(root, value.get("path"), value.get("sha256"))


def pinned_invocation(value, candidate=None):
    require(isinstance(value, dict), "malformed selected invocation")
    argv = value.get("argv")
    if argv is not None:
        string_list(argv, "selected invocation argv")
        tool, args = Path(argv[0]), argv[1:]
    else:
        tool, args = Path(value.get("tool", "MISSING")), value.get("args")
    string_list(args, "selected invocation args")
    tool_hashes = [value[name] for name in ("toolSHA256", "toolSha256", "goSHA256") if name in value]
    require(tool_hashes and all(h == GO_SHA256 for h in tool_hashes) and tool.is_file() and sha(tool) == GO_SHA256, "selected invocation tool mismatch")
    environment = value.get("environment")
    require(isinstance(environment, dict) and all(environment.get(k) == v for k, v in {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0", "GOOS": "windows", "GOARCH": "amd64"}.items()), "selected invocation pinned environment mismatch")
    return args, environment


def selected_v04(root, pointer, product, candidate, source_root):
    report_path = reference(root, pointer)
    report = load(report_path)
    require(report.get("schemaVersion") == 1 and report.get("kind") == "tagged-v04" and report.get("status") == "PASS" and report.get("candidateSha256") == candidate and report.get("sourceBindings") == product, "selected V04 candidate/source/provenance mismatch")
    attempt_path = reference(root, report.get("rootAttempt"))
    attempt = load(attempt_path)
    require(attempt.get("mode") == "root" and type(attempt.get("exit")) is int and attempt["exit"] == 0 and actual_outcome(attempt) == "PASS" and attempt.get("sourceStable") is True and not attempt.get("explicitlyExcluded"), "selected V04 attempt failed")
    call_path = reference(root, report.get("invocation"))
    event_path = reference(root, report.get("events"))
    require(call_path == attempt_path.with_name("invocation.json") and event_path == attempt_path.with_name("execution.jsonl"), "selected V04 invocation/events are not same retained attempt")
    call = load(call_path)
    args, environment = pinned_invocation(call)
    require(args == ["test", "-count=1", "-json", "-timeout=9m", "-tags=nativequalification", "./qualification/native"], "selected V04 command filters/unsupported flags refused")
    outer = parse_events(event_path)
    outer_passes = [e["Test"] for e in outer if e["Action"] == "pass" and e.get("Test")]
    require(set(outer_passes) == {"TestNativeQualification", "TestRejectIncompleteReports", "TestDispatchFailurePropagation"} and len(outer_passes) == 3 and {e["Package"] for e in outer} == {"github.com/localswim/localswim/qualification/native"}, "selected V04 actual root execution missing")
    require(attempt.get("counts") == {"pass": 3, "fail": 0, "skip": 0}, "selected V04 result/event counts mismatch")
    before, after = load(reference(root, report.get("sourceBefore"))), load(reference(root, report.get("sourceAfter")))
    require(before == after and all(isinstance(p, str) and safe_file(source_root, p, h) for p, h in before.items()), "selected V04 source changed")
    require(set(before) == {p.relative_to(source_root).as_posix() for name in ("internal", "cmd", "qualification/native") for p in (source_root / name).rglob("*") if p.is_file()} | {"go.mod", "go.sum"}, "selected V04 source inventory incomplete")
    dispatch = load(reference(root, report.get("dispatch")))
    require(dispatch.get("complete") is True and dispatch.get("sourceStable") is True and dispatch.get("goSHA256") == GO_SHA256, "selected V04 dispatch incomplete")
    inventory = load(source_root / "qualification/native/inventory.json")
    fixture = Path(environment.get("V04_NATIVE_ROOT", "MISSING"))
    require(fixture.is_absolute() and fixture.resolve() == fixture and fixture.is_dir() and len(str(fixture)) <= 70 and not fixture.is_relative_to(source_root) and str(fixture) == call.get("fixture") == attempt.get("fixture"), "selected V04 private fixture context mismatch")
    original = Path(report.get("originalDispatchPath", "MISSING"))
    require(original.is_absolute() and original.resolve() == original and original.is_file() and original.is_relative_to(fixture) and original.name == "results.json" and original.parent.parent == fixture and sha(original) == report.get("originalDispatchSHA256") == sha(reference(root, report.get("dispatch"))), "selected V04 dispatch is not actual retained fixture output")
    require(load(original.with_name("binding.json")) == load(original.with_name("binding-after.json")) == before, "selected V04 actual dispatch source binding mismatch")
    child_refs = report.get("childEvidence")
    require(isinstance(child_refs, dict) and set(child_refs) == {"transaction", "archive", "transactionChild", "archiveChild"}, "selected V04 actual child evidence missing")
    originals = {"transaction": [original.with_name("transaction.jsonl")], "archive": [original.with_name("archive.jsonl")],
                 "transactionChild": list(original.parent.glob("a1/v01-cont-*/logs/native-child.log")),
                 "archiveChild": list(original.parent.glob("n3/*/interruption-child.jsonl"))}
    for name in ("transaction", "archive", "transactionChild", "archiveChild"):
        coverage = dispatch[name]["coverage"] if name in ("transaction", "archive") else dispatch[name]
        required = inventory[name]
        require(coverage.get("required") == required and set(coverage.get("passed", [])) == set(required) and len(coverage["passed"]) == len(required), "selected V04 mandatory coverage missing/duplicate")
        if name in ("transaction", "archive"):
            require(dispatch[name].get("exitOK") is True and dispatch[name].get("reportOK") is True, "selected V04 child execution failed")
        child = reference(root, child_refs[name])
        require(len(originals[name]) == 1 and originals[name][0].is_file() and originals[name][0].resolve().is_relative_to(original.parent) and sha(originals[name][0]) == sha(child), "selected V04 child evidence borrowed or changed")
        if name == "transactionChild":
            body = child.read_text(encoding="utf-8")
            require(not re.search(r"--- (FAIL|SKIP):|^FAIL\b", body, re.MULTILINE) and re.search(r"^PASS\s*$", body, re.MULTILINE), "selected V04 continuation child failed/skipped")
            passed = re.findall(r"--- PASS: (\S+) ", body)
        else:
            passed = [e["Test"] for e in parse_events(child) if e["Action"] == "pass" and e.get("Test")]
        require(all(passed.count(n) == 1 for n in required), "selected V04 actual mandatory child PASS missing/duplicate")
    return report_path


def selected_ordinary(root, pointer, product, candidate, source_root):
    report_path = reference(root, pointer)
    report = load(report_path)
    require(report.get("schemaVersion") == 1 and report.get("kind") == "ordinary-go-all" and report.get("status") == "PASS" and report.get("candidateSha256") == candidate and report.get("sourceBindings") == product, "selected whole suite candidate/source/provenance mismatch")
    archive = reference(root, report.get("archive"))
    archived = {}
    with tarfile.open(archive) as tar:
        for member in tar:
            name = member.name.rstrip("/")
            require(name and not name.startswith("/") and not PureWindowsPath(name).drive and "\\" not in name and all(p not in (".", "..", "") for p in name.split("/")) and (member.isdir() or member.isfile()), "unsafe selected source archive member")
            if member.isfile():
                require(name not in archived, "duplicate selected archive source")
                archived[name] = hashlib.sha256(tar.extractfile(member).read()).hexdigest()
    require(archived and all(archived.get(p) == h for p, h in product.items()), "selected archive differs from complete current product/build inputs")
    before = load(reference(root, report.get("sourceBefore")))
    after = load(reference(root, report.get("sourceAfter")))
    require(before == after == archived, "selected archived source before/after mismatch")
    copy_root = Path(report.get("sourceRoot", "MISSING"))
    require(copy_root.is_absolute() and copy_root.resolve() == copy_root and copy_root.is_dir() and not copy_root.is_relative_to(source_root), "selected source must be retained distinct canonical archive copy")
    require(all(safe_file(copy_root, p, h) for p, h in archived.items()), "selected retained source copy changed")
    require(product_bindings(copy_root) == product, "selected archive copy product set differs from exact current product")
    call_path = reference(root, report.get("invocation"))
    call = load(call_path)
    args, environment = pinned_invocation(call)
    require(type(call.get("exitCode", call.get("exit"))) is int and call.get("exitCode", call.get("exit")) == 0 and not call.get("timedOut"), "selected whole invocation did not exit zero")
    require(call.get("cwd") == str(copy_root), "selected invocation ran outside frozen archive copy")
    require(args[0] == "test" and args[-1] == "./..." and "-count=1" in args and "-json" in args, "selected whole suite requires fresh count1 JSON ./...")
    index = 1
    while index < len(args) - 1:
        arg = args[index]
        if arg == "-p":
            index += 1
            require(index < len(args) - 1 and args[index].isdigit() and int(args[index]) > 0, "selected parallelism invalid")
        else:
            require(arg in ("-count=1", "-json", "-v") or re.fullmatch(r"-timeout=[1-9][0-9]*[smh]", arg) or re.fullmatch(r"-p=[1-9][0-9]*", arg), "selected whole suite filters/overlays/unsupported flags refused")
        index += 1
    event_path = reference(root, report.get("events"))
    require(call.get("logSHA256", call.get("logSha256")) == sha(event_path), "selected actual command/event log mismatch")
    events = [json.loads(line, object_pairs_hook=object_pairs) for line in event_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    require(events and all(isinstance(e, dict) and e.get("Action") in {"start", "run", "pause", "cont", "pass", "output", "bench", "fail", "skip", "build-output", "build-fail"} and isinstance(e.get("Package"), str) and e["Package"] and ("Test" not in e or isinstance(e["Test"], str) and e["Test"]) for e in events), "selected whole suite malformed events")
    require(not any(e.get("Action") in ("fail", "build-fail") or e.get("Action") == "skip" and e.get("Test") for e in events), "selected whole suite failed or skipped tests")
    passes = [e["Test"] for e in events if e.get("Action") == "pass" and e.get("Test")]
    require(passes and len(passes) == len(set((e["Package"], e["Test"]) for e in events if e.get("Action") == "pass" and e.get("Test"))), "selected whole suite zero or duplicate test coverage")
    require(all(sum(e.get("Action") == "run" and e.get("Package") == passed["Package"] and e.get("Test") == passed["Test"] for e in events) == 1 for passed in events if passed.get("Action") == "pass" and passed.get("Test")), "selected whole suite test PASS lacks unique actual run")
    module = re.findall(r"^module\s+(\S+)\s*$", (copy_root / "go.mod").read_text(encoding="utf-8"), re.MULTILINE)
    require(len(module) == 1 and all(e["Package"] == module[0] or e["Package"].startswith(module[0] + "/") for e in events), "selected whole suite event packages outside frozen module")
    for package in {e["Package"] for e in events}:
        require(sum(e.get("Action") == "start" and e["Package"] == package for e in events) == 1, "selected whole package start missing/duplicate")
        terminal = [e for e in events if e["Package"] == package and not e.get("Test") and e.get("Action") in ("pass", "skip")]
        require(len(terminal) == 1, "selected whole package completion missing/duplicate")
        if terminal[0]["Action"] == "skip":
            require(not any(e.get("Test") for e in events if e["Package"] == package) and any("[no test files]" in e.get("Output", "") for e in events if e["Package"] == package), "selected package skip is not an empty-test package")
    prep_path = reference(root, report.get("fixturePreparation"))
    prep = load(prep_path)
    require(prep.get("status") == "PASS_PRIVATE_ROOT_SETUP" and type(prep.get("exit")) is int and prep["exit"] == 0 and prep.get("sourceStable") is True and prep.get("virtualSourceAbsent") is True and prep.get("helperAndModuleBefore") == prep.get("helperAndModuleAfter"), "selected qualified fixture preparation failed")
    require(environment.get("L02_TEST_ROOT") == prep.get("newRoot"), "selected whole suite did not use qualified private fixture parent")
    private_root = PureWindowsPath(prep.get("newRoot", ""))
    require(private_root.is_absolute() and private_root.parent == PureWindowsPath("C:/Temp") and private_root.name.startswith("m09") and len(str(private_root)) <= 70 and "~" not in str(private_root), "selected private fixture root outside bounded m09 scope")
    require(prep.get("sourceRoot") == str(copy_root) and prep.get("goSHA256") == GO_SHA256, "selected private preparation source/tool mismatch")
    for name, h in prep["helperAndModuleBefore"].items():
        require(product.get(name) == h, "selected private helper/module mismatch")
    require(set(prep["helperAndModuleBefore"]) == {"internal/transport/local/files_windows.go", "go.mod", "go.sum"}, "selected private helper input set incomplete")
    require(set(prep.get("files", {})) == {"private-root-setup.go", "overlay.json", "execution.jsonl", "native-result.json"}, "selected private preparation evidence incomplete")
    for name, h in prep["files"].items():
        safe_file(root, (prep_path.parent / name).relative_to(root).as_posix(), h)
    prep_events = parse_events(prep_path.with_name("execution.jsonl"))
    require([e.get("Test") for e in prep_events if e.get("Action") == "pass" and e.get("Test")] == ["TestPreparePrivateL02Root"], "selected private setup did not execute exact qualified helper test")
    native_prep = load(prep_path.with_name("native-result.json"))
    require(native_prep.get("status") == "PASS_PRIVATE_ROOT_SETUP" and native_prep.get("root") == prep.get("newRoot") and native_prep.get("isElevated") is False and native_prep.get("qualifiedPrivateCheck") is True and native_prep.get("empty") is True, "selected fixture native owner/token/emptiness check failed")
    return report_path


def _validate(inventory_path, manifest_path, evidence_root, candidate, source_root):
    evidence_root, source_root = evidence_root.resolve(), source_root.resolve()
    inventory = load(inventory_path)
    manifest = load(manifest_path)
    errors = []
    require(type(inventory.get("schemaVersion")) is int and inventory["schemaVersion"] == 1 and type(manifest.get("schemaVersion")) is int and manifest["schemaVersion"] == 1, "unsupported/missing inventory or manifest schemaVersion")
    required_rows = inventory.get("required")
    require(isinstance(required_rows, list) and required_rows and all(isinstance(r, dict) for r in required_rows), "missing or malformed inventory rows")
    for row in required_rows:
        require(isinstance(row.get("id"), str) and row["id"] and isinstance(row.get("group"), str) and row.get("kind") in {"joined", "browser", "browser-fault", "native", "technical", "regression", "accepted-native-reuse"}, "malformed inventory row")
        string_list(row.get("requirements"), "inventory assertions")
    product = product_bindings(source_root)
    if manifest.get("sourceBindings") != product:
        errors.append("manifest must bind the complete current product and asset set")
    if manifest.get("nativeFreshSuiteStatus") not in ("PASS", "FAIL", "INCOMPLETE"):
        errors.append("missing honest whole-fresh-native-suite status")
    attempts = manifest.get("retainedAttempts")
    require(isinstance(attempts, list) and attempts and all(isinstance(a, dict) for a in attempts), "missing or malformed retained attempt ledger")
    retained_paths = set()
    positive_whole_native = False
    for attempt in attempts:
        target = safe_file(evidence_root, attempt.get("path"), attempt.get("sha256"))
        require(target not in retained_paths, "duplicate retained attempt")
        retained_paths.add(target)
        retained = load_json(target)
        require(attempt.get("status") == actual_outcome(retained), "attempt label disagrees with actual retained result")
        require(attempt.get("scope") in {"joined", "whole-native", "targeted-native", "native-root", "native-focused", "clean-build", "build", "regression"}, "invalid retained attempt scope")
        retained_mode = retained.get("mode") if isinstance(retained, dict) else None
        if retained_mode in ("affected", "affected-available"):
            require(attempt.get("scope") == "whole-native", "whole native attempt scope concealed")
        if attempt.get("scope") == "whole-native":
            require(retained_mode in ("affected", "affected-available") or isinstance(retained, dict) and retained.get("kind") == "ordinary-go-all", "whole native scope requires actual full-suite report")
            positive_whole_native |= actual_outcome(retained) == "PASS" and not retained.get("explicitlyExcluded")
        if attempt.get("scope") == "whole-native" and (actual_outcome(retained) != "PASS" or retained.get("explicitlyExcluded")) and manifest.get("nativeFreshSuiteStatus") != "FAIL" and not manifest.get("selectedCurrentNative"):
            errors.append("failed whole native attempt cannot be relabeled PASS")
    known_attempts = set()
    for pattern in ("joined-*/report.json", "native-*/results.json", "clean-build*/results.json", "clean-build*/report.json"):
        known_attempts.update(p.resolve() for p in evidence_root.glob(pattern) if p.is_file())
    for pattern in ("clean-builds/*.json", "clean-builds/*/*.json"):
        for p in evidence_root.glob(pattern):
            value = load_json(p)
            is_attempt = isinstance(value, dict) and any(k in value for k in ("exit", "exitCode", "error", "failure", "validationError", "commands")) or isinstance(value, list) and p.name.startswith("commands-")
            if is_attempt and actual_outcome(value) == "FAIL":
                known_attempts.add(p.resolve())
    require(known_attempts.issubset(retained_paths), "retained attempt ledger omits known reports")
    if manifest.get("nativeFreshSuiteStatus") == "PASS":
        selected = selected_ordinary(evidence_root, manifest.get("selectedCurrentNative"), product, sha(candidate), source_root)
        require(selected in retained_paths, "selected current whole-native report missing from retained ledger")
        selected_v04(evidence_root, manifest.get("selectedCurrentV04"), product, sha(candidate), source_root)
    else:
        require(not manifest.get("selectedCurrentNative"), "selected current positive suite requires explicit PASS status")
    expected = {row["id"]: row for row in inventory["required"]}
    if len(expected) != len(inventory["required"]):
        errors.append("duplicate frozen requirement")
    if {row["group"] for row in expected.values()} != {f"W{i:02}" for i in range(1, 14)}:
        errors.append("inventory must cover W01-W13")
    if manifest.get("inventorySha256") != sha(inventory_path):
        errors.append("inventory binding mismatch")
    if manifest.get("candidateSha256") != sha(candidate):
        errors.append("candidate binding mismatch")
    freeze = safe_file(evidence_root, manifest.get("freezeFile"), manifest.get("freezeSha256"))
    frozen = load(freeze)
    if frozen.get("inventorySha256") != sha(inventory_path) or timestamp(frozen.get("frozenUtc")) >= timestamp(manifest.get("startedUtc")):
        errors.append("inventory was not frozen before execution")
    seen = set()
    records = manifest.get("records")
    require(isinstance(records, list) and records and all(isinstance(r, dict) for r in records), "missing or malformed evidence records")
    for row in records:
        key = row.get("id")
        if key not in expected or key in seen:
            errors.append("unknown or duplicate evidence record: " + str(key))
            continue
        seen.add(key)
        required = expected[key]
        if row.get("status") != "PASS" or type(row.get("skipped")) is not int or row["skipped"] != 0:
            errors.append("failed or skipped evidence: " + key)
        if row.get("kind") != required["kind"]:
            errors.append("evidence provenance mismatch: " + key)
        checks = string_list(row.get("checks"), "record checks")
        if len(checks) != len(set(checks)) or not set(required["requirements"]).issubset(checks):
            errors.append("missing or duplicate required assertions: " + key)
        if row.get("candidateSha256") != manifest.get("candidateSha256"):
            errors.append("unbound candidate evidence: " + key)
        if row.get("sourceBindings") != product or not row.get("evidence"):
            errors.append("unbound source or evidence: " + key)
        for relative, digest in row.get("sourceBindings", {}).items():
            target = (source_root / relative).resolve()
            if not target.is_relative_to(source_root) or not target.is_file() or sha(target) != digest:
                errors.append("changed or unsafe source binding: " + key)
        evidence = row.get("evidence")
        require(isinstance(evidence, list) and evidence and all(isinstance(e, dict) for e in evidence), "malformed evidence items")
        evidence_paths = []
        for item in evidence:
            target = safe_file(evidence_root, item.get("path"), item.get("sha256"))
            require(item.get("format") in ("checks-json", "metadata-json", "go-test-json", "go-test-verbose"), "unknown evidence format")
            evidence_paths.append(target)
        require(len(evidence_paths) == len(set(evidence_paths)), "duplicate evidence pointer")
        # A pointer alone is insufficient: the parsed evidence must contain each
        # assertion/test result. Supported JSON paths avoid trusting prose claims.
        proved = set()
        parsed = []
        verbose_logs = []
        for item in row.get("evidence", []):
            target = (evidence_root / item.get("path", "MISSING")).resolve()
            if not target.is_relative_to(evidence_root) or not target.is_file():
                continue
            if item.get("format") == "checks-json":
                value = load(target)
                parsed.append((target, value))
                if required["kind"] == "accepted-native-reuse":
                    applicable = value.get("sourceApplicability", {})
                    comparison = applicable.get("comparison", {})
                    tool = applicable.get("toolchain", {})
                    valid = value.get("provenance") == "accepted-native-reuse" and value.get("currentCandidateSha256") == manifest.get("candidateSha256") and value.get("freshPositiveClaim") is False and value.get("currentFixtureUnavailable") is True
                    valid = valid and applicable.get("allRelevantBytesUnchanged") is True and set(comparison) == CASE_INPUTS and all(p in product and data.get("equal") is True and data.get("acceptedCommitSHA256") == data.get("currentSHA256") == product[p] for p, data in comparison.items())
                    valid = valid and Path(tool.get("goExe", "MISSING")).is_file() and sha(Path(tool["goExe"])) == tool.get("goSHA256") == tool.get("qualifiedGoSHA256") == GO_SHA256
                    for path_field, digest_field in (("acceptedRunner", "acceptedRunnerSHA256"), ("acceptedAuthorInvocation", "acceptedAuthorInvocationSHA256"), ("acceptedIndependentInvocation", "acceptedIndependentInvocationSHA256")):
                        path = Path(tool.get(path_field, "MISSING"))
                        valid = valid and path.is_file() and sha(path) == tool.get(digest_field)
                        if valid and path_field != "acceptedRunner":
                            historical = load(path)
                            valid = type(historical.get("exit")) is int and historical["exit"] == 0 and historical.get("tool") == tool.get("goExe") and "-count=1" in string_list(historical.get("args"), "accepted invocation arguments")
                    if not valid:
                        errors.append("unbound or mislabeled accepted native applicability: " + key)
                else:
                    kinds = string_list(value.get("evidenceKinds"), "parsed evidence kinds")
                    if value.get("candidateSha256") != manifest.get("candidateSha256") or value.get("sourceBindings") != product or required["kind"] not in kinds:
                        errors.append("parsed evidence candidate/source/provenance mismatch: " + key)
                    execution = value.get("executionBindings")
                    require(isinstance(execution, dict) and execution, "parsed report lacks proof/tool execution bindings")
                    for name, expected_digest in execution.items():
                        require(isinstance(name, str) and Path(name).is_absolute() and Path(name).is_file() and isinstance(expected_digest, str) and re.fullmatch(r"[0-9a-f]{64}", expected_digest) and sha(Path(name)) == expected_digest, "changed/malformed proof/tool execution binding")
                if value.get("status") == "PASS":
                    proved.update(string_list(value.get("checks"), "parsed checks"))
                else:
                    errors.append("non-passing parsed evidence: " + key)
            elif item.get("format") == "go-test-json":
                events = parse_events(target)
                if not events or any(e.get("Action") in ("fail", "skip") for e in events) or not any(e.get("Action") == "pass" and not e.get("Test") for e in events):
                    errors.append("native execution has failed/skipped or incomplete suite: " + key)
                proved.update(e["Test"] for e in events if e.get("Action") == "pass" and e.get("Test"))
            elif item.get("format") == "metadata-json":
                parsed.append((target, load(target)))
            elif item.get("format") == "go-test-verbose":
                require(required["kind"] == "accepted-native-reuse", "historical verbose evidence cannot substitute for current execution")
                body = target.read_text(encoding="utf-8")
                if re.search(r"--- (FAIL|SKIP):|^FAIL\b", body, re.MULTILINE):
                    errors.append("accepted native log failed or skipped: " + key)
                named = re.findall(r"--- PASS: (\S+) ", body)
                if not all(named.count(n) == 1 for n in required["requirements"]) or not re.search(r"^PASS\s*$", body, re.MULTILINE) or not re.search(r"^ok\s+", body, re.MULTILINE):
                    errors.append("accepted native log lacks required actual PASS: " + key)
                verbose_logs.append(target)
        if required["kind"] == "accepted-native-reuse":
            require(len(verbose_logs) == 2 and len({sha(p) for p in verbose_logs}) == 2, "accepted reuse requires distinct author and independent actual logs")
            proofs = [value for _, value in parsed if value.get("provenance") == "accepted-native-reuse"]
            require(len(proofs) == 1, "accepted reuse requires one applicability proof")
            reports = proofs[0].get("sourceApplicability", {}).get("previousReports", {})
            require(isinstance(reports, dict) and set(reports) == {p.name for p in verbose_logs} and all(sha(p) == reports[p.name].get("sha256") for p in verbose_logs), "accepted logs differ from applicability")
        if required["kind"] in ("native", "technical"):
            streams = [p for p, item in zip(evidence_paths, evidence) if item.get("format") == "go-test-json"]
            require(len(streams) == 1, "native row requires one actual event stream")
            results = [(path, value) for path, value in parsed if "sourceStable" in value and "files" in value]
            if len(results) != 1:
                errors.append("native event stream lacks exactly one bound result: " + key)
            else:
                path, value = results[0]
                if value.get("status") != "PASS" or type(value.get("exit")) is not int or value.get("exit") != 0 or value.get("error") is not None or value.get("sourceStable") is not True or value.get("candidateStable") is not True or value.get("failuresOrSkips") != [] or value.get("candidateSha256") != manifest.get("candidateSha256") or only_product(value.get("sourceBindings", {}), product) != product:
                    errors.append("native parsed execution metadata failed or unbound: " + key)
                require(set(value.get("files", {})) == NATIVE_FILES, "native result lacks complete invocation/events/source/script hashes")
                require(streams[0] == path.with_name("execution.jsonl"), "parsed Go event stream differs from bound execution")
                for name, digest in value.get("files", {}).items():
                    safe_file(evidence_root, (path.parent / name).relative_to(evidence_root).as_posix(), digest)
                for name in ("source-before.json", "source-after.json"):
                    file = path.with_name(name)
                    if not file.is_file() or only_product(load(file), product) != product:
                        errors.append("native full source correspondence mismatch: " + key)
                invocation = path.with_name("invocation.json")
                if not invocation.is_file():
                    errors.append("native invocation missing: " + key)
                else:
                    call = load(invocation)
                    require(call.get("scriptSHA256") == value["files"]["native_targeted.py"], "native copied script differs from invocation")
                    invoked_candidate = Path(call.get("candidate", "MISSING"))
                    require(invoked_candidate.is_file() and sha(invoked_candidate) == manifest.get("candidateSha256"), "native actual candidate file mismatch")
                    args = string_list(call.get("args"), "native invocation arguments")
                    all_native = {n for r in inventory["required"] if r["kind"] in ("native", "technical") for n in r["requirements"]}
                    exact_run = "-run=^(" + "|".join(sorted(all_native)) + ")$"
                    require(args[:2] == ["test", "-count=1"] and "-json" in args and exact_run in args and not any(a.startswith("-skip") for a in args), "native invocation does not select exact frozen requirements")
                    packages = {"./internal/transport/local", "./internal/workspace"}
                    require({a for a in args if a.startswith("./")} == packages, "native invocation package scope mismatch")
                    actual_events = parse_events(streams[0])
                    require({e["Package"] for e in actual_events} == {"github.com/localswim/localswim/" + p[2:] for p in packages}, "native event packages differ from invocation scope")
                    actual_passes = [e["Test"] for e in actual_events if e["Action"] == "pass" and e.get("Test")]
                    require(all_native.issubset(actual_passes) and value.get("requiredUniqueTests") == len(all_native) and value.get("passRecords") == len(actual_passes), "native counts/requirements differ from actual stream")
                    if call.get("candidateSha256") != manifest.get("candidateSha256") or not Path(call.get("tool", "MISSING")).is_file() or sha(Path(call["tool"])) != call.get("toolSHA256") or call.get("toolSHA256") != GO_SHA256:
                        errors.append("native invocation candidate/tool mismatch: " + key)
                    environment = call.get("environment")
                    require(isinstance(environment, dict) and all(environment.get(k) == v for k, v in {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0", "GOOS": "windows", "GOARCH": "amd64"}.items()), "native offline pinned environment mismatch")
        if not set(required["requirements"]).issubset(proved):
            errors.append("assertions not present in passed executable evidence: " + key)
        if not set(checks).issubset(proved):
            errors.append("declared supplemental checks lack passed executable evidence: " + key)
    for key in sorted(set(expected) - seen):
        errors.append("missing required evidence: " + key)
    return errors


def validate(inventory_path, manifest_path, evidence_root, candidate, source_root):
    """Every malformed input returns structured failure, never a partial PASS."""
    try:
        return _validate(Path(inventory_path), Path(manifest_path), Path(evidence_root), Path(candidate), Path(source_root))
    except (OSError, ValueError, KeyError, TypeError, AttributeError, IndexError, RecursionError, OverflowError) as error:
        return ["malformed or inconsistent qualification evidence: " + str(error)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--evidence-root", type=Path, required=True)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--source-root", type=Path, required=True)
    args = parser.parse_args()
    try:
        errors = validate(Path(__file__).with_name("inventory.json"), args.manifest.resolve(), args.evidence_root.resolve(), args.candidate.resolve(), args.source_root.resolve())
    except (OSError, ValueError, KeyError, TypeError) as error:
        errors = ["malformed or unreadable qualification input: " + type(error).__name__]
    print(json.dumps({"status": "FAIL" if errors else "PASS_SCOPED_INVENTORY", "errors": errors, "scope": "M01.09 inventory evidence completeness only; whole fresh native status remains separate and M01.10 is not approved"}, indent=2))
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
