<template>
  <div class='rounded-xl border border-gray-200 dark:border-dark-700 bg-gray-50/50 dark:bg-dark-950/40 p-5'>
    <div class='flex items-center justify-between mb-5'>
      <h3 class='text-sm font-black text-gray-500 dark:text-dark-400'>渠道基础定价</h3>
      <span class='text-[10px] font-black uppercase text-gray-400 dark:text-dark-500 tracking-widest'>
        {{ billingModeLabel(pricing) }}
      </span>
    </div>

    <div class='grid grid-cols-3 gap-y-5 gap-x-4'>
      <div v-for='item in fullPriceItems(pricing)' :key='item.label' class='space-y-1.5'>
        <p class='text-[10px] font-bold text-gray-400 uppercase leading-none'>{{ item.label }}</p>
        <p class='text-sm font-black font-mono text-gray-900 dark:text-white leading-none break-all'>
          {{ formatTokenPrice(item.value) }}
        </p>
      </div>
    </div>

    <div v-if='isRequestBilling(pricing) || (pricing?.intervals?.length)' class='mt-5 pt-5 border-t border-gray-200 dark:border-dark-700 flex flex-wrap items-center gap-6'>
      <div v-if='isRequestBilling(pricing)' class='space-y-1.5'>
        <p class='text-[10px] font-bold text-gray-400 uppercase leading-none'>{{ pricing?.billing_mode === 'image' ? '每张价格' : '每次价格' }}</p>
        <p class='text-sm font-black font-mono text-primary-600 dark:text-primary-400 leading-none'>
          {{ formatRequestPrice(pricing?.per_request_price, pricing?.billing_mode) }}
        </p>
      </div>
      <div v-if='pricing?.intervals?.length' class='flex items-center gap-2 text-primary-600 dark:text-primary-400 bg-primary-500/5 px-4 py-2 rounded-xl border border-primary-500/10'>
        <Icon name='shield' size='xs' />
        <span class='text-[10px] font-black uppercase tracking-widest font-mono'>已配置 {{ pricing.intervals.length }} 档阶梯价格</span>
      </div>
    </div>
  </div>
</template>

<script setup lang='ts'>
import type { UserSupportedModelPricing } from '@/api/channels'
import Icon from '@/components/icons/Icon.vue'
import { formatTokenPrice, formatRequestPrice, billingModeLabel, isRequestBilling, fullPriceItems } from '../utils/pricing'

interface Props {
  pricing: UserSupportedModelPricing | null
}

defineProps<Props>()
</script>
