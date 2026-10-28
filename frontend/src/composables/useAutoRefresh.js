import { ref, onBeforeUnmount } from 'vue';
export function useAutoRefresh(options) {
    const { storageKey, intervals = [5, 10, 15, 30], defaultInterval, onRefresh, shouldPause, } = options;
    const enabled = ref(options.defaultEnabled ?? false);
    const intervalSeconds = ref(defaultInterval ?? intervals[intervals.length - 1]);
    const countdown = ref(0);
    const fetching = ref(false);
    let timerId;
    function loadFromStorage() {
        try {
            const saved = localStorage.getItem(storageKey);
            if (!saved)
                return;
            const parsed = JSON.parse(saved);
            enabled.value = parsed.enabled === true;
            const iv = Number(parsed.interval_seconds);
            if (intervals.includes(iv))
                intervalSeconds.value = iv;
        }
        catch { /* ignore */ }
    }
    function saveToStorage() {
        try {
            localStorage.setItem(storageKey, JSON.stringify({
                enabled: enabled.value,
                interval_seconds: intervalSeconds.value,
            }));
        }
        catch { /* ignore */ }
    }
    async function tick() {
        if (!enabled.value)
            return;
        if (shouldPause?.())
            return;
        if (fetching.value)
            return;
        if (countdown.value <= 1) {
            countdown.value = intervalSeconds.value;
            fetching.value = true;
            try {
                await onRefresh();
            }
            finally {
                fetching.value = false;
            }
            return;
        }
        countdown.value -= 1;
    }
    function start() {
        if (timerId !== undefined)
            return;
        timerId = setInterval(tick, 1000);
    }
    function stop() {
        if (timerId !== undefined) {
            clearInterval(timerId);
            timerId = undefined;
        }
    }
    function setEnabled(value) {
        enabled.value = value;
        saveToStorage();
        if (value) {
            countdown.value = intervalSeconds.value;
            start();
        }
        else {
            stop();
            countdown.value = 0;
        }
    }
    function setInterval_(seconds) {
        intervalSeconds.value = seconds;
        saveToStorage();
        if (enabled.value)
            countdown.value = seconds;
    }
    function resetCountdown() {
        countdown.value = intervalSeconds.value;
    }
    loadFromStorage();
    onBeforeUnmount(stop);
    return {
        enabled: enabled,
        intervalSeconds: intervalSeconds,
        countdown: countdown,
        fetching: fetching,
        intervals,
        setEnabled,
        setInterval: setInterval_,
        resetCountdown,
        start,
        stop,
    };
}
