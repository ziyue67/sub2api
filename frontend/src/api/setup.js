/**
 * Setup API endpoints
 */
import axios from 'axios';
import { buildGatewayUrl } from './url';
// Create a separate client for setup endpoints (not under /api/v1)
const setupClient = axios.create({
    baseURL: buildGatewayUrl('/').replace(/\/+$/, ''),
    timeout: 30000,
    headers: {
        'Content-Type': 'application/json'
    }
});
/**
 * Get setup status
 */
export async function getSetupStatus() {
    const response = await setupClient.get('/setup/status');
    return response.data.data;
}
/**
 * Test database connection
 */
export async function testDatabase(config) {
    await setupClient.post('/setup/test-db', config);
}
/**
 * Test Redis connection
 */
export async function testRedis(config) {
    await setupClient.post('/setup/test-redis', config);
}
/**
 * Perform installation
 */
export async function install(config) {
    const response = await setupClient.post('/setup/install', config);
    return response.data.data;
}
