# Node colors, project header, and fixed workspace preferences design

## Goal

Use blue and orange for the first two node identities, preserve purple as the third palette color, make the non-session project header use the selected root's node color, and simplify the left workspace settings menu by fixing two preferences on.

## Evidence

The browser is not loading an outdated bundle: deployed `index-CYjassRv.js` and local `web/dist/assets/index-CYjassRv.js` have identical SHA-256 `9d16295e…08563e9`. Vite already uses content-addressed asset filenames; an unchanged rebuild correctly preserves the same hash. The visible blue/orange node colors therefore come from persisted `mindfs_nodes` data created before the current palette migration, not deployment lag.

## Design

1. Keep Vite's existing content-hash strategy. Add a source-level regression assertion for the build configuration rather than randomizing asset names. A changed source bundle must receive a changed hash; an unchanged build must retain its hash for cache efficiency.
2. Change the canonical `PALETTE` in `web/src/services/nodeRegistry.ts` to `#3b82f6`, `#f59e0b`, `#7c6bd6`, followed by the existing red, gray, and green entries. Preserve mappings for both the immediate prior palette and the older palette already recognized by the shipped migration, so all existing stored node colors converge by palette position to the new canonical values.
3. Add a `rootColor` prop to `DefaultListView` and apply it to the existing `data-onboarding="project-home"` badge. Pass the current root's `_nodeColor` from `App.tsx`. The component is shared by desktop and mobile layouts, so no viewport-specific branch is necessary.
4. Set project-tree file and directory row text to `fontWeight: 400`, including the selected state. Preserve bold styling exclusively for managed-root project labels.
5. Remove the four specified controls from the FileTree settings menu: onboarding, swap-sidebars, multi-project sessions, and hidden files. Remove now-unused FileTree props/callbacks where they only exist for these controls.
6. Make `showHiddenFiles` and `multiProjectSessionsEnabled` constant `true` in `App.tsx`; remove their state toggles and local-storage persistence. Preserve their existing downstream code paths, which already render hidden files and multi-project groups when true.
7. Add the smallest source-level regression tests covering palette migration, the project-header color prop, permanent preference values, removed menu controls, tree font weight, and Vite content hashing. Preserve the existing uncommitted session-sync change.

## Acceptance criteria

- Fresh local storage assigns `#3b82f6` to `local` and `#f59e0b` to the first added node.
- Existing stored nodes using the immediate prior palette migrate deterministically.
- A file/task view's `project-home` badge uses the active root's node color.
- Desktop and mobile use the same header rendering path.
- Project-tree file/directory labels remain `font-weight: 400` when selected; managed-root labels retain their emphasis.
- The four removed menu items do not render.
- Multi-project sessions and hidden files are always enabled and cannot be disabled via stored preferences.
- Changed source produces a changed content-hash asset filename; unchanged content may reuse the hash.
