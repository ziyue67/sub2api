import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useAppStore } from '@/stores/app';
import { adminAPI } from '@/api/admin';
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError';
export const OPENAI_EXCEL_OAUTH_CLIENT_ID = 'app_fnr0pYvVwwFDocDumLG3H2Bp';
export function useOpenAIOAuth() {
    const appStore = useAppStore();
    const { t } = useI18n();
    const endpointPrefix = '/admin/openai';
    // State
    const authUrl = ref('');
    const sessionId = ref('');
    const oauthState = ref('');
    const loading = ref(false);
    const error = ref('');
    // Reset state
    const resetState = () => {
        authUrl.value = '';
        sessionId.value = '';
        oauthState.value = '';
        loading.value = false;
        error.value = '';
    };
    // Generate auth URL for OpenAI OAuth
    const generateAuthUrl = async (proxyId, redirectUri, oauthClient = 'codex') => {
        loading.value = true;
        authUrl.value = '';
        sessionId.value = '';
        oauthState.value = '';
        error.value = '';
        try {
            const payload = {};
            if (oauthClient === 'excel')
                payload.oauth_client = 'excel';
            if (proxyId) {
                payload.proxy_id = proxyId;
            }
            if (redirectUri) {
                payload.redirect_uri = redirectUri;
            }
            const response = await adminAPI.accounts.generateAuthUrl(`${endpointPrefix}/generate-auth-url`, payload);
            authUrl.value = response.auth_url;
            sessionId.value = response.session_id;
            try {
                const parsed = new URL(response.auth_url);
                oauthState.value = parsed.searchParams.get('state') || '';
            }
            catch {
                oauthState.value = '';
            }
            return true;
        }
        catch (err) {
            error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.openai.failedToGenerateUrl'));
            appStore.showError(error.value);
            return false;
        }
        finally {
            loading.value = false;
        }
    };
    // Exchange auth code for tokens
    const exchangeAuthCode = async (code, currentSessionId, state, proxyId) => {
        if (!code.trim() || !currentSessionId || !state.trim()) {
            error.value = 'Missing auth code, session ID, or state';
            return null;
        }
        loading.value = true;
        error.value = '';
        try {
            const payload = {
                session_id: currentSessionId,
                code: code.trim(),
                state: state.trim()
            };
            if (proxyId) {
                payload.proxy_id = proxyId;
            }
            const tokenInfo = await adminAPI.accounts.exchangeCode(`${endpointPrefix}/exchange-code`, payload);
            return tokenInfo;
        }
        catch (err) {
            error.value = extractI18nErrorMessage(err, t, 'admin.accounts.oauth.openai.errors', t('admin.accounts.oauth.openai.failedToExchangeCode'));
            appStore.showError(error.value);
            return null;
        }
        finally {
            loading.value = false;
        }
    };
    // Validate refresh token and get full token info
    // clientId: 指定 OAuth client_id（用于第三方渠道获取的 RT，如 app_LlGpXReQgckcGGUo2JrYvtJK）
    const validateRefreshToken = async (refreshToken, proxyId, clientId) => {
        if (!refreshToken.trim()) {
            error.value = 'Missing refresh token';
            return null;
        }
        loading.value = true;
        error.value = '';
        try {
            // Use dedicated refresh-token endpoint
            const tokenInfo = await adminAPI.accounts.refreshOpenAIToken(refreshToken.trim(), proxyId, `${endpointPrefix}/refresh-token`, clientId);
            return tokenInfo;
        }
        catch (err) {
            error.value = extractI18nErrorMessage(err, t, 'admin.accounts.oauth.openai.errors', t('admin.accounts.oauth.openai.failedToValidateRT'));
            appStore.showError(error.value);
            return null;
        }
        finally {
            loading.value = false;
        }
    };
    // Build credentials for OpenAI OAuth account (aligned with backend BuildAccountCredentials)
    const buildCredentials = (tokenInfo) => {
        const creds = {
            access_token: tokenInfo.access_token,
            expires_at: tokenInfo.expires_at
        };
        // 仅在返回了新的 refresh_token 时才写入，防止用空值覆盖已有令牌
        if (tokenInfo.refresh_token) {
            creds.refresh_token = tokenInfo.refresh_token;
        }
        if (tokenInfo.id_token) {
            creds.id_token = tokenInfo.id_token;
        }
        if (tokenInfo.email) {
            creds.email = tokenInfo.email;
        }
        if (tokenInfo.chatgpt_account_id) {
            creds.chatgpt_account_id = tokenInfo.chatgpt_account_id;
        }
        if (tokenInfo.chatgpt_user_id) {
            creds.chatgpt_user_id = tokenInfo.chatgpt_user_id;
        }
        if (tokenInfo.organization_id) {
            creds.organization_id = tokenInfo.organization_id;
        }
        if (tokenInfo.plan_type) {
            creds.plan_type = tokenInfo.plan_type;
        }
        if (tokenInfo.subscription_expires_at) {
            creds.subscription_expires_at = tokenInfo.subscription_expires_at;
        }
        if (tokenInfo.client_id) {
            creds.client_id = tokenInfo.client_id;
        }
        return creds;
    };
    // Build extra info from token response
    const buildExtraInfo = (tokenInfo) => {
        const extra = {};
        if (tokenInfo.email) {
            extra.email = tokenInfo.email;
        }
        if (tokenInfo.name) {
            extra.name = tokenInfo.name;
        }
        if (tokenInfo.privacy_mode) {
            extra.privacy_mode = tokenInfo.privacy_mode;
        }
        return Object.keys(extra).length > 0 ? extra : undefined;
    };
    return {
        // State
        authUrl,
        sessionId,
        oauthState,
        loading,
        error,
        // Methods
        resetState,
        generateAuthUrl,
        exchangeAuthCode,
        validateRefreshToken,
        buildCredentials,
        buildExtraInfo
    };
}
