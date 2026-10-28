/**
 * User Subscription API
 * API for regular users to view their own subscriptions and progress
 */
import { apiClient } from './client';
/**
 * Get list of current user's subscriptions
 */
export async function getMySubscriptions() {
    const response = await apiClient.get('/subscriptions');
    return response.data;
}
/**
 * Get current user's active subscriptions
 */
export async function getActiveSubscriptions() {
    const response = await apiClient.get('/subscriptions/active');
    return response.data;
}
/**
 * Get progress for all user's active subscriptions
 */
export async function getSubscriptionsProgress() {
    const response = await apiClient.get('/subscriptions/progress');
    return response.data;
}
/**
 * Get subscription summary for dashboard display
 */
export async function getSubscriptionSummary() {
    const response = await apiClient.get('/subscriptions/summary');
    return response.data;
}
/**
 * Get progress for a specific subscription
 */
export async function getSubscriptionProgress(subscriptionId) {
    const response = await apiClient.get(`/subscriptions/${subscriptionId}/progress`);
    return response.data;
}
export default {
    getMySubscriptions,
    getActiveSubscriptions,
    getSubscriptionsProgress,
    getSubscriptionSummary,
    getSubscriptionProgress
};
