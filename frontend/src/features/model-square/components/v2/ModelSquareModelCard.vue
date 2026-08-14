<template>
  <article
    :id='cardId'
    :data-model-key='model.key'
    class='model-card scroll-mt-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-800 rounded-2xl shadow-sm hover:shadow-md transition-all duration-300 overflow-hidden'
  >
    <header
      class='px-6 py-4 flex items-center justify-between border-b border-gray-100 dark:border-dark-800 bg-gray-50/30 dark:bg-dark-950/20 cursor-pointer select-none'
      @click='toggle'
    >
      <div class='min-w-0'>
        <div class='flex items-center gap-2.5'>
          <h2 class='text-lg font-black tracking-tight text-gray-900 dark:text-white truncate leading-tight'>{{ model.name }}</h2>
          <span class='inline-flex items-center rounded-md bg-gray-100 dark:bg-dark-800 px-2 py-0.5 text-[10px] font-black uppercase text-gray-500 dark:text-dark-400 tracking-wider'>{{ model.platform }}</span>
        </div>
        <p class='mt-1 text-[10px] font-bold text-gray-400 dark:text-dark-500 uppercase tracking-widest'>{{ model.channels.length }} 个渠道</p>
      </div>
      <div class='shrink-0 flex items-center gap-2'>
        <span class='text-[10px] font-bold text-gray-400 dark:text-dark-500 uppercase tracking-widest'>{{ isOpen ? "收起配置" : "查看配置" }}</span>
        <Icon name='chevronDown' size='sm' :class='isOpen ? "rotate-180" : ""' />
      </div>
    </header>

    <div v-show='isOpen' class='divide-y divide-gray-100 dark:divide-dark-800'>
      <ModelSquareChannelPanel
        v-for='channel in model.channels'
        :key='channel.key'
        :channel='channel'
        :user-group-rates='userGroupRates'
      />
    </div>
  </article>
</template>

<script setup lang='ts'>
import { ref, computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import type { ModelSquareModel } from '../../types'
import ModelSquareChannelPanel from './ModelSquareChannelPanel.vue'

interface Props {
  model: ModelSquareModel
  userGroupRates: Record<number, number>
}

const props = defineProps<Props>()
const cardId = computed(() => 'model-' + props.model.key)
const isOpen = ref(false)

function toggle() {
  isOpen.value = !isOpen.value
}
</script>
