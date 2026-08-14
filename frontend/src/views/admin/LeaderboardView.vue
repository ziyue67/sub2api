<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Real backend search filters -->
      <UsageFilters
        v-model="filters"
        flat
        mode="ranking"
        :start-date="startDate"
        :end-date="endDate"
        :model-options="modelOptions"
        :model-creatable="true"
        :exporting="false"
        show-actions
        @change="applyFilters"
        @refresh="rankingRef?.reload"
        @reset="resetFilters"
      >
        <template #after-filters>
          <div class="w-full sm:w-auto sm:min-w-[160px]">
            <label class="input-label">{{ t('leaderboard.periodLabel') }}</label>
            <Select v-model="days" :options="periodOptions" @change="onDaysChange" />
          </div>
        </template>
      </UsageFilters>

      <!-- Ranking table -->
      <div class="card overflow-hidden">
        <UserTokenRanking
          ref="rankingRef"
          :start-date="startDate"
          :end-date="endDate"
          :filters="breakdownFilters"
          :model="filters.model"
          :limit="limit"
          :limit-options="limitOptions"
          @select-user="handleSelectUser"
        />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UsageFilters from '@/components/admin/usage/UsageFilters.vue'
import UserTokenRanking from '@/components/admin/usage/UserTokenRanking.vue'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { ModelStat } from '@/types'

const { t } = useI18n()
const router = useRouter()

type DaysWindow = 1 | 3 | 7 | 14 | 30

const days = ref<DaysWindow>(7)
const limit = ref(20)
const filters = ref<Record<string, any>>({})
const rankingRef = ref<InstanceType<typeof UserTokenRanking> | null>(null)
const requestedModelStats = ref<ModelStat[]>([])
const modelStatsLoading = ref(false)

const periodOptions = computed(() => [
  { value: 1, label: t('leaderboard.period.day1') },
  { value: 3, label: t('leaderboard.period.day3') },
  { value: 7, label: t('leaderboard.period.day7') },
  { value: 14, label: t('leaderboard.period.day14') },
  { value: 30, label: t('leaderboard.period.day30') },
])

const limitOptions = computed(() => [
  { value: 10, label: 'Top 10' },
  { value: 20, label: 'Top 20' },
  { value: 50, label: 'Top 50' },
  { value: 100, label: 'Top 100' },
])

const formatLocalDate = (d: Date) => {
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const computeRange = (d: DaysWindow) => {
  const end = new Date()
  const start = new Date(end.getTime())
  start.setDate(end.getDate() - (d - 1))
  return { start: formatLocalDate(start), end: formatLocalDate(end) }
}

const range = computed(() => computeRange(days.value))
const startDate = computed(() => range.value.start)
const endDate = computed(() => range.value.end)

const modelOptions = computed(() =>
  Array.from(new Set(requestedModelStats.value.map((m) => m.model).filter(Boolean))).sort()
)

// Exclude date fields because UserTokenRanking receives them separately.
const breakdownFilters = computed(() => {
  const f: Record<string, any> = {}
  const keys = ['user_id', 'api_key_id', 'account_id', 'group_id', 'request_type', 'billing_type', 'model']
  for (const k of keys) {
    if (filters.value[k] != null) f[k] = filters.value[k]
  }
  return f
})

const loadModelStats = async () => {
  modelStatsLoading.value = true
  try {
    const res = await adminAPI.dashboard.getModelStats({
      start_date: startDate.value,
      end_date: endDate.value,
      model_source: 'requested',
    })
    requestedModelStats.value = res.models || []
  } catch {
    requestedModelStats.value = []
  } finally {
    modelStatsLoading.value = false
  }
}

const onDaysChange = () => {
  loadModelStats()
}

const applyFilters = () => {
  // Ranking reload is triggered by the changed filters via UserTokenRanking watch.
}

const resetFilters = () => {
  filters.value = {}
  days.value = 7
  limit.value = 20
  loadModelStats()
}

const handleSelectUser = (userId: number) => {
  router.push({
    path: '/admin/usage',
    query: {
      user_id: String(userId),
      start_date: startDate.value,
      end_date: endDate.value,
    },
  })
}

onMounted(() => loadModelStats())
watch([startDate, endDate], () => loadModelStats())
</script>
