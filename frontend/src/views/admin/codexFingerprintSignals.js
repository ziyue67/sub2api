const VALID_TYPES = [
    "header_exact",
    "header_prefix",
    "body_path",
];
export function parseFingerprintSignalsToRows(raw) {
    if (!raw || !raw.trim())
        return [];
    try {
        const arr = JSON.parse(raw);
        if (!Array.isArray(arr))
            return [];
        return arr.map((e) => ({
            type: VALID_TYPES.includes(e?.type) ? e.type : "header_exact",
            match: Array.isArray(e?.match)
                ? e.match.filter((x) => typeof x === "string").join(" / ")
                : "",
            required: e?.required === true,
        }));
    }
    catch {
        return [];
    }
}
export function serializeFingerprintRowsToJSON(rows) {
    const entries = rows
        .map((r) => ({
        type: r.type,
        match: r.match
            .split("/")
            .map((s) => s.trim())
            .filter((s) => s.length > 0),
        required: r.required === true,
    }))
        .filter((e) => e.match.length > 0);
    return JSON.stringify(entries);
}
export function defaultFingerprintSignalRows() {
    return [
        { type: "header_prefix", match: "x-codex-", required: true },
        { type: "header_exact", match: "session-id / session_id", required: false },
        { type: "header_exact", match: "thread-id / thread_id", required: false },
        {
            type: "body_path",
            match: "client_metadata.x-codex-window-id / client_metadata.x-codex-installation-id",
            required: false,
        },
    ];
}
