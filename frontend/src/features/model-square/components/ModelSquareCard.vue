<template>
  <article class='bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-800 rounded-2xl shadow-sm hover:shadow-md transition-all duration-300 overflow-hidden flex flex-col'>
    <header class='px-6 py-5 flex items-start justify-between border-b border-gray-100 dark:border-dark-800 bg-gray-50/30 dark:bg-dark-950/20'>
      <div class='min-w-0'>
        <h2 class='text-xl font-black tracking-tight text-gray-900 dark:text-white truncate leading-tight'>{{ model.name }}</h2>
        <span class='mt-1 inline-block text-[10px] font-black uppercase text-gray-400 dark:text-dark-500 tracking-widest leading-none'>{{ model.platform }}</span>
      </div>
      <div class='shrink-0 text-xs font-black text-gray-400 dark:text-dark-500 uppercase tracking-widest'>
        {{ t('modelPlaza.detail.channelCount', { count: model.channels.length }) }}
      </div>
    </header>

    <div class='flex-1 divide-y divide-gray-100 dark:divide-dark-800'>
      <ModelSquareChannelSection
        v-for='channel in model.channels'
        :key='channel.key'
        :channel='channel'
        :user-group-rates='userGroupRates'
      />
    </div>
  </article>
</template>

<script setup lang='ts'>
import { useI18n } from 'vue-i18n'
import type { ModelSquareModel } from '../types'
import ModelSquareChannelSection from './ModelSquareChannelSection.vue'

interface Props {
  model: ModelSquareModel
  userGroupRates: Record<number, number>
}

defineProps<Props>()

const { t } = useI18n()
</script>
