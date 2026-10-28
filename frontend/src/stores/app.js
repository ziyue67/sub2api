/**
 * Application State Store
 * Manages global UI state including sidebar, loading indicators, and toast notifications
 */
import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
import { i18n } from '@/i18n';
import { checkUpdates as checkUpdatesAPI } from '@/api/admin/system';
import { getPublicSettings as fetchPublicSettingsAPI } from '@/api/auth';
export const useAppStore = defineStore('app', () => {
    // ==================== State ====================
    const sidebarCollapsed = ref(false);
    const mobileOpen = ref(false);
    const sidebarScrollTop = ref(0);
    const loading = ref(false);
    const toasts = ref([]);
    // Public settings cache state
    const publicSettingsLoaded = ref(false);
    const publicSettingsLoading = ref(false);
    const siteName = ref('Sub2API');
    const siteLogo = ref('');
    const siteVersion = ref('');
    const contactInfo = ref('');
    const apiBaseUrl = ref('');
    const docUrl = ref('');
    const cachedPublicSettings = ref(null);
    let publicSettingsRequest = null;
    // Version cache state
    const versionLoaded = ref(false);
    const versionLoading = ref(false);
    const currentVersion = ref('');
    const latestVersion = ref('');
    const hasUpdate = ref(false);
    const buildType = ref('source');
    const releaseInfo = ref(null);
    // Auto-incrementing ID for toasts
    let toastIdCounter = 0;
    // ==================== Computed ====================
    const hasActiveToasts = computed(() => toasts.value.length > 0);
    const backendModeEnabled = computed(() => cachedPublicSettings.value?.backend_mode_enabled ?? false);
    const loadingCount = ref(0);
    // ==================== Actions ====================
    /**
     * Toggle sidebar collapsed state
     */
    function toggleSidebar() {
        sidebarCollapsed.value = !sidebarCollapsed.value;
    }
    /**
     * Set sidebar collapsed state explicitly
     * @param collapsed - Whether sidebar should be collapsed
     */
    function setSidebarCollapsed(collapsed) {
        sidebarCollapsed.value = collapsed;
    }
    /**
     * Toggle mobile sidebar open state
     */
    function toggleMobileSidebar() {
        mobileOpen.value = !mobileOpen.value;
    }
    /**
     * Set mobile sidebar open state explicitly
     * @param open - Whether mobile sidebar should be open
     */
    function setMobileOpen(open) {
        mobileOpen.value = open;
    }
    /**
     * Set global loading state
     * @param isLoading - Whether app is in loading state
     */
    function setLoading(isLoading) {
        if (isLoading) {
            loadingCount.value++;
        }
        else {
            loadingCount.value = Math.max(0, loadingCount.value - 1);
        }
        loading.value = loadingCount.value > 0;
    }
    /**
     * Show a toast notification
     * @param type - Type of toast (success, error, info, warning)
     * @param message - Toast message content
     * @param duration - Auto-dismiss duration in ms (undefined = no auto-dismiss)
     * @returns Toast ID for manual dismissal
     */
    function showToast(type, message, duration) {
        const id = `toast-${++toastIdCounter}`;
        const toast = {
            id,
            type,
            message,
            duration,
            startTime: duration !== undefined ? Date.now() : undefined
        };
        toasts.value.push(toast);
        // Auto-dismiss if duration is specified
        if (duration !== undefined) {
            setTimeout(() => {
                hideToast(id);
            }, duration);
        }
        return id;
    }
    /**
     * Show a success toast
     * @param message - Success message
     * @param duration - Auto-dismiss duration in ms (default: 3000)
     */
    function showSuccess(message, duration = 3000) {
        return showToast('success', message, duration);
    }
    /**
     * Show an error toast
     * @param message - Error message
     * @param duration - Auto-dismiss duration in ms (default: 5000)
     */
    function showError(message, duration = 5000) {
        return showToast('error', message, duration);
    }
    /**
     * Show an info toast
     * @param message - Info message
     * @param duration - Auto-dismiss duration in ms (default: 3000)
     */
    function showInfo(message, duration = 3000) {
        return showToast('info', message, duration);
    }
    /**
     * Show a warning toast
     * @param message - Warning message
     * @param duration - Auto-dismiss duration in ms (default: 4000)
     */
    function showWarning(message, duration = 4000) {
        return showToast('warning', message, duration);
    }
    /**
     * Hide a specific toast by ID
     * @param id - Toast ID to hide
     */
    function hideToast(id) {
        const index = toasts.value.findIndex((t) => t.id === id);
        if (index !== -1) {
            toasts.value.splice(index, 1);
        }
    }
    /**
     * Clear all toasts
     */
    function clearAllToasts() {
        toasts.value = [];
    }
    /**
     * Execute an async operation with loading state
     * Automatically manages loading indicator
     * @param operation - Async operation to execute
     * @returns Promise resolving to operation result
     */
    async function withLoading(operation) {
        setLoading(true);
        try {
            return await operation();
        }
        finally {
            setLoading(false);
        }
    }
    /**
     * Execute an async operation with loading and error handling
     * Shows error toast on failure
     * @param operation - Async operation to execute
     * @param errorMessage - Custom error message (optional)
     * @returns Promise resolving to operation result or null on error
     */
    async function withLoadingAndError(operation, errorMessage) {
        setLoading(true);
        try {
            return await operation();
        }
        catch (error) {
            const message = errorMessage ||
                error.message ||
                i18n.global.t('common.unknownError');
            showError(message);
            return null;
        }
        finally {
            setLoading(false);
        }
    }
    /**
     * Reset app state to defaults
     * Useful for cleanup or testing
     */
    function reset() {
        sidebarCollapsed.value = false;
        loading.value = false;
        loadingCount.value = 0;
        toasts.value = [];
    }
    // ==================== Version Management ====================
    /**
     * Fetch version info (uses cache unless force=true)
     * @param force - Force refresh from API
     */
    async function fetchVersion(force = false) {
        // Return cached data if available and not forcing refresh
        if (versionLoaded.value && !force) {
            return {
                current_version: currentVersion.value,
                latest_version: latestVersion.value,
                has_update: hasUpdate.value,
                build_type: buildType.value,
                release_info: releaseInfo.value || undefined,
                cached: true
            };
        }
        // Prevent duplicate requests
        if (versionLoading.value) {
            return null;
        }
        versionLoading.value = true;
        try {
            const data = await checkUpdatesAPI(force);
            currentVersion.value = data.current_version;
            latestVersion.value = data.latest_version;
            hasUpdate.value = data.has_update;
            buildType.value = data.build_type || 'source';
            releaseInfo.value = data.release_info || null;
            versionLoaded.value = true;
            return data;
        }
        catch (error) {
            console.error('Failed to fetch version:', error);
            return null;
        }
        finally {
            versionLoading.value = false;
        }
    }
    /**
     * Clear version cache (e.g., after update)
     */
    function clearVersionCache() {
        versionLoaded.value = false;
        hasUpdate.value = false;
    }
    // ==================== Public Settings Management ====================
    /**
     * Apply settings to store state (internal helper to avoid code duplication)
     */
    function applySettings(config) {
        if (typeof window !== 'undefined') {
            window.__APP_CONFIG__ = { ...config };
        }
        cachedPublicSettings.value = config;
        siteName.value = config.site_name || 'Sub2API';
        siteLogo.value = config.site_logo || '';
        siteVersion.value = config.version || '';
        contactInfo.value = config.contact_info || '';
        apiBaseUrl.value = config.api_base_url || '';
        docUrl.value = config.doc_url || '';
        publicSettingsLoaded.value = true;
    }
    /**
     * Fetch public settings (uses cache unless force=true)
     * @param force - Force refresh from API
     */
    function fetchPublicSettings(force = false) {
        // An active request always wins over cache/force semantics so every caller observes
        // the same refresh result and no older request can overwrite a newer one.
        if (publicSettingsRequest) {
            return publicSettingsRequest;
        }
        // Check for injected config from server (eliminates flash)
        if (!publicSettingsLoaded.value && !force && window.__APP_CONFIG__) {
            applySettings(window.__APP_CONFIG__);
            return Promise.resolve(window.__APP_CONFIG__);
        }
        // Return cached data if available and not forcing refresh
        if (publicSettingsLoaded.value && !force) {
            if (cachedPublicSettings.value) {
                return Promise.resolve({ ...cachedPublicSettings.value });
            }
            return Promise.resolve({
                registration_enabled: false,
                email_verify_enabled: false,
                force_email_on_third_party_signup: false,
                registration_email_suffix_whitelist: [],
                promo_code_enabled: true,
                password_reset_enabled: false,
                invitation_code_enabled: false,
                turnstile_enabled: false,
                turnstile_site_key: '',
                aliyun_captcha_enabled: false,
                aliyun_captcha_scene_id: '',
                aliyun_captcha_prefix: '',
                aliyun_captcha_region: 'cn',
                site_name: siteName.value,
                site_logo: siteLogo.value,
                site_subtitle: '',
                api_base_url: apiBaseUrl.value,
                contact_info: contactInfo.value,
                doc_url: docUrl.value,
                home_content: '',
                compact_home_enabled: false,
                hide_ccs_import_button: false,
                payment_enabled: false,
                table_default_page_size: 20,
                table_page_size_options: [10, 20, 50, 100],
                custom_menu_items: [],
                custom_endpoints: [],
                linuxdo_oauth_enabled: false,
                wechat_oauth_enabled: false,
                wechat_oauth_open_enabled: false,
                wechat_oauth_mp_enabled: false,
                wechat_oauth_mobile_enabled: false,
                oidc_oauth_enabled: false,
                oidc_oauth_provider_name: 'OIDC',
                github_oauth_enabled: false,
                google_oauth_enabled: false,
                backend_mode_enabled: false,
                passkey_enabled: false,
                version: siteVersion.value,
                balance_low_notify_enabled: false,
                account_quota_notify_enabled: false,
                balance_low_notify_threshold: 0,
                channel_monitor_enabled: true,
                channel_monitor_default_interval_seconds: 60,
                available_channels_enabled: false,
                subscription_enabled: true,
                payment_balance_disabled: false,
                model_plaza_enabled: false,
                model_plaza_require_auth: false,
                plugin_management_enabled: false,
                support_ticket_enabled: false,
                risk_control_enabled: false,
                service_quota_enabled: false,
                affiliate_enabled: false,
                allow_user_view_error_requests: false,
                usage_show_long_context_badge: true,
                usage_show_cache_rate: true,
                usage_show_cache_rate_to_users: false,
                usage_show_upstream_response_model_to_users: false,
            });
        }
        publicSettingsLoading.value = true;
        let apiRequest;
        try {
            apiRequest = fetchPublicSettingsAPI();
        }
        catch (error) {
            console.error('Failed to fetch public settings:', error);
            publicSettingsLoading.value = false;
            return Promise.resolve(null);
        }
        const request = apiRequest
            .then((data) => {
            applySettings(data);
            return data;
        })
            .catch((error) => {
            console.error('Failed to fetch public settings:', error);
            return null;
        })
            .finally(() => {
            if (publicSettingsRequest === request) {
                publicSettingsRequest = null;
                publicSettingsLoading.value = false;
            }
        });
        publicSettingsRequest = request;
        return request;
    }
    /**
     * Clear public settings cache
     */
    function clearPublicSettingsCache() {
        publicSettingsLoaded.value = false;
        cachedPublicSettings.value = null;
    }
    /**
     * Initialize settings from injected config (window.__APP_CONFIG__)
     * This is called synchronously before Vue app mounts to prevent flash
     * @returns true if config was found and applied, false otherwise
     */
    function initFromInjectedConfig() {
        if (window.__APP_CONFIG__) {
            applySettings(window.__APP_CONFIG__);
            return true;
        }
        return false;
    }
    // ==================== Return Store API ====================
    return {
        // State
        sidebarCollapsed,
        mobileOpen,
        sidebarScrollTop,
        loading,
        toasts,
        // Public settings state
        publicSettingsLoaded,
        siteName,
        siteLogo,
        siteVersion,
        contactInfo,
        apiBaseUrl,
        docUrl,
        cachedPublicSettings,
        // Version state
        versionLoaded,
        versionLoading,
        currentVersion,
        latestVersion,
        hasUpdate,
        buildType,
        releaseInfo,
        // Computed
        hasActiveToasts,
        backendModeEnabled,
        // Actions
        toggleSidebar,
        setSidebarCollapsed,
        toggleMobileSidebar,
        setMobileOpen,
        setLoading,
        showToast,
        showSuccess,
        showError,
        showInfo,
        showWarning,
        hideToast,
        clearAllToasts,
        withLoading,
        withLoadingAndError,
        reset,
        // Version actions
        fetchVersion,
        clearVersionCache,
        // Public settings actions
        fetchPublicSettings,
        clearPublicSettingsCache,
        initFromInjectedConfig
    };
});
