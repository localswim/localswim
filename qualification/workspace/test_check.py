"""Controlled consistency/negative tests. These artifacts are not qualification."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import tarfile
import shutil
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("workspace_checker", Path(__file__).with_name("check.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class CheckerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name).resolve()
        self.source = self.base / "source"
        self.root = self.base / "evidence"
        self.root.mkdir()
        for name in ("internal", "cmd", "schemas", "web/src", "web/generated", "web/styles", "web/scripts"):
            (self.source / name).mkdir(parents=True, exist_ok=True)
            (self.source / name / "input.txt").write_text("synthetic source\n")
        for name in ("go.mod", "go.sum", "web/package-lock.json", "web/index.html", "web/package.json", "web/tsconfig.json"):
            (self.source / name).write_text("synthetic input\n")
        self.candidate = self.base / "synthetic-candidate.exe"
        self.candidate.write_bytes(b"synthetic candidate; never executed")
        self.tool = self.base / "synthetic-tool"
        self.tool.write_bytes(b"synthetic tool; never executed")
        self.inventory = {"schemaVersion": 1, "required": [{"id": f"W{i:02}-test", "group": f"W{i:02}", "kind": "joined", "requirements": [f"check-{i}"]} for i in range(1, 14)]}
        self.inv = self.root / "inventory.json"
        self.write(self.inv, self.inventory)
        self.product = checker.product_bindings(self.source)
        self.report = {"status": "PASS", "candidateSha256": checker.sha(self.candidate), "sourceBindings": self.product,
                       "evidenceKinds": ["joined"], "checks": [f"check-{i}" for i in range(1, 14)],
                       "executionBindings": {str(self.tool): checker.sha(self.tool)}}
        self.report_path = self.root / "checks.json"
        self.write(self.report_path, self.report)
        self.freeze = self.root / "freeze.json"
        self.write(self.freeze, {"inventorySha256": checker.sha(self.inv), "frozenUtc": "2026-01-01T00:00:00Z"})
        attempt = self.root / "failed-attempt.json"
        self.write(attempt, {"status": "FAIL", "failure": "retained artificial failure"})
        self.manifest = {"schemaVersion": 1, "sourceBindings": self.product, "inventorySha256": checker.sha(self.inv), "candidateSha256": checker.sha(self.candidate),
                         "freezeFile": "freeze.json", "freezeSha256": checker.sha(self.freeze), "startedUtc": "2026-01-01T00:01:00Z", "nativeFreshSuiteStatus": "INCOMPLETE",
                         "retainedAttempts": [{"path": attempt.name, "sha256": checker.sha(attempt), "scope": "joined", "status": "FAIL"}],
                         "records": [{"id": r["id"], "kind": r["kind"], "status": "PASS", "skipped": 0, "checks": r["requirements"],
                                      "candidateSha256": checker.sha(self.candidate), "sourceBindings": self.product,
                                      "evidence": [{"path": self.report_path.name, "sha256": checker.sha(self.report_path), "format": "checks-json"}]} for r in self.inventory["required"]]}
        self.path = self.root / "manifest.json"

    def write(self, path, value):
        path.write_text(json.dumps(value), encoding="utf-8")

    def errors(self):
        self.write(self.path, self.manifest)
        return checker.validate(self.inv, self.path, self.root, self.candidate, self.source)

    def update_report(self):
        self.write(self.report_path, self.report)
        for row in self.manifest["records"]:
            if row["evidence"][0]["path"] == self.report_path.name:
                row["evidence"][0]["sha256"] = checker.sha(self.report_path)

    def test_controlled_complete_metadata_passes(self):
        self.assertEqual(self.errors(), [])

    def test_wrong_parsed_candidate_labels_do_not_pass(self):
        self.report["candidateSha256"] = "0" * 64
        self.update_report()
        self.assertTrue(self.errors())

    def test_declared_supplemental_check_requires_actual_evidence(self):
        self.manifest["records"][0]["checks"].append("unproved-supplemental-check")
        self.assertTrue(self.errors())

    def test_missing_parsed_source_or_kind_does_not_pass(self):
        for field, value in (("sourceBindings", {}), ("evidenceKinds", []), ("executionBindings", {})):
            with self.subTest(field=field):
                original = self.report[field]
                self.report[field] = value
                self.update_report()
                self.assertTrue(self.errors())
                self.report[field] = original

    def test_source_set_is_exact_including_build_inputs(self):
        self.manifest["sourceBindings"] = {"go.mod": self.product["go.mod"]}
        self.assertTrue(self.errors())
        self.manifest["sourceBindings"] = self.product
        (self.source / "web/index.html").write_text("changed build input")
        self.assertTrue(self.errors())

    def test_changed_execution_script_is_rejected(self):
        self.tool.write_bytes(b"changed script")
        self.assertTrue(self.errors())

    def test_unsafe_freeze_and_evidence_paths_are_rejected(self):
        outside = self.base / "outside.json"
        outside.write_bytes(self.freeze.read_bytes())
        for relative in ("../outside.json", str(outside), "C:/outside.json", "a/../freeze.json", "a\\freeze.json"):
            with self.subTest(path=relative):
                self.manifest["freezeFile"] = relative
                self.assertTrue(self.errors())

    def test_missing_evidence_digest_rejected(self):
        del self.manifest["records"][0]["evidence"][0]["sha256"]
        self.assertTrue(self.errors())

    def test_duplicate_records_and_pointers_rejected(self):
        self.manifest["records"].append(copy.deepcopy(self.manifest["records"][0]))
        self.assertTrue(self.errors())
        self.manifest["records"].pop()
        self.manifest["records"][0]["evidence"] *= 2
        self.assertTrue(self.errors())

    def test_empty_inventory_rejected(self):
        self.write(self.inv, {"required": []})
        self.assertTrue(self.errors())

    def test_unsupported_schema_rejected(self):
        for value in (None, True, 0, 2, "1"):
            self.manifest["schemaVersion"] = value
            self.assertTrue(self.errors())

    def test_malformed_shapes_fail_without_throwing(self):
        original = copy.deepcopy(self.manifest)
        mutations = [("records", None), ("records", ["bad"]), ("sourceBindings", []), ("retainedAttempts", "bad")]
        for field, value in mutations:
            with self.subTest(field=field, value=value):
                self.manifest = copy.deepcopy(original)
                self.manifest[field] = value
                self.assertTrue(self.errors())
        for value in ([], "bad", None, 42):
            self.write(self.path, value)
            self.assertTrue(checker.validate(self.inv, self.path, self.root, self.candidate, self.source))

    def test_nested_malformed_shapes_fail_closed(self):
        for field, value in (("checks", "bad"), ("checks", [{}]), ("evidence", "bad"), ("evidence", [None]), ("sourceBindings", "bad")):
            original = self.manifest["records"][0].get(field)
            self.manifest["records"][0][field] = value
            self.assertTrue(self.errors())
            self.manifest["records"][0][field] = original

    def test_duplicate_json_fields_rejected(self):
        self.path.write_text('{"records":[],"records":[]}')
        self.assertTrue(checker.validate(self.inv, self.path, self.root, self.candidate, self.source))

    def test_failed_attempt_label_cannot_launder_failure(self):
        self.manifest["retainedAttempts"][0]["status"] = "PASS"
        self.assertTrue(self.errors())

    def test_stopped_assertion_without_message_is_retained_failure(self):
        self.assertEqual(checker.actual_outcome({"status": "STOPPED", "errorType": "AssertionError", "error": ""}), "FAIL")

    def test_known_failed_attempt_cannot_be_omitted(self):
        p = self.root / "joined-failure" / "report.json"
        p.parent.mkdir()
        self.write(p, {"status": "FAIL", "failure": "known artificial failure"})
        self.assertTrue(self.errors())

    def test_failed_clean_build_must_be_retained(self):
        p = self.root / "clean-builds" / "setup-failure.json"
        p.parent.mkdir()
        self.write(p, {"error": "artificial setup failure"})
        self.assertTrue(self.errors())

    def test_clean_command_array_failure_and_timeout_are_retained(self):
        p = self.root / "clean-builds" / "commands-controlled.json"
        p.parent.mkdir()
        self.write(p, [{"build": "synthetic", "command": {"exitCode": 1}}])
        self.assertTrue(self.errors())
        self.assertEqual(checker.actual_outcome({"exitCode": None, "timedOut": True}), "FAIL")
        self.manifest["retainedAttempts"].append({"path": p.relative_to(self.root).as_posix(), "sha256": checker.sha(p), "scope": "clean-build", "status": "FAIL"})
        self.assertEqual(self.errors(), [])

    def test_whole_failed_native_cannot_claim_pass(self):
        p = self.root / "failed-attempt.json"
        self.write(p, {"mode": "affected", "exit": 1, "counts": {"pass": 3, "fail": 1, "skip": 0}})
        self.manifest["retainedAttempts"][0].update(sha256=checker.sha(p), scope="whole-native")
        self.manifest["nativeFreshSuiteStatus"] = "PASS"
        self.assertTrue(self.errors())

    def native_fixture(self):
        self.inventory["required"][0].update(kind="native", requirements=["TestSynthetic"])
        self.write(self.inv, self.inventory)
        self.manifest["inventorySha256"] = checker.sha(self.inv)
        self.write(self.freeze, {"inventorySha256": checker.sha(self.inv), "frozenUtc": "2026-01-01T00:00:00Z"})
        self.manifest["freezeSha256"] = checker.sha(self.freeze)
        run = self.root / "controlled-native"
        run.mkdir()
        self.native_events = [{"Action": "run", "Package": "github.com/localswim/localswim/internal/transport/local", "Test": "TestSynthetic"},
                              {"Action": "pass", "Package": "github.com/localswim/localswim/internal/transport/local", "Test": "TestSynthetic"},
                              {"Action": "pass", "Package": "github.com/localswim/localswim/internal/transport/local"},
                              {"Action": "pass", "Package": "github.com/localswim/localswim/internal/workspace"}]
        self.event_path = run / "execution.jsonl"
        self.event_path.write_text("\n".join(json.dumps(e) for e in self.native_events))
        for name in ("source-before.json", "source-after.json"):
            self.write(run / name, self.product)
        (run / "native_targeted.py").write_text("# controlled fixture only\n")
        env = {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0", "GOOS": "windows", "GOARCH": "amd64"}
        self.write(run / "invocation.json", {"candidate": str(self.candidate), "candidateSha256": checker.sha(self.candidate), "scriptSHA256": checker.sha(run / "native_targeted.py"), "tool": str(self.tool), "toolSHA256": checker.sha(self.tool), "environment": env,
                                           "args": ["test", "-count=1", "-json", "-run=^(TestSynthetic)$", "./internal/transport/local", "./internal/workspace"]})
        self.native_result = {"status": "PASS", "exit": 0, "error": None, "sourceStable": True, "candidateStable": True, "failuresOrSkips": [],
                              "candidateSha256": checker.sha(self.candidate), "sourceBindings": self.product, "requiredUniqueTests": 1, "passRecords": 1,
                              "files": {name: checker.sha(run / name) for name in checker.NATIVE_FILES}}
        self.native_path = run / "results.json"
        self.write(self.native_path, self.native_result)
        row = self.manifest["records"][0]
        row.update(kind="native", checks=["TestSynthetic"], evidence=[{"path": self.event_path.relative_to(self.root).as_posix(), "sha256": checker.sha(self.event_path), "format": "go-test-json"},
                   {"path": self.native_path.relative_to(self.root).as_posix(), "sha256": checker.sha(self.native_path), "format": "metadata-json"}])

    def update_native(self):
        self.native_result["files"]["execution.jsonl"] = checker.sha(self.event_path)
        self.write(self.native_path, self.native_result)
        row = self.manifest["records"][0]
        row["evidence"][0]["sha256"] = checker.sha(self.event_path)
        row["evidence"][1]["sha256"] = checker.sha(self.native_path)

    def test_controlled_native_metadata_consistency_passes(self):
        self.native_fixture()
        with patch.object(checker, "GO_SHA256", checker.sha(self.tool)):
            self.assertEqual(self.errors(), [])

    def test_failed_package_cannot_launder_individual_pass(self):
        self.native_fixture()
        self.native_events.append({"Action": "fail", "Package": "synthetic/package"})
        self.event_path.write_text("\n".join(json.dumps(e) for e in self.native_events))
        self.update_native()
        with patch.object(checker, "GO_SHA256", checker.sha(self.tool)):
            self.assertTrue(self.errors())

    def test_native_empty_file_inventory_rejected(self):
        self.native_fixture()
        self.native_result["files"] = {}
        self.write(self.native_path, self.native_result)
        self.manifest["records"][0]["evidence"][1]["sha256"] = checker.sha(self.native_path)
        self.assertTrue(self.errors())

    def test_different_native_stream_cannot_replace_bound_stream(self):
        self.native_fixture()
        other = self.root / "other.jsonl"
        other.write_bytes(self.event_path.read_bytes())
        self.manifest["records"][0]["evidence"][0].update(path=other.name, sha256=checker.sha(other))
        self.assertTrue(self.errors())

    def test_native_skip_duplicate_and_incomplete_stream_rejected(self):
        self.native_fixture()
        for events in ([*self.native_events, {"Action": "skip", "Package": "synthetic/package", "Test": "TestOther"}],
                       [*self.native_events, self.native_events[1]], self.native_events[:-1]):
            with self.subTest(events=events):
                self.event_path.write_text("\n".join(json.dumps(e) for e in events))
                self.update_native()
                with patch.object(checker, "GO_SHA256", checker.sha(self.tool)):
                    self.assertTrue(self.errors())

    def test_labels_cannot_replace_accepted_historical_logs(self):
        self.inventory["required"][0]["kind"] = "accepted-native-reuse"
        self.write(self.inv, self.inventory)
        self.manifest["inventorySha256"] = checker.sha(self.inv)
        self.write(self.freeze, {"inventorySha256": checker.sha(self.inv), "frozenUtc": "2026-01-01T00:00:00Z"})
        self.manifest["freezeSha256"] = checker.sha(self.freeze)
        self.manifest["records"][0]["kind"] = "accepted-native-reuse"
        self.assertTrue(self.errors())

    def selected_fixture(self):
        (self.source / "go.mod").write_text("module synthetic\n")
        helper = self.source / "internal/transport/local/files_windows.go"
        helper.parent.mkdir(parents=True)
        helper.write_text("controlled qualified helper input")
        product = checker.product_bindings(self.source)
        copy_root = self.base / "archive-copy"
        shutil.copytree(self.source, copy_root)
        folder = self.root / "selected"
        folder.mkdir()
        archive = folder / "source.tar"
        with tarfile.open(archive, "w") as tar:
            for name in product:
                tar.add(self.source / name, arcname=name, recursive=False)
        before, after = folder / "before.json", folder / "after.json"
        self.write(before, product)
        self.write(after, product)
        event_path = folder / "events.jsonl"
        event_path.write_text("\n".join(json.dumps(e) for e in [
            {"Action": "start", "Package": "synthetic/package"},
            {"Action": "run", "Package": "synthetic/package", "Test": "TestSynthetic"},
            {"Action": "pass", "Package": "synthetic/package", "Test": "TestSynthetic"},
            {"Action": "pass", "Package": "synthetic/package"}]))
        prep_folder = folder / "preparation"
        prep_folder.mkdir()
        private = "C:\\Temp\\m09-controlled-test"
        self.write(prep_folder / "native-result.json", {"status": "PASS_PRIVATE_ROOT_SETUP", "root": private, "isElevated": False, "qualifiedPrivateCheck": True, "empty": True})
        (prep_folder / "private-root-setup.go").write_text("controlled setup proof")
        self.write(prep_folder / "overlay.json", {})
        (prep_folder / "execution.jsonl").write_text("\n".join(json.dumps(e) for e in [
            {"Action": "pass", "Package": "synthetic/setup", "Test": "TestPreparePrivateL02Root"},
            {"Action": "pass", "Package": "synthetic/setup"}]))
        inputs = {name: product[name] for name in ("internal/transport/local/files_windows.go", "go.mod", "go.sum")}
        prep = {"status": "PASS_PRIVATE_ROOT_SETUP", "exit": 0, "sourceStable": True, "virtualSourceAbsent": True,
                "helperAndModuleBefore": inputs, "helperAndModuleAfter": inputs, "newRoot": private, "sourceRoot": str(copy_root), "goSHA256": checker.sha(self.tool),
                "files": {p.name: checker.sha(p) for p in prep_folder.iterdir()}}
        self.write(prep_folder / "result.json", prep)
        environment = {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0", "GOOS": "windows", "GOARCH": "amd64", "L02_TEST_ROOT": private}
        invocation = {"argv": [str(self.tool), "test", "-p", "1", "-count=1", "-json", "-timeout=60s", "./..."], "cwd": str(copy_root), "exitCode": 0,
                      "toolSHA256": checker.sha(self.tool), "environment": environment, "logSha256": checker.sha(event_path)}
        self.write(folder / "invocation.json", invocation)
        def ref(p):
            return {"path": p.relative_to(self.root).as_posix(), "sha256": checker.sha(p)}
        report = {"schemaVersion": 1, "kind": "ordinary-go-all", "status": "PASS", "candidateSha256": checker.sha(self.candidate), "sourceBindings": product,
                  "sourceRoot": str(copy_root), "archive": ref(archive), "sourceBefore": ref(before), "sourceAfter": ref(after), "events": ref(event_path),
                  "invocation": ref(folder / "invocation.json"), "fixturePreparation": ref(prep_folder / "result.json")}
        path = folder / "report.json"
        self.write(path, report)
        return product, report, path, invocation, folder

    def selected_errors(self, product, report, path):
        self.write(path, report)
        pointer = {"path": path.relative_to(self.root).as_posix(), "sha256": checker.sha(path)}
        try:
            with patch.object(checker, "GO_SHA256", checker.sha(self.tool)):
                checker.selected_ordinary(self.root, pointer, product, checker.sha(self.candidate), self.source)
            return []
        except (ValueError, OSError, KeyError, TypeError) as error:
            return [str(error)]

    def test_selected_current_whole_suite_metadata_consistency(self):
        product, report, path, _, _ = self.selected_fixture()
        self.assertEqual(self.selected_errors(product, report, path), [])

    def test_selected_actual_camelcase_tool_hash_and_conflict(self):
        product, report, path, call, folder = self.selected_fixture()
        call["toolSha256"] = call.pop("toolSHA256")
        self.write(folder / "invocation.json", call)
        report["invocation"]["sha256"] = checker.sha(folder / "invocation.json")
        self.assertEqual(self.selected_errors(product, report, path), [])
        call["toolSHA256"] = "0" * 64
        self.write(folder / "invocation.json", call)
        report["invocation"]["sha256"] = checker.sha(folder / "invocation.json")
        self.assertTrue(self.selected_errors(product, report, path))

    def test_selected_whole_suite_rejects_wrong_candidate_or_source(self):
        product, report, path, _, _ = self.selected_fixture()
        report["candidateSha256"] = "0" * 64
        self.assertTrue(self.selected_errors(product, report, path))
        report["candidateSha256"] = checker.sha(self.candidate)
        report["sourceBindings"] = {}
        self.assertTrue(self.selected_errors(product, report, path))

    def test_selected_whole_suite_refuses_filtered_command(self):
        product, report, path, call, folder = self.selected_fixture()
        for flag in ("-run=TestSynthetic", "-skip=TestOther", "-short", "-overlay=private.json"):
            call["argv"].insert(-1, flag)
            self.write(folder / "invocation.json", call)
            report["invocation"]["sha256"] = checker.sha(folder / "invocation.json")
            self.assertTrue(self.selected_errors(product, report, path))
            call["argv"].remove(flag)

    def test_selected_whole_suite_refuses_failed_or_test_skipped_stream(self):
        product, report, path, call, folder = self.selected_fixture()
        original = (folder / "events.jsonl").read_text()
        for action in ("fail", "skip"):
            (folder / "events.jsonl").write_text(original + "\n" + json.dumps({"Action": action, "Package": "synthetic/package", "Test": "TestOther"}))
            report["events"]["sha256"] = call["logSha256"] = checker.sha(folder / "events.jsonl")
            self.write(folder / "invocation.json", call)
            report["invocation"]["sha256"] = checker.sha(folder / "invocation.json")
            self.assertTrue(self.selected_errors(product, report, path))

    def test_selected_whole_suite_archive_and_after_source_are_exact(self):
        product, report, path, _, folder = self.selected_fixture()
        self.write(folder / "after.json", {"go.mod": product["go.mod"]})
        report["sourceAfter"]["sha256"] = checker.sha(folder / "after.json")
        self.assertTrue(self.selected_errors(product, report, path))

    def test_selected_whole_suite_requires_qualified_private_parent(self):
        product, report, path, _, folder = self.selected_fixture()
        prep_path = folder / "preparation/result.json"
        prep = checker.load(prep_path)
        prep["files"] = {}
        self.write(prep_path, prep)
        report["fixturePreparation"]["sha256"] = checker.sha(prep_path)
        self.assertTrue(self.selected_errors(product, report, path))

    def test_selected_archive_copy_refuses_extra_product_source(self):
        product, report, path, _, _ = self.selected_fixture()
        (Path(report["sourceRoot"]) / "internal/extra.go").write_text("package extra\n")
        self.assertTrue(self.selected_errors(product, report, path))

    def test_selected_v04_refuses_filters_and_missing_actual_root(self):
        folder = self.root / "root-controlled"
        folder.mkdir()
        args = ["test", "-count=1", "-json", "-timeout=9m", "-tags=nativequalification", "./qualification/native"]
        environment = {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0", "GOOS": "windows", "GOARCH": "amd64"}
        self.write(folder / "results.json", {"mode": "root", "exit": 0, "sourceStable": True, "counts": {"pass": 3, "fail": 0, "skip": 0}})
        # Controlled parser-only stream is deliberately not native qualification.
        (folder / "execution.jsonl").write_text("\n".join(json.dumps(e) for e in [
            {"Action": "pass", "Package": "github.com/localswim/localswim/qualification/native", "Test": "TestRejectIncompleteReports"},
            {"Action": "pass", "Package": "github.com/localswim/localswim/qualification/native"}]))
        def ref(path):
            return {"path": path.relative_to(self.root).as_posix(), "sha256": checker.sha(path)}
        report = {"schemaVersion": 1, "kind": "tagged-v04", "status": "PASS", "candidateSha256": checker.sha(self.candidate), "sourceBindings": self.product,
                  "rootAttempt": ref(folder / "results.json"), "events": ref(folder / "execution.jsonl")}
        for flag in ("-run=TestRejectIncompleteReports", "-skip=TestNativeQualification", "-short", "-overlay=fake.json", None):
            command = args.copy()
            if flag:
                command.insert(-1, flag)
            self.write(folder / "invocation.json", {"tool": str(self.tool), "goSHA256": checker.sha(self.tool), "args": command, "environment": environment})
            report["invocation"] = ref(folder / "invocation.json")
            path = folder / "selection.json"
            self.write(path, report)
            with self.subTest(flag=flag), patch.object(checker, "GO_SHA256", checker.sha(self.tool)):
                with self.assertRaisesRegex(ValueError, "command filters" if flag else "actual root execution missing"):
                    checker.selected_v04(self.root, ref(path), self.product, checker.sha(self.candidate), self.source)


if __name__ == "__main__":
    unittest.main()
