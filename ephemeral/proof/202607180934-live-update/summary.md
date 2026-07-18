# Live update proof

Proved commit: `e87c97b`

## Before activation

- Installed ChatGPT Desktop: `26.715.31251`, build `5538`.
- Available release: `26.715.31925`, build `5551`.
- Desktop application PID: `71795`.
- Desktop app-server PID: `56981`.
- The corrected watcher reported active thread `019f75d0-0d8a-7ed3-a509-8b8f9c776525` and waited for it to complete.
- Build 5551 was freshly downloaded, staged, and verified after the previous staged bundle was removed.

## Observed transition

```text
time=2026-07-18T09:35:03.600-06:00 level=INFO msg="waiting for Desktop tasks to finish" threads=[019f75d0-0d8a-7ed3-a509-8b8f9c776525]
time=2026-07-18T09:36:09.838-06:00 level=INFO msg="waiting for uninterrupted Desktop idle window" idle_since=2026-07-18T15:36:08Z remaining=59s
time=2026-07-18T09:37:09.498-06:00 level=INFO msg="Desktop idle window satisfied" idle_since=2026-07-18T15:36:08Z idle_window=1m0s
time=2026-07-18T09:37:12.988-06:00 level=INFO msg="requesting graceful ChatGPT Desktop shutdown" pid=71795
time=2026-07-18T09:37:15.956-06:00 level=INFO msg="ChatGPT Desktop update completed" old_build=5538 new_build=5551 version=26.715.31925
time=2026-07-18T09:37:15.956-06:00 level=INFO msg="watch cycle installed an update"
```

## After activation

- Installed and available builds both report `5551`; `update_available` is false.
- The relaunched Desktop application PID is `74469`.
- The app-server remains discoverable as PID `56981` and correctly reports the new active turn.
- `codesign --verify --deep --strict --verbose=2 /Applications/ChatGPT.app` reports the application valid on disk and satisfying its designated requirement.
- `spctl --assess --type execute --verbose=2 /Applications/ChatGPT.app` reports `accepted`, source `Notarized Developer ID`.
- No update staging, backup, failed-bundle, or failed-build quarantine residue remains.

