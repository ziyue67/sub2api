import { ref, reactive, onUnmounted, toRaw } from 'vue';
import { useDebounceFn } from '@vueuse/core';
import { getPersistedPageSize, setPersistedPageSize } from './usePersistedPageSize';
/**
 * 通用表格数据加载 Composable
 * 统一处理分页、筛选、搜索防抖和请求取消
 */
export function useTableLoader(options) {
    const { fetchFn, initialParams, pageSize, debounceMs = 300 } = options;
    const items = ref([]);
    const loading = ref(false);
    const params = reactive({ ...(initialParams || {}) });
    const pagination = reactive({
        page: 1,
        page_size: pageSize ?? getPersistedPageSize(),
        total: 0,
        pages: 0
    });
    let abortController = null;
    let disposed = false;
    const isAbortError = (error) => {
        return error?.name === 'AbortError' || error?.code === 'ERR_CANCELED' || error?.name === 'CanceledError';
    };
    const load = async () => {
        // A pending debounce can outlive the component scope. Never start another
        // request after disposal, even when the debounce helper cannot be canceled.
        if (disposed)
            return;
        if (abortController) {
            abortController.abort();
        }
        const currentController = new AbortController();
        abortController = currentController;
        loading.value = true;
        try {
            const response = await fetchFn(pagination.page, pagination.page_size, toRaw(params), { signal: currentController.signal });
            if (disposed || currentController.signal.aborted)
                return;
            items.value = response.items || [];
            pagination.total = response.total || 0;
            pagination.pages = response.pages || 0;
        }
        catch (error) {
            if (!isAbortError(error)) {
                console.error('Table load error:', error);
                throw error;
            }
        }
        finally {
            if (abortController === currentController) {
                loading.value = false;
            }
        }
    };
    const reload = () => {
        pagination.page = 1;
        return load();
    };
    const debouncedReload = useDebounceFn(reload, debounceMs);
    const handlePageChange = (page) => {
        // 确保页码在有效范围内
        const validPage = Math.max(1, Math.min(page, pagination.pages || 1));
        pagination.page = validPage;
        load();
    };
    const handlePageSizeChange = (size) => {
        pagination.page_size = size;
        pagination.page = 1;
        setPersistedPageSize(size);
        load();
    };
    onUnmounted(() => {
        disposed = true;
        abortController?.abort();
    });
    return {
        items,
        loading,
        params,
        pagination,
        load,
        reload,
        debouncedReload,
        handlePageChange,
        handlePageSizeChange
    };
}
