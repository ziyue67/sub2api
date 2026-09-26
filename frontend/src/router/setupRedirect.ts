export function resolveCompletedSetupRedirectPath(
  isAuthenticated: boolean,
  isAdmin: boolean,
  isObserver = false
): string {
  if (!isAuthenticated) {
    return '/login'
  }

  if (isAdmin) return '/admin/dashboard'
  return isObserver ? '/admin/accounts' : '/dashboard'
}
