/**
 * Payment Store
 * Manages payment configuration, current order state, and subscription plans
 */
import { defineStore } from 'pinia';
import { ref } from 'vue';
import { paymentAPI } from '@/api/payment';
export const usePaymentStore = defineStore('payment', () => {
    // ==================== State ====================
    /** Payment configuration from backend */
    const config = ref(null);
    /** Currently active order (for payment flow) */
    const currentOrder = ref(null);
    /** Available subscription plans */
    const plans = ref([]);
    const configLoading = ref(false);
    const configLoaded = ref(false);
    let configPromise = null;
    // ==================== Actions ====================
    /** Fetch payment configuration */
    async function fetchConfig(force = false) {
        if (configLoaded.value && !force)
            return config.value;
        if (configPromise)
            return configPromise;
        configPromise = loadConfig();
        return configPromise;
    }
    async function loadConfig() {
        configLoading.value = true;
        try {
            const response = await paymentAPI.getConfig();
            config.value = response.data;
            configLoaded.value = true;
            return config.value;
        }
        catch (error) {
            console.error('[payment] Failed to fetch config:', error);
            return null;
        }
        finally {
            configLoading.value = false;
            configPromise = null;
        }
    }
    /** Fetch available subscription plans */
    async function fetchPlans() {
        try {
            const response = await paymentAPI.getPlans();
            // Backend returns features as newline-separated string; parse to array
            plans.value = (response.data || []).map((p) => ({
                ...p,
                features: typeof p.features === 'string'
                    ? p.features.split('\n').map((f) => f.trim()).filter(Boolean)
                    : (p.features || []),
            }));
            return plans.value;
        }
        catch (error) {
            console.error('[payment] Failed to fetch plans:', error);
            return [];
        }
    }
    /** Create a new order and set it as current */
    async function createOrder(params) {
        const response = await paymentAPI.createOrder(params);
        return response.data;
    }
    /** Poll order status by ID (read-only, no upstream check) */
    async function pollOrderStatus(orderId) {
        try {
            const response = await paymentAPI.getOrder(orderId);
            const order = response.data;
            if (currentOrder.value?.id === orderId) {
                currentOrder.value = order;
            }
            return order;
        }
        catch (error) {
            console.error('[payment] Failed to poll order status:', error);
            return null;
        }
    }
    /** Clear current order state */
    function clearCurrentOrder() {
        currentOrder.value = null;
    }
    return {
        config,
        currentOrder,
        plans,
        configLoading,
        configLoaded,
        fetchConfig,
        fetchPlans,
        createOrder,
        pollOrderStatus,
        clearCurrentOrder
    };
});
