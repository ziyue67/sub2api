export function buildOpsErrorTimeParams(timeRange, customStartTime, customEndTime) {
    if (timeRange === 'custom' && customStartTime && customEndTime) {
        return { start_time: customStartTime, end_time: customEndTime };
    }
    return { time_range: timeRange === 'custom' ? '1h' : timeRange };
}
