<template>
  <section :key='channel.key' class='p-6 space-y-5'>
    <div class='grid grid-cols-[3rem_minmax(0,1fr)] items-start gap-x-4 gap-y-3'>
      <div class='pt-1 text-xs font-black uppercase tracking-[0.16em] text-gray-400 dark:text-dark-500'>渠道</div>
      <div class='min-w-0 break-words text-sm font-bold text-gray-800 dark:text-gray-200'>{{ channel.name }}</div>
      <div class='pt-1 text-xs font-black uppercase tracking-[0.16em] text-gray-400 dark:text-dark-500'>分组</div>
      <div class='model-square-groups min-w-0 flex flex-wrap gap-2 rounded-xl border border-gray-200/70 bg-white/60 p-2.5 dark:border-dark-700/70 dark:bg-dark-950/30'>
        <GroupBadge
          v-for='entry in channel.entries'
          :key='entryKey(entry)'
          :name='entry.group.name'
          :platform='entry.group.platform as GroupPlatform'
          :subscription-type='entry.group.subscription_type as SubscriptionType'
          :rate-multiplier='entry.group.rate_multiplier'
          :user-rate-multiplier='userGroupRates[entry.group.id] ?? null'
          :peak-rate-enabled='entry.group.peak_rate_enabled'
          :peak-start='entry.group.peak_start'
          :peak-end='entry.group.peak_end'
          :peak-rate-multiplier='entry.group.peak_rate_multiplier'
          always-show-rate
          class='model-square-group-badge max-w-full min-w-0 rounded-lg px-3 py-1'
        />
      </div>
    </div>

    <ModelSquarePricingPanel :pricing='channel.pricing' />
  </section>
</template>

<script setup lang='ts'>
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { GroupPlatform, SubscriptionType } from '@/types'
import type { ModelSquareChannel } from '../types'
import { entryKey } from '../utils/key'
import ModelSquarePricingPanel from './ModelSquarePricingPanel.vue'

interface Props {
  channel: ModelSquareChannel
  userGroupRates: Record<number, number>
}

defineProps<Props>()
</script>

<style scoped>
.model-square-groups {
  transition: all 0.3s ease;
}
.model-square-group-badge {
  @apply transition-transform duration-200 hover:scale-110 active:scale-95;
}
</style>
