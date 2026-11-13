# UI source and release standards

Terry approved these expectations for localswim V2. The UI Lead owns
implementation, the Architect owns acceptance, and Terry owns production
cutover approval.

## Source quality

Follow the [TypeScript quality standard](typescript-standard.md) for compiler
checks, curated lint rules and canonical Prettier formatting. Keep source
readable and retain useful diagnostics throughout release optimization.

## Release assets

| Asset | Release expectation |
| --- | --- |
| JavaScript | Bundle and minify with esbuild, removing unused code where safe |
| CSS | Minify with esbuild while preserving selectors and behavior |
| HTML | Apply conservative cleanup that preserves meaningful text and whitespace |
| Raster UI images | Supply compressed derivatives sized for the display context and pixel density |
| Source maps | Retain matching external debugging artifacts, available when needed |

Pin the qualified build-tool versions and make the output reproducible. Preserve
object property names, including API fields, persisted data and browser
interfaces. Use ordinary identifier minification with property mangling disabled.
Preserve required license notices and useful error information. [esbuild options](https://esbuild.github.io/api/#minify)

HTML cleanup must preserve text spacing, preformatted content, form values and
accessibility behavior. The current entry document is approximately 1 KB, so keep
its processing proportional to the available savings.

Record raw and compressed asset sizes during release qualification. Review
material growth using the source of the increase and its functional value.
Compression is a separate optional transport optimization whose cost and benefit
should be measured for the loopback service.

## Images and attachments

Size images for their rendered dimensions multiplied by the display's pixel
density. For example, a 200 CSS-pixel image can use a 400-pixel source on a 2x
display. Supply suitable variants with `srcset` and `sizes` where applicable,
and explicit layout dimensions so the browser can reserve space. [Responsive images](https://developer.mozilla.org/en-US/docs/Web/HTML/Guides/Responsive_images)

Retain original attachment bytes for download, export and backup. Use appropriately
sized thumbnail derivatives for card and drawer previews, preserving the ability
to inspect the original. Choose compression and image format to suit the content
and preserve the required visual quality.

## Packaging and acceptance

The Go executable embeds the optimized browser assets. `dist/` contains release
deliverables and matching debugging artifacts. Keep source maps associated with
the exact build they describe and load them for debugging when needed.

Verify generated assets against a fresh run of the complete release pipeline,
including optimization. Run release acceptance and browser demonstrations using
the exact optimized assets embedded in the candidate executable. Confirm that
minification preserves behavior, error handling, CSP and rendered content.

The implementation handoff includes qualified tool versions, asset sizes and
the verification results for the built candidate.
