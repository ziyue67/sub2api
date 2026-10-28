export function entryKey(entry) {
    return entry.channel_id + ':' + entry.group.id + ':' + entry.name;
}
