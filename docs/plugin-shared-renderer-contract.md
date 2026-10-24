# Shared plugin rendering contract

This bounded native renderer extension supports the original AutoSignIn detail page and AutoBackup configuration without plugin-specific rendering branches. It does not change plugin execution authority, configuration-save semantics, `DesiredEnabled`, or custom API authorization.

## Components and visibility

- Detail pages may use `VSpacer` without properties and `VChip` with string-token `color`, `size`, `variant`, `prepend-icon`, and `append-icon` properties. Navigation, event handlers and arbitrary properties are rejected.
- Forms may use `VSubheader` with a plain-text `text` property. The host maps it to Vuetify 3's `VListSubheader` title, without HTML interpretation.
- `props.show` accepts only `{{identifier}}`, with optional surrounding whitespace. The identifier starts with an ASCII letter or underscore and continues with ASCII letters, digits or underscores. Only an own model field is read.
- Visibility follows JavaScript truthiness: false, zero, empty string, null and undefined hide; an empty array is truthy. The host toggles display rather than deleting values, saving configuration or unmounting the field.
- Missing own fields fail closed as hidden. Inherited members, property traversal, arithmetic, function calls and arbitrary expressions are not evaluated. This is intentionally narrower than the old renderer's dynamic JavaScript evaluation.
- Compound root/item conditions for a remote plugin's first-configuration editor are not supplied by this extension.

## Detail-page style subset

A `style` node contains text only, with no properties or child nodes. The existing pinned PostCSS parser builds an AST; the host validates every node and rebuilds approved rules. The implementation supports bounded class/table-element selectors, `:last-child`, explicit transparent-theme ancestor prefixes, a single level of width-only media queries and a fixed presentation-property allowlist.

Every selector is scoped to the unique detail-dialog content container. Global roots, arbitrary attributes/combinators/pseudo-selectors, nested rules, imports, fonts, script/HTML syntax, escapes, URL/data resources and unapproved functions/properties are rejected. Background properties permit only transparent, hexadecimal colors or one complete rgb/rgba function. Backdrop filtering permits only one complete blur function. Bare variables cannot be used to smuggle resources into these image/filter-capable properties. Approved theme variables can occur inside their typed color/blur functions.

Unknown components and unsupported behavior remain visible errors; the host does not silently discard original nodes to declare compatibility.

## Verification scope

The final source was checked against complete captured page/form structures, including six WebDAV visibility declarations. The focused positive/negative cases and the full 233-test frontend suite passed; TypeScript checks and the production Vite build passed. The old unsupported-component failures and a rejected intermediate overly broad visibility grammar remain recorded. Actual joint-browser verification is recorded below; final delivery additionally requires aggregate publication and remote readback. Original remote WebDAV service operation is outside the Linux/local acceptance scope.


## Joint UI validation, 2026-10-23

The final V4 source passed all 233 web tests, type checking and the production
build. Real cloud Chrome used newly built ordinary server/fixture binaries.
AutoSignIn source and enabled clone pages retained both history tables, chips,
spacers and uniquely scoped styles after refresh (two owned loopback requests,
zero header errors). Its paused cold clone Page HTTP 400 remains a separate
host admission issue, not claimed fixed here. AutoBackup verified the subheader,
all six literal show conditions, hidden value preservation, cancellation without
saving, public clone settings and local once execution. No remote WebDAV test
or actual third-party account request is claimed.

The original static failures and superseded V3 resource-indirection issue are
retained in local evidence. V4 rejects resource-bearing properties/variables,
selector escapes, global selectors, imports and URL/data resources. This is a
bounded display contract, not arbitrary CSS or an expression interpreter.

Cold configuration draft schemas, object-list editors, compound root/item
conditions, unrelated plugin host fixes and pending Merge uploads are excluded.
Main is not automatically updated by this delivery.
