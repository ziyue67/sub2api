<template>
  <div class='mb-6 flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between'>
    <div>
      <h1 class='text-3xl font-black tracking-tight text-gray-900 dark:text-white'>模型广场</h1>
      <p class='mt-1.5 text-sm text-gray-500 dark:text-dark-400'>按模型汇聚渠道、可用分组和渠道基础定价。</p>
    </div>
    <div class='flex items-center gap-3'>
      <div class='relative group flex-1 lg:flex-none'>
        <Icon name='search' size='sm' class='absolute left-3.5 top-1/2 -translate-y-1/2 text-gray-400 group-focus-within:text-primary-500 transition-colors' />
        <input
          :value='search'
          type='text'
          aria-label='搜索模型、渠道、平台或分组'
          class='w-full lg:w-80 h-10 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-800 rounded-lg py-2 pl-10 pr-4 text-sm font-medium outline-none focus:ring-2 focus:ring-primary-500/20 focus:border-primary-500 transition-all shadow-sm'
          placeholder='搜索模型、渠道、平台或分组...'
          @input='$emit("update:search", ($event.target as HTMLInputElement).value)'
        />
      </div>
      <button
        type='button'
        class='flex h-10 w-10 items-center justify-center rounded-lg bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-800 hover:bg-gray-50 dark:hover:bg-dark-800 transition-all shadow-sm active:scale-95'
        :disabled='loading'
        aria-label='刷新'
        @click='$emit("refresh")'
      >
        <Icon name='refresh' size='sm' :class='iconClass' />
      </button>
    </div>
  </div>
</template>

<script setup lang='ts'>
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'

interface Props {
  search: string
  loading: boolean
}

const props = defineProps<Props>()

defineEmits<{
  'update:search': [value: string]
  refresh: []
}>()

const iconClass = computed(() => (props.loading ? 'animate-spin' : ''))
</script>
