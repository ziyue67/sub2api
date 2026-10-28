/**
 * User Payment API endpoints
 * Handles payment operations for regular users
 */
import { apiClient } from './client';
export const paymentAPI = {
    /** Get payment configuration (enabled types, limits, etc.) */
    getConfig() {
        return apiClient.get('/payment/config');
    },
    /** Get available subscription plans */
    getPlans() {
        return apiClient.get('/payment/plans');
    },
    /** Get all checkout page data in a single call */
    getCheckoutInfo() {
        return apiClient.get('/payment/checkout-info');
    },
    /** Get payment method limits and fee rates */
    getLimits() {
        return apiClient.get('/payment/limits');
    },
    /** Create a new payment order */
    createOrder(data) {
        return apiClient.post('/payment/orders', data);
    },
    /** Get current user's orders */
    getMyOrders(params) {
        return apiClient.get('/payment/orders/my', { params });
    },
    /** Get a specific order by ID */
    getOrder(id) {
        return apiClient.get(`/payment/orders/${id}`);
    },
    /** Cancel a pending order */
    cancelOrder(id) {
        return apiClient.post(`/payment/orders/${id}/cancel`);
    },
    /** Verify order payment status with upstream provider */
    verifyOrder(outTradeNo) {
        return apiClient.post('/payment/orders/verify', { out_trade_no: outTradeNo });
    },
    /** Legacy-compatible public order lookup by out_trade_no */
    verifyOrderPublic(outTradeNo) {
        return apiClient.post('/payment/public/orders/verify', { out_trade_no: outTradeNo });
    },
    /** Resolve an order from a signed resume token without auth */
    resolveOrderPublicByResumeToken(resumeToken) {
        return apiClient.post('/payment/public/orders/resolve', { resume_token: resumeToken });
    },
    /** Request a refund for a completed order */
    requestRefund(id, data) {
        return apiClient.post(`/payment/orders/${id}/refund-request`, data);
    },
    /** Get provider instance IDs that allow user refund */
    getRefundEligibleProviders() {
        return apiClient.get('/payment/orders/refund-eligible-providers');
    }
};
