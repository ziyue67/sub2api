# BPS image policy settings / BPS 图片限额设置

Captured on 2026-09-28 using the real SettingsView component and local Vite,
with offline API fixtures containing synthetic administrator settings. No production
API, credentials, conversations or pictures were used. The screenshots are UI
review evidence, not backend integration or live BPS acceptance evidence.

- Baseline: production 62ac3a5dd125f76b23d655c491c5bf2c5816fb5b.
- Before: SettingsView source from that commit; after: this PR's SettingsView.
- Browser viewport: 1440 × 1200. Images crop the image settings controls.
- Noto Sans SC was loaded locally because the test host lacks CJK fonts.
- Fixture: image support enabled, native upload, maximum 20 images,
  warning remainder 8, compact reserve 3.

## Review steps / 复核步骤

1. Open Facilities → Feature switches → Excel / BPS image support.
2. Enable image support and choose native upload.
3. Verify Off is the new policy default. Switch to automatic compaction, then warning interception.
4. Warning remainder and compact reserve inputs appear only in warning mode.
5. Save valid settings; reject invalid warning/reserve/maximum combinations.
6. Repeat in Chinese and English. Persistence is verified separately by isolated application tests.

| State | 中文 | English |
| --- | --- | --- |
| Before | [before-zh.png](before-zh.png) | [before-en.png](before-en.png) |
| Off (default) | [off-zh.png](off-zh.png) | [off-en.png](off-en.png) |
| Automatic compaction | [auto_compact-zh.png](auto_compact-zh.png) | [auto_compact-en.png](auto_compact-en.png) |
| Warning interception | [warn-zh.png](warn-zh.png) | [warn-en.png](warn-en.png) |
