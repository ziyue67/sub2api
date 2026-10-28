const ONE_DAY_MS = 24 * 60 * 60 * 1000;
export function isOneTimeDailyQuota(subscription) {
    if (!subscription.starts_at || !subscription.expires_at)
        return false;
    const startsAt = new Date(subscription.starts_at).getTime();
    const expiresAt = new Date(subscription.expires_at).getTime();
    if (!Number.isFinite(startsAt) || !Number.isFinite(expiresAt))
        return false;
    return expiresAt <= startsAt + ONE_DAY_MS;
}
export function getRemainingDurationParts(targetAt, now = new Date()) {
    const targetTime = targetAt instanceof Date ? targetAt.getTime() : new Date(targetAt).getTime();
    const nowTime = now.getTime();
    if (!Number.isFinite(targetTime) || !Number.isFinite(nowTime))
        return null;
    const diffMs = targetTime - nowTime;
    if (diffMs <= 0)
        return null;
    const totalMinutes = Math.floor(diffMs / (1000 * 60));
    const days = Math.floor(totalMinutes / (24 * 60));
    const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
    const minutes = totalMinutes % 60;
    return { days, hours, minutes };
}
export function getExpirationDateRelation(targetAt, now = new Date()) {
    const target = targetAt instanceof Date ? targetAt : new Date(targetAt);
    const targetTime = target.getTime();
    const nowTime = now.getTime();
    if (!Number.isFinite(targetTime) || !Number.isFinite(nowTime))
        return null;
    if (targetTime <= nowTime)
        return 'expired';
    const targetDay = Date.UTC(target.getFullYear(), target.getMonth(), target.getDate());
    const currentDay = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
    const calendarDays = Math.round((targetDay - currentDay) / ONE_DAY_MS);
    if (calendarDays === 0)
        return 'today';
    if (calendarDays === 1)
        return 'tomorrow';
    return 'later';
}
export function getRemainingExpiryDuration(targetAt, now = new Date()) {
    const targetTime = targetAt instanceof Date ? targetAt.getTime() : new Date(targetAt).getTime();
    const nowTime = now.getTime();
    if (!Number.isFinite(targetTime) || !Number.isFinite(nowTime))
        return null;
    const diffMs = targetTime - nowTime;
    if (diffMs <= 0)
        return null;
    if (diffMs >= ONE_DAY_MS) {
        return { unit: 'days', days: Math.ceil(diffMs / ONE_DAY_MS) };
    }
    const totalMinutes = Math.ceil(diffMs / (60 * 1000));
    return {
        unit: 'hoursMinutes',
        hours: Math.floor(totalMinutes / 60),
        minutes: totalMinutes % 60
    };
}
