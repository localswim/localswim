V02 pinned browser qualification - incomplete

This source stages actual Windows browser/session/private-recovery qualification.
It currently establishes no V02 pass. The exact test issuer and synthetic human
grant were enrolled through owner actions. Fresh native terminal delegation is
still required before the full session/recovery run. Real data and personal profiles
are excluded. No certificate-warning bypass or mock-only qualification is allowed.

The exact staged command is:
  npm --prefix web run qualification:browser

P02 loads run.mjs as the pinned Playwright configuration. A canonical private
V02_RUN_MANIFEST must bind the prepared fixture, issuer/origin/identities, pinned
browser executable and private profile/ACL record. Missing inputs, zero tests,
skipped mandatory cases or execution failure fail. inventory.json enumerates the
required case IDs. Repeated tests do not turn an incomplete matrix into a pass.

Qualification-only native fixture code lives in native/. Its exact issuer,
rights and terminal-action packet must be reviewed before the owner enrolls trust
or creates a human grant. Actual terminal confirmation is not replaced by a test
callback or --yes. Trust removal remains an explicit later owner action.

The product's default entry remains unqualified. A trusted test composition may
supply a profile-bound proof only after actual strict-durability transaction
completion and independent readback in the pinned profile/origin. Positive proof
does not follow from feature detection or a manifest boolean. All fault injection,
CDP interaction, native ACL checks and physical durability limits must be labeled.
No physical power-loss, hostile same-account or meaningful-human-review proof is
claimed. V04 owns integrated root dispatch and later qualification reconciliation.

Preparation checkpoint
- All 23 mandatory cases collect with the frozen runner; collection is not execution.
- Independent static reviews cover the browser harness and native fixture. The
  fixture requires a real console and an exact, fully displayed delegation line.
- A read-only preflight used the actual pinned browser, private profile and product
  bootstrap. Trusted TLS 1.3, strict IndexedDB transaction completion followed by
  independent identical readback, and a native QuotaExceededError were observed.
  This preflight performed no login, code exchange or board mutation.
- The first quota probe failed and remains preserved. A separately recorded probe
  confirmed the CDP quota override, waited for the browser cache interval and used
  incompressible data. The later observation does not establish a unique cause for
  the earlier failure. The suite labels CDP fault injection explicitly.
- Native profile auditing accepts only the reviewed, pinned browser's exact named
  network-sandbox capability and restrictive execute-deny forms in narrow child
  scopes. The profile root and other native private-file predicates remain strict.
  No ACL repair was used to make the audit pass.
- The earlier generic qualified-startup refusal has no proven historical cause.
  Fixed stage categories improve future diagnosis without recording input, codes,
  bearer values, file paths or raw errors.

Remaining acceptance
1. The owner starts the reviewed same-identity fixture in a fresh host console and
   personally enters its unchanged native delegation challenge.
2. Execute the exact staged command with all 23 mandatory cases, no skipped cases,
   no retries, successful teardown and private profile/log/URL audits.
3. Review fresh executed evidence independently, then stop the owned fixture and
   complete the exact owner-confirmed test-issuer removal.
results.json records this incomplete checkpoint; it is not a positive run verdict.
