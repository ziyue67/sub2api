import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { useAuthStore } from './auth';
export const useAccountOpsStore = defineStore('accountOps', () => {
    const auth = useAuthStore();
    const remote = ref(null), draft = ref(null);
    const events = ref([]), hasMore = ref(false);
    watch(() => auth.user ? `${auth.user.id}:${auth.user.role}` : '', () => {
        remote.value = draft.value = null;
        events.value = [];
        hasMore.value = false;
    }, { flush: 'sync' });
    return { remote, draft, events, hasMore };
});
