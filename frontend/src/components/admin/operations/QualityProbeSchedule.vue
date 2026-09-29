<template>
  <div class="space-y-2 text-sm" data-testid="quality-probe-schedule">
    <label class="block space-y-1">
      <span>{{ t('qualityOps.probeInterval') }}</span>
      <select v-model="preset" class="input" data-testid="quality-probe-interval">
        <option v-for="option in presets" :key="option.cron" :value="option.cron">{{ t('qualityOps.probeEveryMinutes', { minutes: option.minutes }) }}</option>
        <option value="custom">{{ t('qualityOps.probeCustomSchedule') }}</option>
      </select>
    </label>
    <label v-if="preset === 'custom'" class="block space-y-1">
      <span>{{ t('qualityOps.cron') }}</span>
      <input v-model.trim="cron" required class="input font-mono" placeholder="*/2 * * * *" data-testid="quality-probe-cron" />
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('qualityOps.probeIntervalHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const cron = defineModel<string>({ required: true })
const { t } = useI18n()
// 预设只用整除 60 分钟或 24 小时的间隔，避免跨小时/日期时出现不等长间隔。
const presets = [1, 2, 5, 10, 15, 30, 60, 120, 360, 720, 1440].map(minutes => ({
  minutes,
  cron: minutes === 1 ? '* * * * *' : minutes < 60 ? `*/${minutes} * * * *`
    : minutes === 60 ? '0 * * * *' : minutes === 1440 ? '0 0 * * *' : `0 */${minutes / 60} * * *`,
}))
const custom = ref(false)
const preset = computed({
  get: () => !custom.value && presets.some(option => option.cron === cron.value) ? cron.value : 'custom',
  set: (value: string) => {
    custom.value = value === 'custom'
    if (!custom.value) cron.value = value
  },
})
</script>
