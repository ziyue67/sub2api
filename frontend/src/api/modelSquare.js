import { apiClient } from './client';
export async function listModelSquare(options) {
    const { data } = await apiClient.get('/model-square', {
        signal: options?.signal
    });
    return data;
}
export const modelSquareAPI = { list: listModelSquare };
export default modelSquareAPI;
