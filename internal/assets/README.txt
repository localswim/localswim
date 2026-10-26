Verified embedded production bundle

Handler() returns an HTTP handler only after complete embedded inventory and
SHA-256 verification. Verify() exposes the same startup check. Missing, tampered,
undeclared or malformed assets fail startup. There is no runtime disk fallback,
directory listing or catch-all application rewrite. / and /index.html share HTML;
JavaScript uses canonical .js URLs only. Source maps and query aliases are absent.

The HTML loads /js/app.js as an external ES module and /styles/app.css. The handler
sets script-src 'self', connect-src 'self', a restrictive CSP, nosniff and no-store.
No CDN, inline script, private configuration or credential enters these files.
Build and manifest maintenance belong to cmd/assetstage; do not edit bundle output.

web/src/app.ts reads only X-Localswim-Installation from a same-origin HTTPS GET /
with credentials omitted, no-store and redirects refused. The native executable
adds its active installation UUID on GET / and /index.html without changing the
verified HTML body. Missing, malformed, zero or redirected metadata keeps the
entry unavailable. A valid UUID mounts the shared current-credential provider,
SessionView and SessionWorkspace with the literal profile identity unqualified
and no qualification proof. The real session exchange supplies board, incarnation
and actor identities. Without profile proof the shared recovery provider refuses
dispatch; this metadata is not authentication or positive qualification.

mountApplication(root, profile) is the explicit trusted composition hook for
later V02 profile proof. It replaces and closes a previous composition at that
root, including a pending open. No query parameter or browser capability guess
supplies proof. Future V02 qualification supplies proof only after its own accepted
gates. Synthetic browser harnesses do not qualify a production profile.

No installation trust enrollment, human grant, credential issuance, automatic
login, restore, original-operation lookup or replay occurs in this entry. W02/W03
own UI behavior and policy. P02 owns scripts; Q04 and V02 own later product build
and browser qualification respectively.
