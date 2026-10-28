import { apiClient } from './client';
export const studioBridgeAPI = {
    async launch() {
        const response = await apiClient.post('/user/studio-bridge/launch', {
            app_id: 'luoye-ai',
        });
        return response.data;
    },
    async sessionProbe(parentOrigin) {
        const response = await apiClient.get('/user/studio-bridge/session-probe', {
            params: {
                app_id: 'luoye-ai',
                parent_origin: parentOrigin,
            },
        });
        return response.data;
    },
};
