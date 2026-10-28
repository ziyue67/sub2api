/**
 * Keep the first enabled route aligned with the API key's directly-bound group.
 * If the target group already exists in another route, swap the route priorities
 * so each group's weight and cooldown settings stay with that group.
 */
export function syncPrimaryGroupRoute(groupId, routes) {
    const nextRoutes = routes.map((route) => ({ ...route }));
    if (groupId === null || nextRoutes.length === 0)
        return nextRoutes;
    const primaryIndex = nextRoutes
        .map((route, index) => ({ route, index }))
        .filter(({ route }) => route.enabled !== false && route.group_id > 0)
        .sort((a, b) => a.route.priority - b.route.priority || a.index - b.index)[0]?.index;
    if (primaryIndex === undefined)
        return nextRoutes;
    const existingIndex = nextRoutes.findIndex((route, index) => index !== primaryIndex && route.group_id === groupId);
    if (existingIndex >= 0) {
        const primaryRoute = nextRoutes[primaryIndex];
        const existingRoute = nextRoutes[existingIndex];
        nextRoutes[existingIndex] = {
            ...primaryRoute,
            priority: existingRoute.priority
        };
        nextRoutes[primaryIndex] = {
            ...existingRoute,
            priority: primaryRoute.priority,
            enabled: true
        };
    }
    else {
        nextRoutes[primaryIndex].group_id = groupId;
    }
    return nextRoutes.sort((a, b) => a.priority - b.priority);
}
