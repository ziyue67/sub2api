# BPS image policy settings / BPS 图片限额设置

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy)；下列文件名与验证说明保留用于追溯。

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
| Before | [before-zh.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/before-zh.png) | [before-en.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/before-en.png) |
| Off (default) | [off-zh.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/off-zh.png) | [off-en.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/off-en.png) |
| Automatic compaction | [auto_compact-zh.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/auto_compact-zh.png) | [auto_compact-en.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/auto_compact-en.png) |
| Warning interception | [warn-zh.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/warn-zh.png) | [warn-en.png](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/bps-image-policy/warn-en.png) |
