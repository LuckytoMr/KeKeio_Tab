# KeKeIO Tab

KeKeIO Tab is a local-first new tab extension with grouped shortcuts, wallpapers, portable configuration backups, and cloud sync.

## Development

```powershell
pnpm install
pnpm dev -- --host 127.0.0.1 --port 5173
```

Open:

```text
http://localhost:5173/newtab.html
```

## Build

```powershell
pnpm test
pnpm build
```

The unpacked extension output is:

```text
extension/dist
```

Load it in Chrome or Edge:

1. Open `chrome://extensions` or `edge://extensions`.
2. Enable developer mode.
3. Choose "Load unpacked".
4. Select the repository's `extension/dist` directory.

## Current Slice

- Manifest V3 new tab override.
- New tab UI with search, groups, shortcut add/edit/delete.
- KeKeIO Tab search engine list with Baidu first and Google second.
- IndexedDB is the authoritative local-first profile store; existing `chrome.storage.local` or development localStorage profiles are imported once as legacy data.
- Local wallpaper upload in IndexedDB.
- Wallpaper workspace with four sources: official picks, web resources, local upload, and selected pool.
- Web resource catalog shape with provider, source page, tags, and 4K/2K/HD variants.
- Non-repeating random wallpaper rotation.
- Effective settings for density, columns, sidebar side, brand visibility, search engine, wallpaper categories, overlay, and blur.
- Backend account login/register inside the extension.
- The extension backend is fixed to `https://tab.kekeio.com`; users cannot redirect credentials or sync traffic to another host.
- Backend profile save/load using Bearer token, idempotent profile saves, and credentials stored separately from profile data.
- Ordinary configuration export uses a strict, versioned `SharedProfileV2` allowlist envelope. It excludes device identity, credentials, sync runtime/error metadata, rotation history, local blobs, and every local `assetId`; importing it preserves the current device-local state.
- Three-minute automatic backend sync after local changes, plus a manual real-time sync button.
- GitHub Gist profile sync using the user's own token; token and Gist ID are stored only in browser local storage.
- Web wallpapers are login-gated and loaded from the backend API before falling back to provider crawling.
- Remote styles are login-gated, CSS-only, and can be refreshed from the backend.
- Options page.

## Not Implemented Yet

- Third-party wallpaper providers.
