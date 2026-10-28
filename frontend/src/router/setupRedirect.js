export function resolveCompletedSetupRedirectPath(isAuthenticated, isAdmin, isObserver = false) {
    if (!isAuthenticated) {
        return '/login';
    }
    if (isAdmin)
        return '/admin/dashboard';
    return isObserver ? '/admin/accounts' : '/dashboard';
}
