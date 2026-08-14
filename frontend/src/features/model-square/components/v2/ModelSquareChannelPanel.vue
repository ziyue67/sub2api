<template>
  <div class='border-t border-gray-100 dark:border-dark-800 p-5'>
    <div class='flex flex-col xl:flex-row xl:items-start gap-5'>
      <div class='min-w-0 flex-1'>
        <div class='flex items-center gap-2'>
          <span class='text-xs font-black uppercase tracking-widest text-gray-400 dark:text-dark-500'>渠道</span>
          <span class='text-sm font-bold text-gray-800 dark:text-gray-200'>{{ channel.name }}</span>
        </div>
        <div class='mt-2.5 flex flex-wrap gap-2'>
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
            class='max-w-full min-w-0 rounded-lg px-3 py-1'
          />
        </div>
      </div>
      <div class='flex-1 min-w-0'>
        <div class='flex items-center justify-between mb-2'>
          <span class='text-xs font-black uppercase tracking-widest text-gray-400 dark:text-dark-500'>基础定价</span>
          <span class='text-[10px] font-black uppercase tracking-widest text-primary-600 dark:text-primary-400'>{{ billingModeLabel(channel.pricing) }}</span>
        </div>
        <div class='grid grid-cols-3 gap-3'>
          <div v-for='item in priceItems' :key='item.label' class='space-y-1'>
            <span class='text-[10px] font-bold text-gray-400 dark:text-dark-500 uppercase'>{{ item.label }}</span>
            <span class='block text-xs font-black font-mono text-gray-900 dark:text-white'>{{ formatTokenPrice(item.value) }}</span>
          </div>
        </div>
        <div v-if='extraVisible' class='mt-2.5 flex flex-wrap gap-3 text-xs'>
          <div v-if='isRequestBilling(channel.pricing)' class='space-y-1'>
            <span class='text-[10px] font-bold text-gray-400 dark:text-dark-500 uppercase'>{{ requestPriceLabel }}</span>
            <span class='block text-xs font-black font-mono text-primary-600 dark:text-primary-400'>{{ formatRequestPrice(channel.pricing?.per_request_price, channel.pricing?.billing_mode) }}</span>
          </div>
          <div v-if='channel.pricing?.intervals?.length' class='inline-flex items-center gap-1.5 text-primary-600 dark:text-primary-400 bg-primary-500/5 px-3 py-1.5 rounded-lg border border-primary-500/10'>
            <Icon name='shield' size='xs' />
            <span class='text-[10px] font-black uppercase tracking-widest font-mono'>已配置 {{ channel.pricing.intervals.length }} 档阶梯价格</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang='ts'>
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { GroupPlatform, SubscriptionType } from '@/types'
import type { ModelSquareChannel } from '../../types'
import { entryKey } from '../../utils/key'
import { formatTokenPrice, formatRequestPrice, billingModeLabel, isRequestBilling, fullPriceItems } from '../../utils/pricing'

interface Props {
  channel: ModelSquareChannel
  userGroupRates: Record<number, number>
}

const props = defineProps<Props>()

const priceItems = computed(() => fullPriceItems(props.channel.pricing))
const extraVisible = computed(() => isRequestBilling(props.channel.pricing) || (props.channel.pricing?.intervals?.length ?? 0) > 0)
const requestPriceLabel = computed(() => (props.channel.pricing?.billing_mode === 'image' ? '每张价格' : '每次价格'))
</script>
