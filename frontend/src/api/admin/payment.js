/**
 * Admin Payment API endpoints
 * Handles payment management operations for administrators
 */
import { apiClient } from '../client';
export const adminPaymentAPI = {
    // ==================== Config ====================
    /** Get payment configuration (admin view) */
    getConfig() {
        return apiClient.get('/admin/payment/config');
    },
    /** Update payment configuration */
    updateConfig(data) {
        return apiClient.put('/admin/payment/config', data);
    },
    // ==================== Dashboard ====================
    /** Get payment dashboard statistics */
    getDashboard(days) {
        return apiClient.get('/admin/payment/dashboard', {
            params: days ? { days } : undefined
        });
    },
    // ==================== Orders ====================
    /** Get all orders (paginated, with filters) */
    getOrders(params) {
        return apiClient.get('/admin/payment/orders', { params });
    },
    /** Get a specific order by ID */
    getOrder(id) {
        return apiClient.get(`/admin/payment/orders/${id}`);
    },
    /** Cancel an order (admin) */
    cancelOrder(id) {
        return apiClient.post(`/admin/payment/orders/${id}/cancel`);
    },
    /** Retry recharge for a failed order */
    retryRecharge(id) {
        return apiClient.post(`/admin/payment/orders/${id}/retry`);
    },
    /** Process a refund */
    refundOrder(id, data) {
        return apiClient.post(`/admin/payment/orders/${id}/refund`, data);
    },
    /** Query and finalize a pending refund */
    queryRefund(id) {
        return apiClient.post(`/admin/payment/orders/${id}/refund/query`);
    },
    // ==================== Channels ====================
    /** Get all payment channels */
    getChannels() {
        return apiClient.get('/admin/payment/channels');
    },
    /** Create a payment channel */
    createChannel(data) {
        return apiClient.post('/admin/payment/channels', data);
    },
    /** Update a payment channel */
    updateChannel(id, data) {
        return apiClient.put(`/admin/payment/channels/${id}`, data);
    },
    /** Delete a payment channel */
    deleteChannel(id) {
        return apiClient.delete(`/admin/payment/channels/${id}`);
    },
    // ==================== Subscription Plans ====================
    /** Get all subscription plans */
    getPlans() {
        return apiClient.get('/admin/payment/plans');
    },
    /** Create a subscription plan */
    createPlan(data) {
        return apiClient.post('/admin/payment/plans', data);
    },
    /** Update a subscription plan */
    updatePlan(id, data) {
        return apiClient.put(`/admin/payment/plans/${id}`, data);
    },
    /** Delete a subscription plan */
    deletePlan(id) {
        return apiClient.delete(`/admin/payment/plans/${id}`);
    },
    // ==================== Provider Instances ====================
    /** Get all provider instances */
    getProviders() {
        return apiClient.get('/admin/payment/providers');
    },
    /** Create a provider instance */
    createProvider(data) {
        return apiClient.post('/admin/payment/providers', data);
    },
    /** Update a provider instance */
    updateProvider(id, data) {
        return apiClient.put(`/admin/payment/providers/${id}`, data);
    },
    /** Delete a provider instance */
    deleteProvider(id) {
        return apiClient.delete(`/admin/payment/providers/${id}`);
    }
};
export default adminPaymentAPI;
