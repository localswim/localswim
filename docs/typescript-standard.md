# TypeScript quality standard

Terry approved this standard for localswim V2. Rules must catch plausible defects,
protect a contract, or remove recurring mechanical work. Evaluate diagnostic value
and execution cost when changing the rule set.

## Tools and coverage

- Use the pinned TypeScript compiler for type checking and JavaScript emission.
- Use Oxlint with `oxlint-tsgolint` for syntax and type-aware linting. Pin qualified
  versions in `web/package.json` and the lockfile, including Windows support.
- Use Prettier for authored TypeScript, JavaScript, MJS, CSS, HTML and web tooling
  configuration. Commit one shared configuration using two spaces, double quotes,
  LF and a 100-column print target, with the pinned formatter's other defaults.
  Provide a format command that applies it and a format-check command that fails
  the project gate when authored files differ from that output.
- Lint authored application code, tests and build scripts. Type-check production
  TypeScript and checked JavaScript. Give Node tooling its correct environment.
  Existing dynamic JavaScript test harnesses receive syntax linting; expand typed
  coverage when it provides useful checks without distorting their fixtures.
- Check emitted browser assets against fresh release-build output, including
  the optimization in the [UI release standard](ui-standards.md). Generated files
  are verified through that comparison and excluded from linting and formatting.

TypeScript 7.0 supports the chosen approach. ESLint core and `typescript-eslint`
have separate compatibility requirements. Qualify upgrades against the actual
mixed TypeScript and JSDoc JavaScript sources. [Oxlint typed linting](https://oxc.rs/docs/guide/usage/linter/type-aware.html)

## Compiler checks

Enable these options in the applicable production configuration.

| Option | Value | Purpose |
| --- | --- | --- |
| `strict` | `true` | Strict typing, null handling and implicit-any checks |
| `allowJs`, `checkJs` | `true` | Check the shared JavaScript modules |
| `noFallthroughCasesInSwitch` | `true` | Catch accidental switch fallthrough |
| `noImplicitReturns` | `true` | Catch incomplete return paths |
| `noEmitOnError` | `true` | Keep invalid source out of generated assets |
| `noUncheckedIndexedAccess` | `true` | Account for missing array and dictionary entries |
| `noUnusedLocals`, `noUnusedParameters` | `true` | Catch stale variables and arguments |
| `noImplicitOverride` | `true` | Make overrides deliberate |
| `allowUnreachableCode`, `allowUnusedLabels` | `false` | Catch unreachable statements and accidental labels |

An intentionally unused parameter may use an underscore prefix. Review indexed
access findings individually. Prefer a guard, iteration or a more precise type;
use a narrow assertion only when an established invariant proves it safe.

Keep `exactOptionalPropertyTypes` disabled for this standard. The qualification
found substantial optional-request forwarding churn; reconsider it when a
specific contract benefits from distinguishing absence from explicit `undefined`.

## Lint rules

Use a committed, explicit rule selection. Each enabled rule is an error. Review
new defaults and rules during dependency upgrades before adding them to the set.

| Required checks | Purpose |
| --- | --- |
| `typescript/no-floating-promises`, `typescript/no-misused-promises`, `typescript/await-thenable` | Catch missing awaits, unhandled operations and async callback mistakes |
| `typescript/no-unsafe-assignment`, `typescript/no-unsafe-argument`, `typescript/no-unsafe-call`, `typescript/no-unsafe-member-access`, `typescript/no-unsafe-return` | Prevent unchecked `any` from bypassing validation |
| `typescript/switch-exhaustiveness-check` | Cover every member of a finite state union |
| `no-async-promise-executor`, `no-promise-executor-return`, `array-callback-return` | Catch executor and callback control-flow mistakes |
| `no-dupe-args`, `no-dupe-keys`, `no-dupe-else-if`, `no-duplicate-case` | Catch duplicated definitions and branches |
| `no-self-assign`, `no-self-compare`, `use-isnan`, `valid-typeof`, `no-unsafe-finally`, `no-unreachable` | Catch invalid comparisons and ineffective or unsafe control flow |
| `no-debugger`, `no-eval` | Catch debugging leftovers and dynamic code execution |

Use `unknown` for untrusted JSON and narrow through the existing validators.
Deliberately detached promises need internal rejection handling or an explicit
handler at the call site. A `void` expression records intent; review must also
establish the rejection path. [Promise rule behavior](https://oxc.rs/docs/guide/usage/linter/rules/typescript/no-floating-promises.html)

## Exceptions and verification

- Preserve useful inference. Add annotations where they clarify boundaries.
- Require a concrete demonstrated benefit before adding naming, documentation,
  complexity, function-length or other stylistic restrictions.
- Explain necessary suppressions beside the smallest affected expression or
  statement. Check for obsolete disable directives. Resolve findings through
  real invariants and error handling rather than blanket exclusions or empty catches.
- Run type checking, linting, formatting checks, generated-asset verification and
  the existing behavioral tests through the project check command before handoff.
- Keep formatting adoption in a separate mechanical change from behavior fixes.
- During initial rollout, record qualified tool versions, mixed-source behavior,
  the findings resolved and measured check runtime. Obtain independent review
  before claiming the candidate meets this standard.

The UI Lead owns applying this standard to the UI candidate. The Architect checks
its implementation and evidence as part of acceptance.
